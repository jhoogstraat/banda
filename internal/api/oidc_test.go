package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSession(t *testing.T) {
	o := signer("secret")
	now := time.Now()
	v := o.sign("alice|1", now.Add(time.Hour))
	if id, ok := o.verify(v, now); !ok || id != "alice|1" {
		t.Fatalf("valid session: got %q, %v", id, ok)
	}
	if _, ok := o.verify(o.sign("alice", now.Add(time.Hour))[1:], now); ok {
		t.Fatal("tampered payload accepted")
	}
	if _, ok := signer("other").verify(v, now); ok {
		t.Fatal("session signed with another secret accepted")
	}
	if _, ok := o.verify(v, now.Add(2*time.Hour)); ok {
		t.Fatal("expired session accepted")
	}
	if _, ok := o.verify("", now); ok {
		t.Fatal("empty session accepted")
	}
}

func TestLocalPath(t *testing.T) {
	for in, want := range map[string]string{
		"/t/abc?x=1": "/t/abc?x=1", "": "/", "https://evil.com": "/", "//evil.com": "/", "/\\evil.com": "/",
		"/./\\evil.com": "/", "/\t/evil.com": "/", "/%5Cevil.com": "/%5Cevil.com", "evil.com": "/",
	} {
		if got := localPath(in); got != want {
			t.Errorf("localPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// A previewed app on the same host can plant its owner's session with a
// narrower path, which the browser sends before the viewer's own.
func TestSessionCookieShadowing(t *testing.T) {
	o := &OIDC{signer: signer("secret")}
	exp := time.Now().Add(time.Hour)
	alice, mallory := o.sign("alice", exp), o.sign("mallory", exp)
	for header, want := range map[string]string{
		"banda_session=" + alice:                                "alice",
		"banda_session=forged; banda_session=" + alice:          "alice",
		"banda_session=" + alice + "; banda_session=" + alice:   "alice",
		"banda_session=" + mallory + "; banda_session=" + alice: "",
		"banda_preview=" + alice + "; other=1":                  "",
	} {
		if got, _ := o.session(header); got != want {
			t.Errorf("session(%q) = %q, want %q", header, got, want)
		}
	}

	// Over https only a __Host- cookie counts: other hosts of the site can't set one.
	o.secure = true
	if got, _ := o.session("banda_session=" + mallory + "; __Host-banda_session=" + alice); got != "alice" {
		t.Errorf("https session = %q, want alice", got)
	}
}

// A bundled Dex (DEX_URL) has the issuer $PUBLIC_URL/dex: the API discovers it
// without reaching the public URL, and browsers reach it through the API's proxy.
func TestDexProxy(t *testing.T) {
	const issuer = "http://banda.invalid/dex"
	dex := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dex/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]string{"issuer": issuer, "authorization_endpoint": issuer + "/auth",
				"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys"})
		case "/dex/auth":
			fmt.Fprint(w, "login form, q=", r.URL.RawQuery)
		default:
			http.NotFound(w, r)
		}
	}))
	defer dex.Close()

	o, err := NewOIDC(t.Context(), nil, OIDCConfig{Issuer: issuer, ClientID: "banda", PublicURL: "http://banda.invalid", DexURL: dex.URL})
	if err != nil {
		t.Fatalf("discovery through DEX_URL: %v", err)
	}
	mux := http.NewServeMux()
	o.Mount(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("GET", "http://banda.invalid/dex/auth?client_id=banda", nil))
	if got := rec.Body.String(); rec.Code != 200 || got != "login form, q=client_id=banda" {
		t.Fatalf("proxied /dex/auth: %d %q", rec.Code, got)
	}

	// Other hosts go where they point.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "other") }))
	defer other.Close()
	res, err := o.client.Get(other.URL)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(res.Body); string(b) != "other" {
		t.Fatalf("other host: %q", b)
	}
}

// Only an explicit false (some providers send a string) leaves an email
// unverified; Entra ID sends no claim at all.
func TestEmailVerified(t *testing.T) {
	for claim, want := range map[any]bool{true: true, nil: true, "true": true, false: false, "false": false} {
		if got := emailVerified(claim); got != want {
			t.Errorf("emailVerified(%#v) = %v", claim, got)
		}
	}
}
