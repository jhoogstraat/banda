package forge

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	"golang.org/x/oauth2"
)

// A team's OAuth application on a GitLab host. GitLab has no manifest flow,
// so someone creates it by hand and pastes its ID and secret into Settings.
// Each user authorizes it twice: once with api (merge requests, the repo
// picker; the token stays in the API) and once with write_repository only,
// whose token is the one git in sandboxes gets. GitLab (Doorkeeper, without
// reuse_access_token) keeps the two grants and their refresh tokens apart,
// so neither can reach the other's powers. GitLab can't narrow a user's token
// to one project: the git token reaches every project its owner can push to,
// for at most two hours, over git only.

// GitLabApp is a GitLab OAuth application, read from its Secret (see GitLabAppSecret).
type GitLabApp struct {
	Host, ClientID, ClientSecret string
	base                         string // https://<host>, or the test's fake

	misconfigured atomic.Bool // GitLab refused its client credentials; logged once
}

// GitLabAppSecret holds what a GitLab application's Secret keys: made by
// Banda from Settings, or by hand (Helm, GitOps).
var GitLabAppSecret = struct{ Host, ID, Secret string }{"host", "application-id", "secret"}

// The scopes of a user's two grants: Banda's own (the API's), and git's (the sandbox's).
const (
	GitLabAPIScope = "api"
	GitLabGitScope = "write_repository"
)

// GitLabApp reads a GitLab application from its Secret's data.
func (c Config) GitLabApp(data map[string][]byte) (*GitLabApp, error) {
	get := func(k string) string { return strings.TrimSpace(string(data[k])) }
	a := &GitLabApp{Host: strings.ToLower(get(GitLabAppSecret.Host)), ClientID: get(GitLabAppSecret.ID), ClientSecret: get(GitLabAppSecret.Secret)}
	if a.Host == "" {
		a.Host = "gitlab.com"
	}
	if kind := c.Known()[a.Host]; kind != GitLab {
		return nil, fmt.Errorf("%s isn't a GitLab host: name it in FORGE_HOSTS as %s=gitlab", a.Host, a.Host)
	}
	if a.ClientID == "" || a.ClientSecret == "" {
		return nil, fmt.Errorf("it needs %s and %s", GitLabAppSecret.ID, GitLabAppSecret.Secret)
	}
	a.base = c.Base(a.Host)
	return a, nil
}

// GitLabAppsURL is where an application is created on host: the user's own
// ("user"), a group's (by its full path) or the instance's ("admin").
func (c Config) GitLabAppsURL(host, owner, group string) string {
	switch owner {
	case "group":
		return c.Base(host) + "/groups/" + strings.Trim(group, "/") + "/-/settings/applications"
	case "admin":
		return c.Base(host) + "/admin/applications"
	}
	return c.Base(host) + "/-/user_settings/applications"
}

func (a *GitLabApp) oauth(redirect, scope string) *oauth2.Config {
	return &oauth2.Config{ClientID: a.ClientID, ClientSecret: a.ClientSecret, RedirectURL: redirect, Scopes: []string{scope},
		Endpoint: oauth2.Endpoint{AuthURL: a.base + "/oauth/authorize", TokenURL: a.base + "/oauth/token", AuthStyle: oauth2.AuthStyleInParams}}
}

// AuthorizeURL sends a user to GitLab to grant scope, with PKCE (S256).
func (a *GitLabApp) AuthorizeURL(redirect, scope, state, verifier string) string {
	return a.oauth(redirect, scope).AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
}

// Exchange turns the code from the OAuth flow into the user's token, which
// must have exactly the scope asked for: a token for git must not do more.
func (a *GitLabApp) Exchange(ctx context.Context, redirect, scope, code, verifier string) (UserToken, error) {
	t, err := a.oauth(redirect, scope).Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return UserToken{}, err
	}
	if got, _ := t.Extra("scope").(string); got != scope {
		return UserToken{}, fmt.Errorf("GitLab granted %q, not %q", got, scope)
	}
	return UserToken{t.AccessToken, t.RefreshToken, t.Expiry}, nil
}

