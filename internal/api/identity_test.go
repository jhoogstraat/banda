package api

import (
	"strings"
	"testing"
	"time"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestAudienceFile(t *testing.T) {
	for in, want := range map[string]string{
		"sts.amazonaws.com": "sts.amazonaws.com", "vault": "vault", "https://vault.example.com": "vault.example.com",
		"api://AzureADTokenExchange": "AzureADTokenExchange", "a/b:c": "a_b_c", "..": "_..", ".hidden": "_.hidden",
		"https://": "_", "//iam.googleapis.com/projects/1/x": "__iam.googleapis.com_projects_1_x", "ü": "_",
	} {
		if got := audienceFile(in); got != want {
			t.Errorf("audienceFile(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRepoClaim(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/Acme/api.git": "github.com/Acme/api", "https://GitHub.com/acme/api/": "github.com/acme/api",
		"git@github.com:acme/api.git": "github.com/acme/api", "ssh://git@gitlab.example:2222/g/s/r.git": "gitlab.example:2222/g/s/r",
		"https://github.com:443/acme/api": "github.com/acme/api", "ssh://git@github.com:22/acme/api": "github.com/acme/api",
		"git://e2e-git/plain.git": "e2e-git/plain", "": "",
		// local repositories name nobody's: no host to bind
		"file:///opt/e2e/plain.git": "", "file:///github.com/acme/api": "", "/github.com/acme/api": "",
		// nothing a policy or a recipe could misread
		"https://github.com/acme/api$(id)": "", "https://github.com/acme/a'pi": "", "https://github.com/acme/../x": "",
		"https://github.com/acme/%2e%2e/x": "", "https://github.com/acme//x": "", "https://github.com/acme/a%20b": "",
		"git@github.com:acme/a\"b": "",
	} {
		if got := repoClaim(in); got != want {
			t.Errorf("repoClaim(%q) = %q, want %q", in, got, want)
		}
	}
}

// Only an email the IdP vouched for, of one user, names a token's subject.
func TestIdentityRefusal(t *testing.T) {
	for _, c := range []struct {
		email            string
		verified, shared bool
		refused          string
	}{
		{"ana@acme.io", true, false, ""},
		{"", true, false, "signed in without one"},
		{"ana@acme.io", false, false, "hasn't verified"},
		{"ana@acme.io", true, true, "another Banda user"},
	} {
		if got := identityRefusal(c.email, c.verified, c.shared); c.refused == "" && got != "" || !strings.Contains(got, c.refused) {
			t.Errorf("identityRefusal(%+v) = %q", c, got)
		}
	}
}

// Changes within a second reach each runner as one IdTokensChanged.
func TestIdTokensChangedCoalesces(t *testing.T) {
	s := &Service{runners: map[string]*runner{}}
	r := &runner{ch: make(chan *bandav1.ConnectResponse, 16), done: make(chan struct{})}
	s.runners["thread"] = r
	s.runners[loginKey("ana")] = &runner{ch: make(chan *bandav1.ConnectResponse, 16), done: make(chan struct{})}
	for range 10 {
		s.idTokensChanged()
	}
	time.Sleep(1500 * time.Millisecond)
	if n := len(r.ch); n != 1 {
		t.Fatalf("%d messages, want 1", n)
	}
	if n := len(s.runners[loginKey("ana")].ch); n != 0 {
		t.Fatalf("the login environment got %d", n)
	}
	s.idTokensChanged()
	time.Sleep(1500 * time.Millisecond)
	if n := len(r.ch); n != 2 {
		t.Fatalf("%d messages after a later change, want 2", n)
	}
}
