package forge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Each grant asks for its one scope with PKCE, and a token with more than
// was asked for (a git token that could call the API) is refused.
func TestGitLabAppScopes(t *testing.T) {
	granted := "api"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/oauth/token" && r.Form.Get("code_verifier") == "v":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "a", "refresh_token": "r", "expires_in": 7200, "scope": granted})
		case r.URL.Path == "/oauth/revoke" && r.Form.Get("client_secret") == "s":
			w.Write([]byte("{}"))
		case r.URL.Path == "/oauth/revoke":
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte(`{"error":"invalid_client"}`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()
	a, err := Config{TestURL: srv.URL}.GitLabApp(map[string][]byte{"application-id": []byte("c"), "secret": []byte("s")})
	if err != nil || a.Host != "gitlab.com" {
		t.Fatal(a, err)
	}
	for _, scope := range []string{GitLabAPIScope, GitLabGitScope} {
		u, _ := url.Parse(a.AuthorizeURL("https://banda/gitlab/callback", scope, "st", "v"))
		q := u.Query()
		if u.Path != "/oauth/authorize" || q.Get("scope") != scope || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" ||
			q.Get("client_id") != "c" || q.Get("state") != "st" || q.Get("client_secret") != "" {
			t.Errorf("%s: %s", scope, u)
		}
	}
	if _, err := a.Exchange(context.Background(), "https://banda/gitlab/callback", GitLabAPIScope, "code", "v"); err != nil {
		t.Errorf("api grant: %v", err)
	}
	if _, err := a.Exchange(context.Background(), "https://banda/gitlab/callback", GitLabGitScope, "code", "v"); err == nil {
		t.Error("a git grant with the api scope was accepted")
	}

	if err := a.Verify(context.Background()); err != nil {
		t.Errorf("verify: %v", err)
	}
	a.ClientSecret = "wrong"
	var fe *Error
	if err := a.Verify(context.Background()); !errors.As(err, &fe) || fe.Code != Refused {
		t.Errorf("verify a wrong secret: %v", err)
	}
	if _, err := (Config{}).GitLabApp(map[string][]byte{"host": []byte("github.com"), "application-id": []byte("c"), "secret": []byte("s")}); err == nil {
		t.Error("a GitHub host was accepted")
	}
}

// Only invalid_grant means the user's grant is gone. Refused client
// credentials are the team's application needing attention; rate limits,
// other refusals and outages keep the stored pair.
func TestGitLabAppRefresh(t *testing.T) {
	status, body := 0, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	defer srv.Close()
	a, _ := Config{TestURL: srv.URL}.GitLabApp(map[string][]byte{"application-id": []byte("c"), "secret": []byte("s")})
	refresh := func(s int, b string) error {
		status, body = s, b
		_, err := a.Refresh(context.Background(), "r")
		return err
	}
	if err := refresh(400, `{"error":"invalid_grant"}`); !errors.Is(err, ErrRevoked) {
		t.Errorf("invalid_grant: %v", err)
	}
	for _, c := range []struct {
		status int
		body   string
	}{{401, `{"error":"invalid_client"}`}, {400, `{"error":"unauthorized_client"}`}, {429, `slow down`}, {400, `{"error":"invalid_request"}`}, {403, ``}, {502, `bad gateway`}} {
		var fe *Error
		if err := refresh(c.status, c.body); errors.Is(err, ErrRevoked) || !errors.As(err, &fe) || fe.Code != Unavailable {
			t.Errorf("%d %s: %v", c.status, c.body, err)
		}
		if want := strings.Contains(c.body, "client"); a.Misconfigured() != want {
			t.Errorf("%d %s: misconfigured %v", c.status, c.body, a.Misconfigured())
		}
		if strings.Contains(c.body, "client") {
			status, body = 200, `{"access_token":"a","refresh_token":"r2","expires_in":7200}`
			a.Refresh(context.Background(), "r")
		}
	}
}

// A token without a granted scope is refused: Banda can't tell what it reaches.
// Owner is the token's resource owner, from Doorkeeper's token info.
func TestGitLabAppEmptyScopeAndOwner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth/token":
			json.NewEncoder(w).Encode(map[string]any{"access_token": "a", "refresh_token": "r", "expires_in": 7200})
		case "/oauth/token/info":
			if r.Header.Get("Authorization") != "Bearer a" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Write([]byte(`{"resource_owner_id":42,"scope":["write_repository"],"application":{"uid":"c"}}`))
		}
	}))
	defer srv.Close()
	a, _ := Config{TestURL: srv.URL}.GitLabApp(map[string][]byte{"application-id": []byte("c"), "secret": []byte("s")})
	if _, err := a.Exchange(context.Background(), "https://banda/gitlab/callback", GitLabGitScope, "code", "v"); err == nil {
		t.Error("a token without a scope was accepted")
	}
	if id, err := a.Owner(context.Background(), "a"); id != 42 || err != nil {
		t.Errorf("owner: %d, %v", id, err)
	}
	if _, err := a.Owner(context.Background(), "other"); err == nil {
		t.Error("owner of a refused token")
	}
}
