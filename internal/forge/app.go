package forge

import (
	"bytes"
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
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/go-github/v93/github"
	"golang.org/x/oauth2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// A team's GitHub App: git in sandboxes gets its installation tokens, which
// last an hour and reach the thread's repository only, and forge calls go out
// with each user's own token from the app's OAuth flow ("Connect GitHub").
// The private key is the one long-lived credential; it stays in the API.

// App is a GitHub App, read from its Secret (see AppSecret).
type App struct {
	Host, Slug, ClientID, ClientSecret string
	ID                                 int64
	key                                *rsa.PrivateKey
	base                               string // https://<host>, or the test's fake

	mu            sync.Mutex
	installs      map[string]installation // owner/name
	tokens        map[string]RepoToken    // thread, installation, repo and access
	misconfigured atomic.Bool             // logged once
}

type installation struct {
	id int64
	at time.Time
}

// RepoToken is an installation token for one repository.
type RepoToken struct {
	Token   string
	Expires time.Time
}

// AppSecret holds what the Kubernetes Secret banda-github-app keys: made by
// Banda from the manifest flow, or by hand (Helm, GitOps).
var AppSecret = struct{ Host, ID, Slug, ClientID, ClientSecret, PrivateKey string }{
	"host", "app-id", "slug", "client-id", "client-secret", "private-key"}

// App reads a GitHub App from its Secret's data.
func (c Config) App(data map[string][]byte) (*App, error) {
	get := func(k string) string { return strings.TrimSpace(string(data[k])) }
	a := &App{Host: strings.ToLower(get(AppSecret.Host)), Slug: get(AppSecret.Slug), ClientID: get(AppSecret.ClientID),
		ClientSecret: get(AppSecret.ClientSecret), installs: map[string]installation{}, tokens: map[string]RepoToken{}}
	if a.Host == "" {
		a.Host = "github.com"
	}
	if kind := c.Known()[a.Host]; kind != GitHub {
		return nil, fmt.Errorf("%s isn't a GitHub host: name it in FORGE_HOSTS as %s=github", a.Host, a.Host)
	}
	var err error
	if a.ID, err = strconv.ParseInt(get(AppSecret.ID), 10, 64); err != nil {
		return nil, fmt.Errorf("%s isn't an app id", AppSecret.ID)
	}
	if a.Slug == "" || a.ClientID == "" || a.ClientSecret == "" {
		return nil, fmt.Errorf("it needs %s, %s and %s", AppSecret.Slug, AppSecret.ClientID, AppSecret.ClientSecret)
	}
	if a.key, err = parseKey(data[AppSecret.PrivateKey]); err != nil {
		return nil, err
	}
	a.base = c.Base(a.Host)
	return a, nil
}

// parseKey reads the app's private key: GitHub hands out PKCS#1 PEM.
func parseKey(b []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("its private-key isn't PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if rk, ok := k.(*rsa.PrivateKey); err == nil && ok {
		return rk, nil
	}
	return nil, errors.New("its private-key isn't an RSA key")
}

// URL is the app's page on the forge, InstallURL where accounts install it.
func (a *App) URL() string        { return a.base + "/apps/" + a.Slug }
func (a *App) InstallURL() string { return a.URL() + "/installations/new" }

// AppPermissions are what the app asks for: what internal/forge/github.go
// calls need (pull requests, their checks and statuses), pushing
// (contents) and pushing workflow files (workflows), which branches that
// touch .github/workflows need.
var AppPermissions = map[string]string{"contents": "write", "metadata": "read", "pull_requests": "write", "checks": "read",
	"statuses": "read", "workflows": "write"}

// Manifest is the GitHub App manifest Banda posts to the forge: private, its
// webhook off, OAuth back to publicURL/github/callback, and after the
// conversion back to publicURL/github/app; installing returns to
// publicURL/github/installed, which goes on to Settings.
func Manifest(name, publicURL string) string {
	b, _ := json.Marshal(map[string]any{
		"name": name, "url": publicURL, "public": false,
		"redirect_url":             publicURL + "/github/app",
		"callback_urls":            []string{publicURL + "/github/callback"},
		"setup_url":                publicURL + "/github/installed",
		"setup_on_update":          true,
		"request_oauth_on_install": false,
		"hook_attributes":          map[string]any{"url": publicURL + "/github/webhook", "active": false},
		"default_permissions":      AppPermissions,
		"default_events":           []string{},
	})
	return string(b)
}

// NewAppURL is where the browser posts the manifest: the user's own apps, or an organization's.
func (c Config) NewAppURL(host, org, state string) string {
	path := "/settings/apps/new"
	if org != "" {
		path = "/organizations/" + url.PathEscape(org) + path
	}
	return c.Base(host) + path + "?" + url.Values{"state": {state}}.Encode()
}

// NewApp is the app the forge made from Banda's manifest, as the forge
// describes it.
type NewApp struct {
	Data        map[string][]byte // its Secret's
	Owner       string
	Personal    bool // owned by a user account, not an organization
	Permissions map[string]string
	Events      []string
}

// ConvertManifest exchanges the code the forge returned for the new app's
// keys and what the forge says the app is.
func (c Config) ConvertManifest(ctx context.Context, host, code string) (*NewApp, error) {
	gh, err := gitHubClient(c.Base(host), "")
	if err != nil {
		return nil, err
	}
	// go-github's AppConfig drops permissions, events and the owner's type.
	req, err := gh.NewRequest(ctx, http.MethodPost, "app-manifests/"+url.PathEscape(code)+"/conversions", nil)
	if err != nil {
		return nil, err
	}
	var v struct {
		github.AppConfig
		Owner       struct{ Login, Type string } `json:"owner"`
		Permissions map[string]string            `json:"permissions"`
		Events      []string                     `json:"events"`
	}
	if _, err := gh.Do(req, &v); err != nil {
		return nil, ghError(err, "create the app again")
	}
	return &NewApp{Data: map[string][]byte{AppSecret.Host: []byte(host), AppSecret.ID: []byte(strconv.FormatInt(v.GetID(), 10)),
		AppSecret.Slug: []byte(v.GetSlug()), AppSecret.ClientID: []byte(v.GetClientID()),
		AppSecret.ClientSecret: []byte(v.GetClientSecret()), AppSecret.PrivateKey: []byte(v.GetPEM())},
		Owner: v.Owner.Login, Personal: v.Owner.Type == "User", Permissions: v.Permissions, Events: v.Events}, nil
}

// Differs says how the new app differs from what Banda asked for (Manifest,
// for publicURL, owned by org or, without one, a user account): "" if it
// doesn't. A manifest can be posted to the forge by anyone with a state, so
// what comes back is checked, not trusted. The forge doesn't say whether an
// app is public or its webhook active, so it asks: a private app's page is
// not found without signing in, and a webhook may only point back at Banda.
func (a *App) Differs(ctx context.Context, n *NewApp, publicURL, org string) (string, error) {
	var why []string
	if !maps.Equal(n.Permissions, AppPermissions) {
		why = append(why, "other permissions")
	}
	if len(n.Events) > 0 {
		why = append(why, "webhook events")
	}
	switch {
	case org != "" && (n.Personal || !strings.EqualFold(n.Owner, org)):
		why = append(why, fmt.Sprintf("owned by %s, not the organization %s", n.Owner, org))
	case org == "" && !n.Personal:
		why = append(why, fmt.Sprintf("owned by the organization %s, not a personal account", n.Owner))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.api()+"/apps/"+url.PathEscape(a.Slug), nil)
	if err != nil {
		return "", err
	}
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", unreachable("GitHub", err)
	}
	res.Body.Close()
	if res.StatusCode == http.StatusOK {
		why = append(why, "public")
	}
	gh, err := a.asApp()
	if err != nil {
		return "", err
	}
	hook, hres, err := gh.Apps.GetHookConfig(ctx)
	switch {
	case hres != nil && hres.StatusCode == http.StatusNotFound: // none
	case err != nil:
		return "", ghError(err, appFix)
	case hook.GetURL() != "" && hook.GetURL() != publicURL+"/github/webhook":
		why = append(why, "a webhook to "+hook.GetURL())
	}
	return strings.Join(why, ", "), nil
}

