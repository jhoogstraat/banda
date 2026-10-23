package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/jhoogstraat/banda/internal/forge"
	"github.com/jhoogstraat/banda/internal/store"
)

// fakeGitLab answers GitLab's token endpoint: a refresh token works once and
// its successor keeps the grant's scope. It counts refreshes per grant.
func fakeGitLab(t *testing.T) (*forge.GitLabApp, map[string]int) {
	t.Helper()
	var mu sync.Mutex
	refreshes := map[string]int{}
	valid := map[string]string{"api-r1": "api", "git-r1": "git"} // refresh token: grant
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		grant, ok := valid[r.Form.Get("refresh_token")]
		if r.URL.Path != "/oauth/token" || r.Form.Get("client_secret") != "s" || !ok {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		time.Sleep(20 * time.Millisecond) // concurrent callers pile up meanwhile
		delete(valid, r.Form.Get("refresh_token"))
		refreshes[grant]++
		n := refreshes[grant] + 1
		valid[fmt.Sprintf("%s-r%d", grant, n)] = grant
		json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprintf("%s-a%d", grant, n), "refresh_token": fmt.Sprintf("%s-r%d", grant, n),
			"expires_in": 7200, "token_type": "Bearer"})
	}))
	t.Cleanup(srv.Close)
	a, err := forge.Config{TestURL: srv.URL}.GitLabApp(map[string][]byte{"application-id": []byte("c"), "secret": []byte("s")})
	if err != nil {
		t.Fatal(err)
	}
	return a, refreshes
}

// The two grants share one Secret, each under its own keys, and rotate on
// their own: racing callers of one get one refresh between them, saved
// before use, and the other grant's refresh token is untouched.
func TestGitLabTokenRotation(t *testing.T) {
	a, refreshes := fakeGitLab(t)
	soon := time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	stored := map[string][]byte{"access-token": []byte("api-a1"), "refresh-token": []byte("api-r1"), "expires-at": []byte(soon),
		"git-access-token": []byte("git-a1"), "git-refresh-token": []byte("git-r1"), "git-expires-at": []byte(soon)}
	fail := false
	api := memKeeper(stored, &fail)
	git := prefixed(api, gitPrefix)
	var wg sync.WaitGroup
	got := make([]string, 20)
	for i := range got {
		k := api
		if i%2 == 1 {
			k = git
		}
		wg.Go(func() {
			tok, err := k.fresh(context.Background(), a, a.Host, "alice")
			if err != nil {
				t.Error(err)
			}
			got[i] = tok.Access
		})
	}
	wg.Wait()
	if refreshes["api"] != 1 || refreshes["git"] != 1 {
		t.Errorf("refreshes %v, want one per grant", refreshes)
	}
	for i, tok := range got {
		if want := []string{"api-a2", "git-a2"}[i%2]; tok != want {
			t.Fatalf("got %v, want api-a2 and git-a2 alternating", got)
		}
	}
	for k, want := range map[string]string{"access-token": "api-a2", "refresh-token": "api-r2", "git-access-token": "git-a2", "git-refresh-token": "git-r2"} {
		if string(stored[k]) != want {
			t.Errorf("saved %s = %s, want %s", k, stored[k], want)
		}
	}
	// A spent refresh token: that grant is revoked, the other still works.
	git.forget("alice", a.Host)
	stored["git-refresh-token"], stored["git-expires-at"] = []byte("git-r1"), []byte(soon)
	if _, err := git.fresh(context.Background(), a, a.Host, "alice"); !errors.Is(err, forge.ErrRevoked) {
		t.Errorf("spent refresh token: %v", err)
	}
	if tok, err := api.fresh(context.Background(), a, a.Host, "alice"); tok.Access != "api-a2" || err != nil {
		t.Errorf("the api grant after git's was refused: %q %v", tok.Access, err)
	}
}

// gitLabService is a Service whose caches already hold host's application
// (nil: none) and alice's two grants, so nothing reaches Kubernetes.
func gitLabService(t *testing.T, app *forge.GitLabApp, host string, stored map[string][]byte) *Service {
	t.Helper()
	fail := false
	s := &Service{gh: newGitHub(nil), gl: newGitLabs(nil), tokens: forgeTokens{m: map[string]cachedToken{}}}
	s.gh.appAt = time.Now() // no GitHub App
	s.gl.apps[host] = cachedGitLabApp{app: app, at: time.Now()}
	s.gl.api = memKeeper(stored, &fail)
	s.gl.git = prefixed(s.gl.api, gitPrefix)
	return s
}

