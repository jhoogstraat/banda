package api

import (
	"testing"
	"time"
)

func TestSession(t *testing.T) {
	o := &OIDC{secret: []byte("secret")}
	now := time.Now()
	v := o.sign("alice|1", now.Add(time.Hour))
	if id, ok := o.verify(v, now); !ok || id != "alice|1" {
		t.Fatalf("valid session: got %q, %v", id, ok)
	}
	if _, ok := o.verify(o.sign("alice", now.Add(time.Hour))[1:], now); ok {
		t.Fatal("tampered payload accepted")
	}
	if _, ok := (&OIDC{secret: []byte("other")}).verify(v, now); ok {
		t.Fatal("session signed with another secret accepted")
	}
	if _, ok := o.verify(v, now.Add(2*time.Hour)); ok {
		t.Fatal("expired session accepted")
	}
	if _, ok := o.verify("", now); ok {
		t.Fatal("empty session accepted")
	}
}