// Refresh trades a refresh token, which GitLab then refuses, for a new
// access and refresh token (GitLab's last two hours). Only GitLab refusing
// the grant (invalid_grant) is ErrRevoked: refused client credentials, rate
// limits and outages keep the stored pair, as App.Refresh does.
func (a *GitLabApp) Refresh(ctx context.Context, refresh string) (UserToken, error) {
	t, err := a.oauth("", "").TokenSource(ctx, &oauth2.Token{RefreshToken: refresh}).Token()
	var re *oauth2.RetrieveError
	errors.As(err, &re)
	switch {
	case err == nil:
		a.misconfigured.Store(false)
		return UserToken{t.AccessToken, t.RefreshToken, t.Expiry}, nil
	case re != nil && re.ErrorCode == "invalid_grant":
		return UserToken{}, ErrRevoked
	case re != nil && (re.ErrorCode == "invalid_client" || re.ErrorCode == "unauthorized_client"):
		if !a.misconfigured.Swap(true) {
			slog.Error("GitLab refused the GitLab application's ID or secret: check its Secret", "host", a.Host, "error", re.ErrorCode)
		}
		return UserToken{}, &Error{Unavailable, "the team's GitLab application for " + a.Host + " needs attention, so your connection can't be renewed: see GitLab in Settings"}
	}
	return UserToken{}, &Error{Unavailable, "GitLab couldn't renew your connection just now: try again in a minute"}
}

// Misconfigured: GitLab refused the application's ID or secret on the last refresh.
func (a *GitLabApp) Misconfigured() bool { return a.misconfigured.Load() }

// Owner is the GitLab user id a token belongs to (Doorkeeper's
// /oauth/token/info, which takes a token of any scope).
func (a *GitLabApp) Owner(ctx context.Context, access string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.base+"/oauth/token/info", nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, unreachable("GitLab", err)
	}
	defer res.Body.Close()
	var info struct {
		ResourceOwnerID int64 `json:"resource_owner_id"`
	}
	if res.StatusCode != http.StatusOK {
		return 0, fromStatus("GitLab", "connect GitLab again", res.StatusCode, res.Status)
	}
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil || info.ResourceOwnerID == 0 {
		return 0, fmt.Errorf("GitLab's token info has no resource owner: %v", err)
	}
	return info.ResourceOwnerID, nil
}

// Revoke revokes a token (and the refresh token of its grant) on GitLab; one
// already gone is fine.
func (a *GitLabApp) Revoke(ctx context.Context, token string) error {
	status, err := a.revoke(ctx, token)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fromStatus("GitLab", "check the GitLab application in Settings", status, http.StatusText(status))
	}
	return nil
}

// Verify checks the application's ID and secret without side effects: GitLab
// answers a revocation of a token that doesn't exist with 200 for the right
// credentials and refuses wrong ones (Doorkeeper's validate_presence_of_client).
func (a *GitLabApp) Verify(ctx context.Context) error {
	status, err := a.revoke(ctx, "banda-verify-"+rand.Text())
	switch {
	case err != nil:
		return err
	case status == http.StatusOK:
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &Error{Refused, "GitLab doesn't know that Application ID and Secret on " + a.Host + ": copy both again from the application's page."}
	}
	return &Error{Unavailable, fmt.Sprintf("GitLab answered %d", status)}
}

func (a *GitLabApp) revoke(ctx context.Context, token string) (int, error) {
	form := url.Values{"token": {token}, "client_id": {a.ClientID}, "client_secret": {a.ClientSecret}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base+"/oauth/revoke", strings.NewReader(form.Encode()))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, unreachable("GitLab", err)
	}
	res.Body.Close()
	return res.StatusCode, nil
}

// User is who an api token belongs to.
func (a *GitLabApp) User(ctx context.Context, access string) (*gitlab.User, error) {
	c, err := gitLabClient(a.base, access, nil)
	if err != nil {
		return nil, err
	}
	u, _, err := c.Users.CurrentUser(gitlab.WithContext(ctx))
	if err != nil {
		return nil, (&gitLab{fix: "connect GitLab again"}).fail(err)
	}
	return u, nil
}