// SettingsURL is the app's settings on the forge, where its owner deletes it.
func (a *App) SettingsURL(owner string, personal bool) string {
	if personal {
		return a.base + "/settings/apps/" + a.Slug
	}
	return a.base + "/organizations/" + url.PathEscape(owner) + "/settings/apps/" + a.Slug
}

// api is the forge's REST API root.
func (a *App) api() string {
	if a.base == "https://github.com" {
		return "https://api.github.com"
	}
	return a.base + "/api/v3"
}

// JWT authenticates as the app: RS256, issued 60 s ago against clock drift,
// valid for 10 minutes from then, GitHub's maximum.
func (a *App) JWT(now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	claims, err := json.Marshal(map[string]any{"iat": now.Add(-time.Minute).Unix(), "exp": now.Add(9 * time.Minute).Unix(), "iss": a.ClientID})
	if err != nil {
		return "", err
	}
	msg := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`)) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, a.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return msg + "." + enc.EncodeToString(sig), nil
}

// asApp is a client authenticated as the app.
func (a *App) asApp() (*github.Client, error) {
	jwt, err := a.JWT(time.Now())
	if err != nil {
		return nil, err
	}
	return gitHubClient(a.base, jwt)
}

const appFix = "check the GitHub App in Settings"

// ErrNotInstalled: the app isn't installed on the repository's owner, or not for that repository.
var ErrNotInstalled = errors.New("the GitHub App isn't installed for this repository")

// Installation finds the installation that reaches owner/name.
// ponytail: cached for 5 minutes per repository; an uninstall shows when its tokens fail.
func (a *App) Installation(ctx context.Context, owner, name string) (int64, error) {
	key := owner + "/" + name
	a.mu.Lock()
	in, ok := a.installs[key]
	a.mu.Unlock()
	if ok && time.Since(in.at) < 5*time.Minute {
		return in.id, nil
	}
	gh, err := a.asApp()
	if err != nil {
		return 0, err
	}
	got, res, err := gh.Apps.GetRepositoryInstallation(ctx, owner, name)
	if res != nil && res.StatusCode == http.StatusNotFound {
		return 0, ErrNotInstalled
	}
	if err != nil {
		return 0, ghError(err, appFix)
	}
	a.mu.Lock()
	a.installs[key] = installation{got.GetID(), time.Now()}
	a.mu.Unlock()
	return got.GetID(), nil
}

// RepoToken mints an installation token for one repository: contents to
// write (and workflows) or only read, and metadata. Tokens last an hour; one
// is reused for the same thread until 10 minutes before it expires, so a
// token one thread's agent revokes or leaks is no other thread's.
func (a *App) RepoToken(ctx context.Context, thread string, installation int64, repo string, write bool) (RepoToken, error) {
	key := fmt.Sprintf("%s %d %s %t", thread, installation, repo, write)
	a.mu.Lock()
	t, ok := a.tokens[key]
	a.mu.Unlock()
	if ok && time.Until(t.Expires) > 10*time.Minute {
		return t, nil
	}
	gh, err := a.asApp()
	if err != nil {
		return RepoToken{}, err
	}
	perms := &github.InstallationPermissions{Contents: github.Ptr("read"), Metadata: github.Ptr("read")}
	if write {
		perms.Contents, perms.Workflows = github.Ptr("write"), github.Ptr("write")
	}
	got, _, err := gh.Apps.CreateInstallationToken(ctx, installation, &github.InstallationTokenOptions{Repositories: []string{repo}, Permissions: perms})
	if err != nil {
		return RepoToken{}, ghError(err, appFix)
	}
	t = RepoToken{got.GetToken(), got.GetExpiresAt().Time}
	a.mu.Lock()
	if len(a.tokens) > 1000 { // ponytail: emptied whole; a team has far fewer running threads
		clear(a.tokens)
	}
	a.tokens[key] = t
	a.mu.Unlock()
	return t, nil
}

// Owner is the account that owns the app now (it can be transferred), and
// whether it is a user's.
func (a *App) Owner(ctx context.Context) (login string, personal bool, err error) {
	gh, err := a.asApp()
	if err != nil {
		return "", false, err
	}
	app, _, err := gh.Apps.Get(ctx, "")
	if err != nil {
		return "", false, ghError(err, appFix)
	}
	return app.GetOwner().GetLogin(), app.GetOwner().GetType() == "User", nil
}

// Installations lists the accounts the app is installed on.
// ponytail: the first 100; counting a selection mints a token per installation.
func (a *App) Installations(ctx context.Context) ([]*bandav1.GitHubInstallation, error) {
	gh, err := a.asApp()
	if err != nil {
		return nil, err
	}
	list, _, err := gh.Apps.ListInstallations(ctx, &github.ListOptions{PerPage: 100})
	if err != nil {
		return nil, ghError(err, appFix)
	}
	var out []*bandav1.GitHubInstallation
	for _, in := range list {
		i := &bandav1.GitHubInstallation{Account: in.GetAccount().GetLogin(), AvatarUrl: in.GetAccount().GetAvatarURL(),
			AllRepositories: in.GetRepositorySelection() == "all", ManageUrl: in.GetHTMLURL()}
		if !i.AllRepositories {
			tok, _, err := gh.Apps.CreateInstallationToken(ctx, in.GetID(), &github.InstallationTokenOptions{
				Permissions: &github.InstallationPermissions{Metadata: github.Ptr("read")}})
			if err != nil {
				return nil, ghError(err, appFix)
			}
			c, err := gitHubClient(a.base, tok.GetToken())
			if err != nil {
				return nil, err
			}
			repos, _, err := c.Apps.ListRepos(ctx, &github.ListOptions{PerPage: 1})
			if err != nil {
				return nil, ghError(err, appFix)
			}
			i.Repositories = int32(repos.GetTotalCount())
		}
		out = append(out, i)
	}
	return out, nil
}

// UserToken is a user's token from the app's OAuth flow: GitHub's last 8
// hours, and their refresh token, used once, 6 months. A zero Expiry never
// expires (an app with expiring tokens turned off).
type UserToken struct {
	Access, Refresh string
	Expiry          time.Time
}

func (a *App) oauth(redirect string) *oauth2.Config {
	return &oauth2.Config{ClientID: a.ClientID, ClientSecret: a.ClientSecret, RedirectURL: redirect,
		Endpoint: oauth2.Endpoint{AuthURL: a.base + "/login/oauth/authorize", TokenURL: a.base + "/login/oauth/access_token",
			AuthStyle: oauth2.AuthStyleInParams}}
}

// AuthorizeURL sends a user to the forge to connect, with PKCE (S256).
func (a *App) AuthorizeURL(redirect, state, verifier string) string {
	return a.oauth(redirect).AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
}

// Exchange turns the code from the OAuth flow into the user's token.
func (a *App) Exchange(ctx context.Context, redirect, code, verifier string) (UserToken, error) {
	t, err := a.oauth(redirect).Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return UserToken{}, err
	}
	return UserToken{t.AccessToken, t.RefreshToken, t.Expiry}, nil
}

// ErrRevoked: the forge refused a refresh token for good (revoked, expired, used).
var ErrRevoked = errors.New("GitHub no longer accepts this connection")

// Refresh trades a refresh token, which the forge then refuses, for a new
// access and refresh token. Only the forge refusing the grant is ErrRevoked;
// anything else (the app's own credentials refused, rate limits, outages)
// leaves the connection as it is.
func (a *App) Refresh(ctx context.Context, refresh string) (UserToken, error) {
	t, err := a.oauth("").TokenSource(ctx, &oauth2.Token{RefreshToken: refresh}).Token()
	var re *oauth2.RetrieveError
	errors.As(err, &re)
	switch {
	case err == nil:
		return UserToken{t.AccessToken, t.RefreshToken, t.Expiry}, nil
	case re != nil && (re.ErrorCode == "bad_refresh_token" || re.ErrorCode == "invalid_grant"):
		return UserToken{}, ErrRevoked
	case re != nil && (re.ErrorCode == "incorrect_client_credentials" || re.ErrorCode == "invalid_client" || re.ErrorCode == "unauthorized_client"):
		if !a.misconfigured.Swap(true) {
			slog.Error("GitHub refused the GitHub App's client id or secret: check its Secret", "app", a.Slug, "error", re.ErrorCode)
		}
		return UserToken{}, &Error{Unavailable, "GitHub refused the team's GitHub App, so your connection can't be renewed: check the app in Settings"}
	}
	return UserToken{}, &Error{Unavailable, "GitHub couldn't renew your connection just now: try again in a minute"}
}

// Revoke deletes the user's authorization of the app (all its tokens) on the
// forge; one already gone is fine.
func (a *App) Revoke(ctx context.Context, access string) error {
	body, _ := json.Marshal(map[string]string{"access_token": access})
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, a.api()+"/applications/"+url.PathEscape(a.ClientID)+"/grant", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(a.ClientID, a.ClientSecret)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return unreachable("GitHub", err)
	}
	res.Body.Close()
	if res.StatusCode >= 300 && res.StatusCode != http.StatusNotFound && res.StatusCode != http.StatusUnprocessableEntity {
		return fromStatus("GitHub", appFix, res.StatusCode, res.Status)
	}
	return nil
}

// User is who a user token belongs to.
func (a *App) User(ctx context.Context, access string) (*github.User, error) {
	gh, err := gitHubClient(a.base, access)
	if err != nil {
		return nil, err
	}
	u, _, err := gh.Users.Get(ctx, "")
	return u, ghError(err, "connect GitHub again")
}

// Access is what a user token may do with a repository: "write", "read" or
// "" (none, or the app can't see it).
func (a *App) Access(ctx context.Context, access, owner, name string) (string, error) {
	gh, err := gitHubClient(a.base, access)
	if err != nil {
		return "", err
	}
	r, res, err := gh.Repositories.Get(ctx, owner, name)
	if res != nil && res.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if err != nil {
		return "", ghError(err, "reconnect GitHub in Settings")
	}
	switch p := r.GetPermissions(); {
	case p.GetPush():
		return "write", nil
	case p.GetPull():
		return "read", nil
	}
	return "", nil
}

// Repos lists the repositories a user reaches through the app: those of its
// installations the user can see, filtered by name.
// ponytail: the first 100 installations and 100 repositories of each.
func (a *App) Repos(ctx context.Context, access, query string) ([]*bandav1.ForgeRepo, error) {
	gh, err := gitHubClient(a.base, access, github.WithTransport(newETags(access, nil)))
	if err != nil {
		return nil, err
	}
	fix := "reconnect GitHub in Settings"
	installs, _, err := gh.Apps.ListUserInstallations(ctx, &github.ListOptions{PerPage: 100})
	if err != nil {
		return nil, ghError(err, fix)
	}
	var out []*bandav1.ForgeRepo
	for _, in := range installs {
		repos, _, err := gh.Apps.ListUserRepos(ctx, in.GetID(), &github.ListOptions{PerPage: 100})
		if err != nil {
			return nil, ghError(err, fix)
		}
		for _, r := range repos.Repositories {
			if strings.Contains(strings.ToLower(r.GetFullName()), strings.ToLower(query)) {
				out = append(out, &bandav1.ForgeRepo{Url: "https://" + a.Host + "/" + r.GetFullName() + ".git", Name: r.GetFullName(),
					Description: r.GetDescription(), Forge: GitHub})
			}
		}
	}
	return out, nil
}