// git gets the git grant's token, never the api grant's, as oauth2 with its expiry.
func TestGitLabCredential(t *testing.T) {
	a, _ := fakeGitLab(t)
	later := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	s := gitLabService(t, a, a.Host, map[string][]byte{"access-token": []byte("api-x"), "expires-at": []byte(later.Format(time.RFC3339)),
		"git-access-token": []byte("git-x"), "git-expires-at": []byte(later.Format(time.RFC3339))})
	th := store.Thread{ID: "t", OwnerID: "alice", RepoURL: "https://gitlab.com/g/sub/p.git"}
	repo, _ := Forges.Repo(th.RepoURL)
	res, err := s.gitLabCredential(context.Background(), th, repo)
	if err != nil {
		t.Fatal(err)
	}
	if res.Username != "oauth2" || res.Password != "git-x" || !res.ExpiresAt.AsTime().Equal(later) {
		t.Errorf("got %s %s %v", res.Username, res.Password, res.ExpiresAt.AsTime())
	}

	// No application on the host: a reason to act on.
	s = gitLabService(t, nil, "gitlab.com", map[string][]byte{})
	_, err = s.gitLabCredential(context.Background(), th, repo)
	var ce *connect.Error
	if !errors.As(err, &ce) || ce.Code() != connect.CodeFailedPrecondition || !strings.HasPrefix(ce.Message(), "set up GitLab for gitlab.com in Settings") {
		t.Errorf("no application: %v", err)
	}
}

// forgeToken on a GitLab host: the api grant's token once connected, never
// git's; otherwise the legacy secret, as before.
func TestGitLabForgeToken(t *testing.T) {
	a, _ := fakeGitLab(t)
	legacy := func(s *Service) {
		s.tokens.m["alice "+forge.GitLab] = cachedToken{value: "glpat-legacy", name: "GITLAB_TOKEN", at: time.Now()}
	}
	connected := gitLabService(t, a, a.Host, map[string][]byte{"access-token": []byte("api-x"), "git-access-token": []byte("git-x")})
	legacy(connected)
	if tok, err := connected.forgeToken(context.Background(), "alice", forge.GitLab, a.Host); tok.Value != "api-x" || tok.Fix != "reconnect GitLab in Settings" || err != nil {
		t.Errorf("connected: %+v %v", tok, err)
	}
	for name, s := range map[string]*Service{"not connected": gitLabService(t, a, a.Host, map[string][]byte{}), "no application": gitLabService(t, nil, a.Host, map[string][]byte{})} {
		legacy(s)
		if tok, err := s.forgeToken(context.Background(), "alice", forge.GitLab, a.Host); tok.Value != "glpat-legacy" || tok.Fix != "check GITLAB_TOKEN in Settings" || err != nil {
			t.Errorf("%s: %+v %v", name, tok, err)
		}
	}
}

// An abandoned flow's api grant (the second consent never came back) is
// revoked on GitLab when the next flow sweeps it, not just forgotten; a
// pending grant is found only by its own flow's nonce.
func TestGitLabPendingSwept(t *testing.T) {
	var revoked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/oauth/token":
			w.Write([]byte(`{"access_token":"new","refresh_token":"new-r","expires_in":7200,"scope":"api"}`))
		case "/oauth/revoke":
			revoked = append(revoked, r.Form.Get("token"))
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	a, err := forge.Config{TestURL: srv.URL}.GitLabApp(map[string][]byte{"application-id": []byte("c"), "secret": []byte("s")})
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{gh: newGitHub(nil), gl: newGitLabs(nil), PublicURL: "https://banda"}
	s.gl.apps["gitlab.com"] = cachedGitLabApp{app: a, at: time.Now()}
	s.gl.pending["old"] = pendingGrant{"u1", "gitlab.com", forge.UserToken{Refresh: "abandoned"}, time.Now().Add(-11 * time.Minute)}
	s.gl.pending["recent"] = pendingGrant{"u2", "gitlab.com", forge.UserToken{Refresh: "waiting"}, time.Now()}
	next, err := s.gitLabFirstGrant(context.Background(), "u1", "gitlab.com", "code", "state", "")
	if err != nil || !strings.Contains(next, "scope=write_repository") {
		t.Fatal(next, err)
	}
	if !slices.Equal(revoked, []string{"abandoned"}) {
		t.Errorf("revoked %v", revoked)
	}
	if _, ok := s.gl.pending["recent"]; !ok || len(s.gl.pending) != 2 {
		t.Errorf("pending: %v", s.gl.pending)
	}
	u, _ := url.Parse(next)
	_, _, extra, ok := s.gh.checkState(u.Query().Get("state"), "gitlab-git", "u1")
	if p, found := s.takePending(extra[0]); !ok || !found || p.user != "u1" || p.tok.Refresh != "new-r" {
		t.Errorf("the flow's own pending grant: %v %v %+v", ok, found, p)
	}
}
