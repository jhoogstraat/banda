package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jhoogstraat/banda/internal/forge"
)

func TestFlowState(t *testing.T) {
	g := &gitHub{key: signer("k")}
	state := g.key.sign("connect|alice|github.com|nonce", time.Now().Add(time.Minute))
	if u, host, extra, ok := g.checkState(state, "connect", "alice"); !ok || u != "alice" || host != "github.com" || len(extra) != 0 {
		t.Errorf("valid state: %q %q %q %v", u, host, extra, ok)
	}
	app := g.key.sign("app|alice|github.com|nonce|acme|42", time.Now().Add(time.Minute))
	if _, _, extra, ok := g.checkState(app, "app", "alice"); !ok || strings.Join(extra, " ") != "acme 42" {
		t.Errorf("an app state's organization and the app it replaces: %q %v", extra, ok)
	}
	for name, c := range map[string]struct{ state, kind, user string }{
		"another user's browser": {state, "connect", "mallory"},
		"signed out":             {state, "connect", ""},
		"another flow":           {state, "app", "alice"},
		"tampered":               {"x" + state[1:], "connect", "alice"},
		"another API's":          {signer("other").sign("connect|alice|github.com|nonce", time.Now().Add(time.Minute)), "connect", "alice"},
		"expired":                {g.key.sign("connect|alice|github.com|nonce", time.Now().Add(-time.Second)), "connect", "alice"},
		"malformed":              {g.key.sign("connect|alice", time.Now().Add(time.Minute)), "connect", "alice"},
	} {
		if _, _, _, ok := g.checkState(c.state, c.kind, c.user); ok {
			t.Errorf("%s: accepted", name)
		}
	}
	if v := g.verifier(state); len(v) != 43 || v != g.verifier(state) || v == g.verifier(state+"x") {
		t.Errorf("PKCE verifier %q: want 43 characters, the same for a state, another for another", v)
	}
}

// fakeGitHub answers the OAuth token endpoint like GitHub: a refresh token
// works once. It counts refreshes.
func fakeGitHub(t *testing.T) (*forge.App, *int) {
	t.Helper()
	var mu sync.Mutex
	refreshes, valid := 0, map[string]bool{"ghr_1": true}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		old := r.Form.Get("refresh_token")
		if r.URL.Path != "/login/oauth/access_token" || !valid[old] {
			json.NewEncoder(w).Encode(map[string]string{"error": "bad_refresh_token"})
			return
		}
		time.Sleep(20 * time.Millisecond) // concurrent callers pile up meanwhile
		delete(valid, old)
		refreshes++
		next := fmt.Sprintf("ghr_%d", refreshes+1)
		valid[next] = true
		json.NewEncoder(w).Encode(map[string]any{"access_token": fmt.Sprintf("ghu_%d", refreshes+1), "refresh_token": next, "expires_in": 28800})
	}))
	t.Cleanup(srv.Close)
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	a, err := forge.Config{TestURL: srv.URL}.App(map[string][]byte{"app-id": []byte("1"), "slug": []byte("b"), "client-id": []byte("c"),
		"client-secret": []byte("s"), "private-key": pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})})
	if err != nil {
		t.Fatal(err)
	}
	return a, &refreshes
}

// memKeeper keeps tokens in a map, as the Secret would; saving fails while fail is set.
func memKeeper(stored map[string][]byte, fail *bool) *tokenKeeper {
	var mu sync.Mutex
	return &tokenKeeper{conns: map[string]*userConn{},
		load: func(context.Context, string, string) (map[string][]byte, error) {
			mu.Lock()
			defer mu.Unlock()
			return stored, nil
		},
		save: func(_ context.Context, _, _ string, data map[string][]byte) error {
			mu.Lock()
			defer mu.Unlock()
			if *fail {
				return errors.New("kubernetes is down")
			}
			for k, v := range data {
				stored[k] = v
			}
			return nil
		}}
}

// Refresh tokens work once: callers racing on an expiring token get one
// refresh between them, and the new pair is saved before anyone uses it.
func TestTokenRotation(t *testing.T) {
	a, refreshes := fakeGitHub(t)
	stored := encodeUserToken(forge.UserToken{Access: "ghu_1", Refresh: "ghr_1", Expiry: time.Now().Add(time.Minute)})
	fail := false
	k := memKeeper(stored, &fail)
	var wg sync.WaitGroup
	got := make([]string, 20)
	for i := range got {
		wg.Go(func() {
			tok, err := k.token(context.Background(), a, "alice")
			if err != nil {
				t.Error(err)
			}
			got[i] = tok
		})
	}
	wg.Wait()
	if *refreshes != 1 {
		t.Errorf("%d refreshes, want 1", *refreshes)
	}
	for _, tok := range got {
		if tok != "ghu_2" {
			t.Fatalf("got %v, want all ghu_2", got)
		}
	}
	if string(stored["refresh-token"]) != "ghr_2" || string(stored["access-token"]) != "ghu_2" {
		t.Errorf("saved %s", stored)
	}

	// Saving fails after a refresh: the new pair is kept and saved first next
	// time, without spending the refresh token again.
	k.conn("alice", a.Host).tok.Expiry = time.Now()
	fail = true
	if _, err := k.token(context.Background(), a, "alice"); err == nil {
		t.Fatal("a refresh that couldn't be saved was used")
	}
	fail = false
	if tok, err := k.token(context.Background(), a, "alice"); tok != "ghu_3" || err != nil || *refreshes != 2 || string(stored["refresh-token"]) != "ghr_3" {
		t.Errorf("after the failed save: %q %v, %d refreshes, saved %s", tok, err, *refreshes, stored["refresh-token"])
	}

	// A refresh token GitHub refuses: the connection is revoked.
	k.forget("alice", a.Host)
	stored["refresh-token"], stored["expires-at"] = []byte("ghr_1"), []byte(time.Now().Format(time.RFC3339))
	if _, err := k.token(context.Background(), a, "alice"); !errors.Is(err, forge.ErrRevoked) {
		t.Errorf("spent refresh token: %v", err)
	}
	if u, host := k.owner("ghu_3"); u != "" || host != "" {
		t.Errorf("owner of a forgotten token: %q", u)
	}
}

