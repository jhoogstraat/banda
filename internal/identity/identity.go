// Package identity makes Banda an OIDC issuer of short-lived workload
// identity tokens: each thread's pod gets JWTs naming its owner, which
// secret managers and clouds that trust Banda's issuer (as they trust GitHub
// Actions') exchange for their own short-lived credentials. The signing keys
// live in the Secret banda-identity, which no pod mounts; discovery and the
// public keys are served at the issuer, unauthenticated.
package identity

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Lifetime is how long a token is valid. Runners refresh well before.
const Lifetime = 15 * time.Minute

const (
	DiscoveryPath = "/.well-known/openid-configuration"
	JWKSPath      = "/.well-known/jwks.json"
)

// Claims are what a token says beyond the standard ones.
type Claims struct {
	Audience string
	Email    string // the thread's owner, verified by the IdP; sub is "user:<email>"
	Thread   string
	Repo     string // host/path, empty without a repository
	Branch   string
	// NoJTI leaves jti out, for a verifier that takes each jti once: a client
	// exchanging the same file twice, as claude does after a restart, would be
	// refused until the next refresh. The token's lifetime still bounds it.
	NoJTI bool
}

// Issuer signs tokens with the newest key that is due and publishes all of them.
type Issuer struct {
	URL       string
	keys      []key // oldest first
	discovery []byte
	jwks      []byte
}

type key struct {
	from time.Time // signs from then on; see New
	kid  string
	priv *rsa.PrivateKey
}

// New parses the keys, PEM RSA private keys named "<unix seconds>.pem" after
// when they start signing: a key added with a time in the future is
// published at once and signs only from then on, so verifiers' caches have
// it before the first token they need it for. issuer has no trailing slash.
func New(issuer string, pems map[string][]byte) (*Issuer, error) {
	i := &Issuer{URL: issuer}
	for name, data := range pems {
		secs, err := strconv.ParseInt(strings.TrimSuffix(name, ".pem"), 10, 64)
		if err != nil || !strings.HasSuffix(name, ".pem") {
			return nil, fmt.Errorf("signing key %q: want <unix seconds>.pem", name)
		}
		priv, err := parseKey(data)
		if err != nil {
			return nil, fmt.Errorf("signing key %q: %w", name, err)
		}
		i.keys = append(i.keys, key{time.Unix(secs, 0), thumbprint(&priv.PublicKey), priv})
	}
	if len(i.keys) == 0 {
		return nil, errors.New("no signing key")
	}
	slices.SortFunc(i.keys, func(a, b key) int { return a.from.Compare(b.from) })
	jwks := struct {
		Keys []map[string]string `json:"keys"`
	}{}
	for _, k := range i.keys {
		jwks.Keys = append(jwks.Keys, jwk(&k.priv.PublicKey, k.kid))
	}
	var err error
	if i.jwks, err = json.Marshal(jwks); err != nil {
		return nil, err
	}
	i.discovery, err = json.Marshal(map[string]any{
		"issuer":                                issuer,
		"jwks_uri":                              issuer + JWKSPath,
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"response_types_supported":              []string{"id_token"},
		"subject_types_supported":               []string{"public"},
		"claims_supported":                      []string{"iss", "aud", "sub", "iat", "nbf", "exp", "jti", "email", "email_verified", "thread", "repo", "branch"},
	})
	return i, err
}

func parseKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("not PEM")
	}
	var k any
	k, err := x509.ParsePKCS1PrivateKey(block.Bytes) // openssl genrsa
	if err != nil {
		if k, err = x509.ParsePKCS8PrivateKey(block.Bytes); err != nil { // openssl genpkey
			return nil, err
		}
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok || rk.N.BitLen() < 2048 {
		return nil, errors.New("not an RSA key of at least 2048 bits")
	}
	return rk, nil
}

// FirstKey is the key a new install starts with, as the Secret's data.
func FirstKey() (map[string][]byte, error) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		return nil, err
	}
	name := strconv.FormatInt(time.Now().Unix(), 10) + ".pem"
	return map[string][]byte{name: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})}, nil
}

var b64 = base64.RawURLEncoding

func jwk(pub *rsa.PublicKey, kid string) map[string]string {
	return map[string]string{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": kid,
		"n": b64.EncodeToString(pub.N.Bytes()), "e": b64.EncodeToString(big.NewInt(int64(pub.E)).Bytes())}
}

// thumbprint is the key's RFC 7638 thumbprint: SHA-256 over its required
// members in lexicographic order, without whitespace.
func thumbprint(pub *rsa.PublicKey) string {
	sum := sha256.Sum256(fmt.Appendf(nil, `{"e":"%s","kty":"RSA","n":"%s"}`,
		b64.EncodeToString(big.NewInt(int64(pub.E)).Bytes()), b64.EncodeToString(pub.N.Bytes())))
	return b64.EncodeToString(sum[:])
}

// signer is the newest key due at now; the oldest if none is yet.
func (i *Issuer) signer(now time.Time) key {
	k := i.keys[0]
	for _, c := range i.keys[1:] {
		if !c.from.After(now) {
			k = c
		}
	}
	return k
}

// Mint signs a token for c, valid from now for Lifetime.
func (i *Issuer) Mint(c Claims, now time.Time) (string, time.Time, error) {
	k := i.signer(now)
	exp := now.Add(Lifetime)
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": k.kid})
	if err != nil {
		return "", exp, err
	}
	claims := map[string]any{
		"iss": i.URL, "aud": c.Audience, "sub": "user:" + c.Email,
		"iat": now.Unix(), "nbf": now.Unix(), "exp": exp.Unix(),
		"email": c.Email, "email_verified": true, "thread": c.Thread, "repo": c.Repo, "branch": c.Branch,
	}
	if !c.NoJTI {
		claims["jti"] = rand.Text()
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", exp, err
	}
	signed := b64.EncodeToString(header) + "." + b64.EncodeToString(payload)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(nil, k.priv, crypto.SHA256, sum[:])
	if err != nil {
		return "", exp, err
	}
	return signed + "." + b64.EncodeToString(sig), exp, nil
}

// Handler serves discovery and the public keys: public, and cacheable for
// 5 minutes, so a new key needs that long to reach verifiers.
func (i *Issuer) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := i.discovery
		if r.URL.Path == JWKSPath {
			body = i.jwks
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.Header().Set("Access-Control-Allow-Origin", "*") // public keys, as other issuers serve them
		w.Write(body)
	})
}
