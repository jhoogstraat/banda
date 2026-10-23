package identity

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// serve starts an issuer on a test server, at the server's URL.
func serve(t *testing.T, keys map[string][]byte) (*Issuer, *httptest.Server) {
	t.Helper()
	var i *Issuer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { i.Handler().ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)
	var err error
	if i, err = New(srv.URL, keys); err != nil {
		t.Fatal(err)
	}
	return i, srv
}

func firstKey(t *testing.T) map[string][]byte {
	t.Helper()
	k, err := FirstKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// Tokens verify with go-oidc against the served discovery and keys: what a
// real consumer (Vault, Kubernetes) does.
func TestMintVerifies(t *testing.T) {
	i, srv := serve(t, firstKey(t))
	ctx := context.Background()
	p, err := oidc.NewProvider(ctx, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	tok, exp, err := i.Mint(Claims{Audience: "vault", Email: "alice@example.com", Thread: "t1", Repo: "github.com/acme/api", Branch: "banda/t1"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Sub(now) != Lifetime {
		t.Errorf("expires after %v", exp.Sub(now))
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: "vault"}).Verify(ctx, tok)
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Sub, Email, Thread, Repo, Branch, Jti string
		Iat, Nbf, Exp                         int64
		Verified                              bool `json:"email_verified"`
	}
	if err := idt.Claims(&c); err != nil {
		t.Fatal(err)
	}
	if c.Sub != "user:alice@example.com" || c.Email != "alice@example.com" || c.Thread != "t1" || c.Repo != "github.com/acme/api" ||
		c.Branch != "banda/t1" || c.Jti == "" || c.Iat != now.Unix() || c.Nbf != now.Unix() || c.Exp != now.Add(15*time.Minute).Unix() || !c.Verified {
		t.Errorf("claims: %+v", c)
	}
	if idt.Issuer != srv.URL {
		t.Errorf("issuer %q", idt.Issuer)
	}
	if _, err := p.Verifier(&oidc.Config{ClientID: "other"}).Verify(ctx, tok); err == nil {
		t.Error("verified for another audience")
	}
	late := p.Verifier(&oidc.Config{ClientID: "vault", Now: func() time.Time { return now.Add(16 * time.Minute) }})
	if _, err := late.Verify(ctx, tok); err == nil {
		t.Error("verified after expiry")
	}
	other, _ := serve(t, firstKey(t))
	forged, _, _ := other.Mint(Claims{Audience: "vault", Email: "mallory@example.com"}, now)
	if _, err := p.Verifier(&oidc.Config{ClientID: "vault"}).Verify(ctx, forged); err == nil {
		t.Error("verified a token signed by another key")
	}
	r, err := http.Get(srv.URL + DiscoveryPath)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.Header.Get("Cache-Control") != "public, max-age=300" {
		t.Errorf("Cache-Control %q", r.Header.Get("Cache-Control"))
	}
}

// Every key is published; the newest due one signs, a future one not yet.
func TestKeys(t *testing.T) {
	now := time.Now()
	old, current, future := pemKey(t, now.Add(-time.Hour)), pemKey(t, now.Add(-time.Minute)), pemKey(t, now.Add(time.Hour))
	keys := map[string][]byte{}
	for _, k := range []map[string][]byte{old, current, future} {
		for n, v := range k {
			keys[n] = v
		}
	}
	i, srv := serve(t, keys)
	r, err := http.Get(srv.URL + JWKSPath)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	var jwks struct {
		Keys []struct{ Kid, Kty, Alg, Use, N, E string }
	}
	if err := json.NewDecoder(r.Body).Decode(&jwks); err != nil {
		t.Fatal(err)
	}
	if len(jwks.Keys) != 3 || jwks.Keys[0].Kty != "RSA" || jwks.Keys[0].Alg != "RS256" || jwks.Keys[0].E != "AQAB" {
		t.Fatalf("jwks: %+v", jwks)
	}
	if got, want := i.signer(now).kid, i.keys[1].kid; got != want {
		t.Errorf("signs with %s, want the current key %s", got, want)
	}
	if got, want := i.signer(now.Add(2*time.Hour)).kid, i.keys[2].kid; got != want {
		t.Errorf("after its time signs with %s, want the future key %s", got, want)
	}
	// Tokens of every published key verify.
	p, err := oidc.NewProvider(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{now, now.Add(2 * time.Hour)} {
		tok, _, _ := i.Mint(Claims{Audience: "a", Email: "a@b"}, at)
		v := p.Verifier(&oidc.Config{ClientID: "a", Now: func() time.Time { return at }})
		if _, err := v.Verify(context.Background(), tok); err != nil {
			t.Errorf("at %v: %v", at, err)
		}
	}
}

// pemKey is a PKCS#1 key (openssl genrsa) due at from.
func pemKey(t *testing.T, from time.Time) map[string][]byte {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return map[string][]byte{strconv.FormatInt(from.Unix(), 10) + ".pem": pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})}
}

// The kid is RFC 7638's thumbprint: its example key from section 3.1.
func TestThumbprint(t *testing.T) {
	n := "0vx7agoebGcQSuuPiLJXZptN9nndrQmbXEps2aiAFbWhM78LhWx4cbbfAAtVT86zwu1RK7aPFFxuhDR1L6tSoc_BJECPebWKRXjBZCiFV4n3oknjhMstn64tZ_2W-5JsGY4Hc5n9yBXArwl93lqt7_RN5w6Cf0h4QyQ5v-65YGjQR0_FDW2QvzqY368QQMicAtaSqzs8KJZgnYb9c7d0zgdAZHzu6qMQvRL5hajrn1n91CbOpbISD08qNLyrdkt-bFTWhAI4vMQFh6WeZu0fM4lFd2NcRwr3XPksINHaQ-G_xBniIqbw0Ls1jF44-csFCur-kEgU8awapJzKnqDKgw"
	var pub rsa.PublicKey
	nb, _ := b64.DecodeString(n)
	pub.N = new(big.Int).SetBytes(nb)
	pub.E = 65537
	if got := thumbprint(&pub); got != "NzbLsXh8uDCcd-6MNwXF4W_7noWXFZAfHkxZsRGC9Xs" {
		t.Errorf("thumbprint %s", got)
	}
}

func TestBadKeys(t *testing.T) {
	small, _ := rsa.GenerateKey(rand.Reader, 1024)
	der, _ := x509.MarshalPKCS8PrivateKey(small)
	for name, keys := range map[string]map[string][]byte{
		"none":     {},
		"name":     {"key.pem": []byte("x")},
		"not pem":  {"1.pem": []byte("hello")},
		"1024 bit": {"1.pem": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})},
	} {
		if _, err := New("https://banda.example", keys); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
