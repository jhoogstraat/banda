package forge

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"
)

var testKey, _ = rsa.GenerateKey(rand.Reader, 2048)

func testApp(t *testing.T, base string) *App {
	t.Helper()
	key := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(testKey)})
	a, err := Config{TestURL: base}.App(map[string][]byte{"app-id": []byte("42"), "slug": []byte("banda"), "client-id": []byte("Iv1.abc"),
		"client-secret": []byte("shh"), "private-key": key})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAppFromSecret(t *testing.T) {
	a := testApp(t, "")
	if a.Host != "github.com" || a.ID != 42 || a.InstallURL() != "https://github.com/apps/banda/installations/new" {
		t.Errorf("got %+v, install at %s", a, a.InstallURL())
	}
	for name, data := range map[string]map[string][]byte{
		"no key":      {"app-id": []byte("1"), "slug": []byte("s"), "client-id": []byte("c"), "client-secret": []byte("x")},
		"bad id":      {"app-id": []byte("x")},
		"gitlab host": {"host": []byte("gitlab.com"), "app-id": []byte("1")},
	} {
		if _, err := (Config{}).App(data); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The JWT is RS256 by the app's key, issued a minute ago against clock drift
// and valid at most 10 minutes from then.
func TestAppJWT(t *testing.T) {
	now := time.Now()
	jwt, err := testApp(t, "").JWT(now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(jwt, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&testKey.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature: %v", err)
	}
	var claims struct {
		Iat, Exp int64
		Iss      string
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	json.Unmarshal(raw, &claims)
	if claims.Iss != "Iv1.abc" || claims.Iat != now.Unix()-60 || claims.Exp-claims.Iat > 600 || claims.Exp <= now.Unix() {
		t.Errorf("claims %+v", claims)
	}
}

// An installation token reaches one repository: write (with workflows) or
// read. It is reused until 10 minutes before it expires.
func TestRepoToken(t *testing.T) {
	expires := time.Hour
	mints := 0
	f, base := newFake(t, map[string]any{
		"POST /api/v3/app/installations/7/access_tokens": func(r *http.Request) (int, any) {
			mints++
			return http.StatusCreated, map[string]any{"token": "ghs_" + strings.Repeat("x", 36), "expires_at": time.Now().Add(expires).Format(time.RFC3339)}
		},
	})
	a := testApp(t, base)
	ctx := context.Background()
	for range 2 {
		if tok, err := a.RepoToken(ctx, "th", 7, "r", true); err != nil || !strings.HasPrefix(tok.Token, "ghs_") {
			t.Fatalf("got %+v, %v", tok, err)
		}
	}
	if mints != 1 {
		t.Errorf("minted %d tokens for one repository, want 1", mints)
	}
	if !strings.HasPrefix(f.auth[0], "Bearer ey") {
		t.Errorf("authenticated with %q, want the app's JWT", f.auth[0])
	}
	body, _ := json.Marshal(f.posted["POST /api/v3/app/installations/7/access_tokens"])
	if want := `{"permissions":{"contents":"write","metadata":"read","workflows":"write"},"repositories":["r"]}`; string(body) != want {
		t.Errorf("asked for %s, want %s", body, want)
	}
	a.RepoToken(ctx, "th", 7, "r", false)
	body, _ = json.Marshal(f.posted["POST /api/v3/app/installations/7/access_tokens"])
	if want := `{"permissions":{"contents":"read","metadata":"read"},"repositories":["r"]}`; string(body) != want || mints != 2 {
		t.Errorf("read: asked for %s (%d mints), want %s", body, mints, want)
	}
	if tok, _ := a.RepoToken(ctx, "another thread", 7, "r", false); mints != 3 || tok.Token == "" {
		t.Errorf("%d mints: another thread got the first thread's token", mints)
	}
	expires = 5 * time.Minute // within the 10 minutes: never reused
	a.RepoToken(ctx, "th", 7, "other", true)
	a.RepoToken(ctx, "th", 7, "other", true)
	if mints != 5 {
		t.Errorf("%d mints, want a new token for one about to expire", mints)
	}
}

func TestInstallation(t *testing.T) {
	calls := 0
	_, base := newFake(t, map[string]any{
		"GET /api/v3/repos/o/r/installation": func(*http.Request) (int, any) { calls++; return http.StatusOK, map[string]any{"id": 7} },
	})
	a := testApp(t, base)
	for range 2 {
		if id, err := a.Installation(context.Background(), "o", "r"); id != 7 || err != nil {
			t.Fatalf("got %d, %v", id, err)
		}
	}
	if calls != 1 {
		t.Errorf("%d lookups, want 1 (cached)", calls)
	}
	if _, err := a.Installation(context.Background(), "o", "x"); !errors.Is(err, ErrNotInstalled) {
		t.Errorf("not installed: %v", err)
	}
}

// A refresh token GitHub refuses (it answers 200 with an error) means the
// connection is gone; one it can't be asked about is not.
func TestRefresh(t *testing.T) {
	answer := map[string]any{"error": "bad_refresh_token"}
	_, base := newFake(t, map[string]any{"POST /login/oauth/access_token": func(*http.Request) (int, any) { return http.StatusOK, answer }})
	a := testApp(t, base)
	if _, err := a.Refresh(context.Background(), "ghr_old"); !errors.Is(err, ErrRevoked) {
		t.Errorf("refused: %v", err)
	}
	answer = map[string]any{"access_token": "ghu_new", "refresh_token": "ghr_new", "expires_in": 28800, "token_type": "bearer"}
	got, err := a.Refresh(context.Background(), "ghr_old")
	if err != nil || got.Access != "ghu_new" || got.Refresh != "ghr_new" || time.Until(got.Expiry) < 7*time.Hour {
		t.Errorf("got %+v, %v", got, err)
	}
	if _, err := testApp(t, "http://127.0.0.1:1").Refresh(context.Background(), "ghr_old"); err == nil || errors.Is(err, ErrRevoked) {
		t.Errorf("unreachable: %v, want an error that keeps the connection", err)
	}
	answer = map[string]any{"error": "invalid_grant"}
	if _, err := a.Refresh(context.Background(), "ghr_old"); !errors.Is(err, ErrRevoked) {
		t.Errorf("invalid_grant: %v", err)
	}
}

// Only the forge refusing the grant ends a connection: the app's own
// credentials refused, a rate limit or an outage say so and keep it.
func TestRefreshUnavailable(t *testing.T) {
	status, answer := http.StatusOK, any(nil)
	_, base := newFake(t, map[string]any{"POST /login/oauth/access_token": func(*http.Request) (int, any) { return status, answer }})
	a := testApp(t, base)
	for name, c := range map[string]struct {
		status int
		answer any
	}{
		"client credentials": {http.StatusOK, map[string]any{"error": "incorrect_client_credentials"}},
		"invalid client":     {http.StatusUnauthorized, map[string]any{"error": "invalid_client"}},
		"rate limited":       {http.StatusTooManyRequests, map[string]any{"message": "slow down"}},
		"outage":             {http.StatusBadGateway, map[string]any{}},
		"forbidden":          {http.StatusForbidden, map[string]any{}},
	} {
		status, answer = c.status, c.answer
		var fe *Error
		if _, err := a.Refresh(context.Background(), "ghr_old"); errors.Is(err, ErrRevoked) || !errors.As(err, &fe) || fe.Code != Unavailable {
			t.Errorf("%s: %v, want unavailable", name, err)
		}
	}
}

// The app from a manifest is kept only as Banda asked for it: private, its
// permissions exactly, no events, no webhook elsewhere, owned as asked.
func TestDiffers(t *testing.T) {
	public, hook := false, ""
	_, base := newFake(t, map[string]any{
		"GET /api/v3/apps/banda": func(r *http.Request) (int, any) {
			if public && r.Header.Get("Authorization") == "" {
				return http.StatusOK, map[string]any{"slug": "banda"}
			}
			return http.StatusNotFound, map[string]any{"message": "Not Found"}
		},
		"GET /api/v3/app/hook/config": func(*http.Request) (int, any) { return http.StatusOK, map[string]any{"url": hook} },
	})
	a := testApp(t, base)
	ctx := context.Background()
	asked := func() *NewApp { return &NewApp{Owner: "acme", Permissions: maps.Clone(AppPermissions)} }
	if why, err := a.Differs(ctx, asked(), "https://b.example", "ACME"); why != "" || err != nil {
		t.Fatalf("as asked: %q, %v", why, err)
	}
	more := asked()
	more.Permissions["administration"] = "write"
	events := asked()
	events.Events = []string{"push"}
	personal := asked()
	personal.Personal = true
	for name, c := range map[string]struct {
		n         *NewApp
		org, want string
		public    bool
		hook      string
	}{
		"more permissions":        {more, "acme", "other permissions", false, ""},
		"fewer permissions":       {&NewApp{Owner: "acme", Permissions: map[string]string{"contents": "write"}}, "acme", "other permissions", false, ""},
		"events":                  {events, "acme", "webhook events", false, ""},
		"another organization":    {asked(), "other", "owned by acme, not the organization other", false, ""},
		"a user, not the org":     {personal, "acme", "owned by acme, not the organization acme", false, ""},
		"an org, not a user":      {asked(), "", "owned by the organization acme, not a personal account", false, ""},
		"public":                  {asked(), "acme", "public", true, ""},
		"a webhook elsewhere":     {asked(), "acme", "a webhook to https://evil.example/hook", false, "https://evil.example/hook"},
		"Banda's webhook is fine": {asked(), "acme", "", false, "https://b.example/github/webhook"},
	} {
		public, hook = c.public, c.hook
		if why, err := a.Differs(ctx, c.n, "https://b.example", c.org); why != c.want || err != nil {
			t.Errorf("%s: %q, %v; want %q", name, why, err, c.want)
		}
	}
}

func TestManifest(t *testing.T) {
	var m struct {
		Public      bool
		RedirectURL string                `json:"redirect_url"`
		Hook        struct{ Active bool } `json:"hook_attributes"`
		Permissions map[string]string     `json:"default_permissions"`
		Events      []string              `json:"default_events"`
	}
	if err := json.Unmarshal([]byte(Manifest("Banda (b.example)", "https://b.example")), &m); err != nil {
		t.Fatal(err)
	}
	if m.Public || m.Hook.Active || len(m.Events) != 0 || m.RedirectURL != "https://b.example/github/app" ||
		m.Permissions["workflows"] != "write" || m.Permissions["contents"] != "write" || len(m.Permissions) != 6 {
		t.Errorf("manifest %+v", m)
	}
	if got := (Config{}).NewAppURL("github.com", "acme", "s"); got != "https://github.com/organizations/acme/settings/apps/new?state=s" {
		t.Errorf("NewAppURL: %s", got)
	}
}

// What a user's token may do with a repository: push is write, pull read,
// and a repository the app doesn't reach (404) nothing.
func TestAccess(t *testing.T) {
	_, base := newFake(t, map[string]any{
		"GET /api/v3/repos/o/w": map[string]any{"permissions": map[string]bool{"push": true, "pull": true}},
		"GET /api/v3/repos/o/r": map[string]any{"permissions": map[string]bool{"pull": true}},
		"GET /api/v3/repos/o/n": map[string]any{"permissions": map[string]bool{}},
	})
	a := testApp(t, base)
	for name, want := range map[string]string{"w": "write", "r": "read", "n": "", "missing": ""} {
		if got, err := a.Access(context.Background(), "ghu_x", "o", name); got != want || err != nil {
			t.Errorf("%s: got %q, %v; want %q", name, got, err, want)
		}
	}
}