// Not connected is no token and no error; a token that never expires is used as is.
func TestTokenKeeper(t *testing.T) {
	a, refreshes := fakeGitHub(t)
	fail := false
	if tok, err := memKeeper(map[string][]byte{}, &fail).token(context.Background(), a, "bob"); tok != "" || err != nil {
		t.Errorf("not connected: %q, %v", tok, err)
	}
	k := memKeeper(encodeUserToken(forge.UserToken{Access: "ghu_forever"}), &fail)
	if tok, _ := k.token(context.Background(), a, "bob"); tok != "ghu_forever" || *refreshes != 0 {
		t.Errorf("no expiry: %q, %d refreshes", tok, *refreshes)
	}
	if u, host := k.owner("ghu_forever"); u != "bob" || host != a.Host {
		t.Errorf("owner: %q %q", u, host)
	}
}

// The permission gate asks the forge once per user and repository within 5 minutes.
func TestRepoAccessCached(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasSuffix(r.Header.Get("Authorization"), "ghu_alice") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"permissions": map[string]bool{"pull": true}})
	}))
	defer srv.Close()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	a, _ := forge.Config{TestURL: srv.URL}.App(map[string][]byte{"app-id": []byte("1"), "slug": []byte("b"), "client-id": []byte("c"),
		"client-secret": []byte("s"), "private-key": pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})})
	s := &Service{gh: newGitHub(nil)}
	for range 3 {
		if got, err := s.repoAccess(context.Background(), a, "alice", 1, "ghu_alice", "o", "r"); got != "read" || err != nil {
			t.Fatalf("got %q, %v", got, err)
		}
	}
	if calls != 1 {
		t.Errorf("%d calls, want 1", calls)
	}
	// Reconnected to another GitHub account: its access is asked afresh.
	s.repoAccess(context.Background(), a, "alice", 3, "ghu_alice", "o", "r")
	if calls != 2 {
		t.Errorf("%d calls: another account of alice's got the first one's answer", calls)
	}
	var fe *forge.Error
	if _, err := s.repoAccess(context.Background(), a, "bob", 2, "ghu_revoked", "o", "r"); !errors.As(err, &fe) || fe.Code != forge.Unauthorized {
		t.Errorf("a refused token: %v", err)
	}
}

// A refresh in flight when the connection is forgotten (a disconnect, a
// revocation) can't bring its tokens back once the caller deleted them:
// forget waits for it, and the connection is dead from then on.
func TestForgetDuringRefresh(t *testing.T) {
	a, _ := fakeGitHub(t)
	var mu sync.Mutex
	stored := encodeUserToken(forge.UserToken{Access: "ghu_1", Refresh: "ghr_1", Expiry: time.Now().Add(time.Minute)})
	saving, release := make(chan struct{}), make(chan struct{})
	k := &tokenKeeper{conns: map[string]*userConn{},
		load: func(context.Context, string, string) (map[string][]byte, error) {
			mu.Lock()
			defer mu.Unlock()
			return maps.Clone(stored), nil
		},
		save: func(_ context.Context, _, _ string, data map[string][]byte) error {
			saving <- struct{}{}
			<-release
			mu.Lock()
			defer mu.Unlock()
			stored = data
			return nil
		}}
	refreshed := make(chan string)
	go func() {
		tok, _ := k.token(context.Background(), a, "alice")
		refreshed <- tok
	}()
	<-saving // refreshed, saving the new pair
	forgotten := make(chan struct{})
	go func() {
		k.forget("alice", a.Host)
		mu.Lock()
		stored = nil // what connectionRefused does next: delete the Secret
		mu.Unlock()
		close(forgotten)
	}()
	select {
	case <-forgotten:
		t.Fatal("forget returned while a refresh was still saving: the Secret it deletes comes back")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-forgotten
	<-refreshed
	if stored != nil {
		t.Errorf("the forgotten connection's tokens were saved again: %s", stored)
	}
	if tok, err := k.token(context.Background(), a, "alice"); tok != "" || err != nil {
		t.Errorf("after forget: %q, %v; want not connected", tok, err)
	}
}

// A connect flow returns only to a thread's page: anything else is an open redirect.
func TestBackTo(t *testing.T) {
	for back, want := range map[string]string{
		"/t/0b7c6d2e-9a1f-4c3b-8e5d-1f2a3b4c5d6e": "/t/0b7c6d2e-9a1f-4c3b-8e5d-1f2a3b4c5d6e",
		"": "", "/settings": "", "//evil.example/t/x": "", "https://evil.example/t/x": "", "/t/x/../../evil": "",
		"/t/x?y=1": "", "/t/x#y": "", "/t/x\\evil": "", "/t/x|y": "", "/t/x\n": "",
	} {
		r := httptest.NewRequest("GET", "/github/connect?"+url.Values{"back": {back}}.Encode(), nil)
		if got := backTo(r); got != want {
			t.Errorf("backTo(%q) = %q, want %q", back, got, want)
		}
	}
}
