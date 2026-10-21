package api

import (
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
