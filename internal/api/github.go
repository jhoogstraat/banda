package api

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/forge"
	"github.com/jhoogstraat/banda/internal/sandbox"
	"github.com/jhoogstraat/banda/internal/store"
)

// The team's GitHub App (internal/forge/app.go) and users' connections to it.
// Anyone signed in sets it up from Settings, as with team secrets. Its keys
// are in the Secret banda-github-app, a user's tokens in banda-forge-<hash>:
// no pod mounts either, Postgres keeps only who and when, and nothing here
// logs or returns a token or key. Sandboxes get installation tokens for their
// own repository through GitCredential.

// GitHubAppSecret names the app's Secret. Tests running APIs side by side in
// one namespace give each its own (TEST_GITHUB_APP_SECRET).
var GitHubAppSecret = "banda-github-app"

type gitHub struct {
	mu     sync.Mutex
	app    *forge.App
	appErr error
	appAt  time.Time
	inst   []*bandav1.GitHubInstallation
	instAt time.Time
	access map[string]cachedAccess // user, their forge id, owner/name
	key    signer                  // OAuth and manifest states
	tokens *tokenKeeper
	// What was wrong with the app each user last created and Banda refused.
	// ponytail: in memory, said for an hour; lost on a restart, when it is on GitHub still.
	refusals map[string]refusal
}

type refusal struct {
	r  *bandav1.GitHubAppRefusal
	at time.Time
}

type cachedAccess struct {
	access string // "write", "read" or ""
	at     time.Time
}

func newGitHub(sb *sandbox.Client) *gitHub {
	g := &gitHub{access: map[string]cachedAccess{}, key: signer(rand.Text()), refusals: map[string]refusal{}}
	g.tokens = &tokenKeeper{conns: map[string]*userConn{},
		load: func(ctx context.Context, user, host string) (map[string][]byte, error) {
			return sb.Secret(ctx, sandbox.ForgeSecret(user, host))
		},
		save: func(ctx context.Context, user, host string, data map[string][]byte) error {
			return sb.PutSecret(ctx, sandbox.ForgeSecret(user, host), "forge-connection", data)
		}}
	return g
}

// gitHubApp is the team's GitHub App, nil if there is none, read from its
// Secret at most every 30 s (one made by hand applies within that). An
// unusable Secret is an error, which Settings shows. Another app in its
// place ends the connections to the one before (dropConnections).
func (s *Service) gitHubApp(ctx context.Context) (*forge.App, error) {
	g := s.gh
	g.mu.Lock()
	if !g.appAt.IsZero() && time.Since(g.appAt) < 30*time.Second {
		defer g.mu.Unlock()
		return g.app, g.appErr
	}
	data, err := s.sandboxes.Secret(ctx, GitHubAppSecret)
	if err != nil {
		g.mu.Unlock()
		return nil, err // e.g. no RBAC: not cached, tried again
	}
	var app *forge.App
	if data != nil {
		app, err = Forges.App(data)
		if err != nil {
			err = fmt.Errorf("the Secret %s is unusable: %w", GitHubAppSecret, err)
		}
	}
	// The same app keeps its caches (installations, tokens).
	var replaced *forge.App
	if app == nil || g.app == nil || app.ID != g.app.ID || app.Host != g.app.Host || app.ClientSecret != g.app.ClientSecret {
		if app != nil && g.app != nil && (app.ID != g.app.ID || app.Host != g.app.Host) {
			replaced = g.app
		}
		g.app, g.inst, g.instAt = app, nil, time.Time{}
		clear(g.access)
	}
	g.appErr, g.appAt = err, time.Now()
	g.mu.Unlock()
	// ponytail: noticed by this API while running; connections to an app replaced by hand while it was down end when GitHub refuses them.
	if replaced != nil {
		slog.Info("github app replaced", "was", replaced.Slug, "now", app.Slug)
		if err := s.dropConnections(context.WithoutCancel(ctx), replaced); err != nil {
			slog.Error("end connections to the replaced github app", "err", err)
		}
	}
	return app, err
}

// dropConnections ends every connection to the app a: each grant is revoked
// on the forge with its credentials (best effort: it may be gone), then the
// tokens and connections are forgotten. Its users connect to the next app.
func (s *Service) dropConnections(ctx context.Context, a *forge.App) error {
	users, err := s.store.ForgeConnectionUsers(ctx, a.Host)
	if err != nil {
		return err
	}
	for _, u := range users {
		if tok, _ := s.gh.tokens.token(ctx, a, u); tok != "" {
			if err := a.Revoke(ctx, tok); err != nil {
				slog.Warn("revoke a connection to the old github app", "user", u, "err", err)
			}
		}
		if err := s.forgetConnection(ctx, u, a.Host); err != nil {
			return err
		}
	}
	return nil
}

// appChanged forgets the app, so the next use reads its Secret.
func (s *Service) appChanged() {
	s.gh.mu.Lock()
	s.gh.appAt = time.Time{}
	s.gh.mu.Unlock()
}

// appFor is the team's app if it serves host.
func (s *Service) appFor(ctx context.Context, host string) *forge.App {
	a, err := s.gitHubApp(ctx)
	if err != nil {
		slog.Error("github app", "err", err)
	}
	if a == nil || a.Host != host {
		return nil
	}
	return a
}

// userToken is the user's access token for the app, refreshed when it is
// about to expire; empty if they haven't connected. A connection the forge
// refuses is marked broken (Settings shows "Reconnect") and forgotten.
func (s *Service) userToken(ctx context.Context, userID string, a *forge.App) (string, error) {
	tok, err := s.gh.tokens.token(ctx, a, userID)
	if errors.Is(err, forge.ErrRevoked) {
		s.connectionRefused(ctx, userID, a.Host)
	}
	return tok, err
}

// connectionRefused marks the user's connection broken and drops its tokens.
func (s *Service) connectionRefused(ctx context.Context, userID, host string) {
	ctx = context.WithoutCancel(ctx)
	s.gh.tokens.forget(userID, host)
	if err := s.sandboxes.DeleteSecret(ctx, sandbox.ForgeSecret(userID, host)); err != nil {
		slog.Error("delete refused forge tokens", "user", userID, "err", err)
	}
	if err := s.store.BreakForgeConnection(ctx, userID, host); err != nil {
		slog.Error("break forge connection", "user", userID, "err", err)
	}
}

// TokenRefused hears of a token a forge answered 401 to (forge.Config.Refused):
// if it is a user's connection's token, they revoked it.
func (s *Service) TokenRefused(token string) {
	if user, host := s.gh.tokens.owner(token); user != "" {
		slog.Info("github refused a user's token", "user", user)
		s.connectionRefused(context.Background(), user, host)
	}
	s.gitLabTokenRefused(token)
}

// tokenKeeper keeps users' app tokens fresh. GitHub's refresh tokens work
// once, so refreshes are serialized per user and the new pair is saved
// before the new access token is used: losing it would disconnect them.
// ponytail: per-user locks in one API; several replicas would need a lock in Kubernetes or Postgres.
type tokenKeeper struct {
	load func(ctx context.Context, user, host string) (map[string][]byte, error)
	save func(ctx context.Context, user, host string, data map[string][]byte) error

	mu    sync.Mutex
	conns map[string]*userConn // user, host
}

type userConn struct {
	mu     sync.Mutex
	tok    forge.UserToken
	loaded bool
	// unsaved: refreshed but not yet saved; saved before anything else.
	unsaved bool
	// dead: forgotten (disconnected, revoked, the app replaced); a caller
	// that got it before must not save its tokens again.
	dead bool
}

// refreshBefore is how long before expiry a token is refreshed.
const refreshBefore = 5 * time.Minute

func (k *tokenKeeper) conn(user, host string) *userConn {
	k.mu.Lock()
	defer k.mu.Unlock()
	c := k.conns[user+" "+host]
	if c == nil {
		c = &userConn{}
		k.conns[user+" "+host] = c
	}
	return c
}

func (k *tokenKeeper) token(ctx context.Context, a *forge.App, user string) (string, error) {
	t, err := k.fresh(ctx, a, a.Host, user)
	return t.Access, err
}

// refresher trades a refresh token for a new pair: a GitHub App, a GitLab application.
type refresher interface {
	Refresh(ctx context.Context, refresh string) (forge.UserToken, error)
}

// fresh is the user's token on host, refreshed by a when it is about to expire.
func (k *tokenKeeper) fresh(ctx context.Context, a refresher, host, user string) (forge.UserToken, error) {
	c := k.conn(user, host)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead {
		return forge.UserToken{}, nil
	}
	if !c.loaded {
		data, err := k.load(ctx, user, host)
		if err != nil {
			return forge.UserToken{}, err
		}
		c.tok, c.loaded = decodeUserToken(data), true
	}
	if c.unsaved {
		if err := k.save(ctx, user, host, encodeUserToken(c.tok)); err != nil {
			return forge.UserToken{}, err
		}
		c.unsaved = false
	}
	if c.tok.Access == "" || c.tok.Expiry.IsZero() || time.Until(c.tok.Expiry) > refreshBefore {
		return c.tok, nil
	}
	fresh, err := a.Refresh(ctx, c.tok.Refresh)
	if errors.Is(err, forge.ErrRevoked) {
		c.tok = forge.UserToken{} // its access token is no use either
	}
	if err != nil {
		return forge.UserToken{}, err
	}
	c.tok, c.unsaved = fresh, true // kept even if saving fails: the old refresh token is spent
	if err := k.save(ctx, user, host, encodeUserToken(fresh)); err != nil {
		return forge.UserToken{}, err
	}
	c.unsaved = false
	return fresh, nil
}

// put keeps a new connection's token, saved first.
func (k *tokenKeeper) put(ctx context.Context, user, host string, t forge.UserToken) error {
	c := k.conn(user, host)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dead {
		return errors.New("the connection ended meanwhile")
	}
	if err := k.save(ctx, user, host, encodeUserToken(t)); err != nil {
		return err
	}
	c.tok, c.loaded, c.unsaved = t, true, false
	return nil
}

// forget drops the user's tokens. A refresh in flight finishes first (and
// saves), so a caller deleting the Secret after this deletes it for good.
func (k *tokenKeeper) forget(user, host string) {
	k.mu.Lock()
	c := k.conns[user+" "+host]
	delete(k.conns, user+" "+host)
	k.mu.Unlock()
	if c != nil {
		c.mu.Lock()
		c.dead = true
		c.mu.Unlock()
	}
}

// expire makes the next fresh refresh the access token token, if it is still
// the user's: the forge refused it, whatever its expiry says here.
func (k *tokenKeeper) expire(user, host, token string) {
	c := k.conn(user, host)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tok.Access == token {
		c.tok.Expiry = time.Now()
	}
}

// owner is whose cached access token token is.
func (k *tokenKeeper) owner(token string) (user, host string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for key, c := range k.conns {
		if c.mu.TryLock() { // one being refreshed holds a new token anyway
			mine := token != "" && subtle.ConstantTimeCompare([]byte(c.tok.Access), []byte(token)) == 1
			c.mu.Unlock()
			if mine {
				user, host, _ = strings.Cut(key, " ")
				return user, host
			}
		}
	}
	return "", ""
}

func encodeUserToken(t forge.UserToken) map[string][]byte {
	exp := ""
	if !t.Expiry.IsZero() {
		exp = t.Expiry.UTC().Format(time.RFC3339)
	}
	return map[string][]byte{"access-token": []byte(t.Access), "refresh-token": []byte(t.Refresh), "expires-at": []byte(exp)}
}

func decodeUserToken(data map[string][]byte) forge.UserToken {
	exp, _ := time.Parse(time.RFC3339, string(data["expires-at"]))
	return forge.UserToken{Access: string(data["access-token"]), Refresh: string(data["refresh-token"]), Expiry: exp}
}

// repoAccess is what the user's token, of the forge account forgeID, may do
// with owner/name, cached for 5 minutes: a reconnect to another account asks afresh.
func (s *Service) repoAccess(ctx context.Context, a *forge.App, userID string, forgeID int64, token, owner, name string) (string, error) {
	key := fmt.Sprintf("%s %d %s/%s", userID, forgeID, owner, name)
	s.gh.mu.Lock()
	c, ok := s.gh.access[key]
	s.gh.mu.Unlock()
	if ok && time.Since(c.at) < 5*time.Minute {
		return c.access, nil
	}
	access, err := a.Access(ctx, token, owner, name)
	if err != nil {
		return "", err
	}
	s.gh.mu.Lock()
	if len(s.gh.access) > 5000 { // ponytail: emptied whole, like the ETag cache
		clear(s.gh.access)
	}
	s.gh.access[key] = cachedAccess{access, time.Now()}
	s.gh.mu.Unlock()
	return access, nil
}

func refuse(msg string) error { return connect.NewError(connect.CodeFailedPrecondition, msg) }

// noGitToken is the reason without the app: what the runner said before it.
const noGitToken = "an https remote needs a GIT_TOKEN secret"

// GitCredential gives the thread's runner an installation token for the
// thread's repository as Postgres has it, never one the pod names: write
// access if the owner may push there, else read. Refusals say what to do, as
// a clause the runner ends its push detail with. Only a connected runner gets
// one, as with IdTokens: a runner token taken from a pod is no use once the
// pod is gone (the runner connects before it clones and pushes only while
// connected). Every credential handed out is redacted from what the runner
// sends while it is valid (redact.go).
func (s *Service) GitCredential(ctx context.Context, req *bandav1.GitCredentialRequest) (*bandav1.GitCredentialResponse, error) {
	res, err := s.gitCredential(ctx, req)
	if err == nil {
		var expires time.Time
		if res.ExpiresAt != nil {
			expires = res.ExpiresAt.AsTime()
		}
		s.handedOut.remember(req.ThreadId, res.Password, expires)
	}
	return res, err
}

func (s *Service) gitCredential(ctx context.Context, req *bandav1.GitCredentialRequest) (*bandav1.GitCredentialResponse, error) {
	t, err := s.runnerThread(ctx, req.ThreadId, req.Token)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	_, up := s.runners[t.ID]
	s.mu.Unlock()
	if !up {
		return nil, errNotRunning
	}
	repo, ok := Forges.Repo(t.RepoURL)
	if ok && repo.Kind == forge.GitLab {
		return s.gitLabCredential(ctx, t, repo)
	}
	var a *forge.App
	if ok {
		a = s.appFor(ctx, repo.Host)
	}
	if a == nil {
		return nil, refuse(noGitToken)
	}
	token, err := s.userToken(ctx, t.OwnerID, a)
	if errors.Is(err, forge.ErrRevoked) {
		return nil, refuse("reconnect GitHub in Settings: GitHub no longer accepts your connection")
	}
	if err != nil {
		return nil, forgeError(err)
	}
	if token == "" {
		return nil, refuse("connect GitHub in Settings, so Banda can push as you")
	}
	owner, name, _ := strings.Cut(repo.Path, "/")
	id, err := a.Installation(ctx, owner, name)
	if errors.Is(err, forge.ErrNotInstalled) {
		return nil, refuse(fmt.Sprintf("the team's GitHub App isn't installed for %s: install it at %s", repo.Path, a.InstallURL()))
	}
	if err != nil {
		return nil, forgeError(err)
	}
	conn, err := s.store.ForgeConnection(ctx, t.OwnerID, a.Host)
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, refuse("connect GitHub in Settings, so Banda can push as you")
	}
	access, err := s.repoAccess(ctx, a, t.OwnerID, conn.ForgeID, token, owner, name)
	var fe *forge.Error
	if errors.As(err, &fe) && fe.Code == forge.Unauthorized {
		s.connectionRefused(ctx, t.OwnerID, a.Host)
		return nil, refuse("reconnect GitHub in Settings: GitHub no longer accepts your connection")
	}
	if err != nil {
		return nil, forgeError(err)
	}
	if access == "" {
		return nil, refuse(fmt.Sprintf("your GitHub account can't read %s: ask for access to it on GitHub", repo.Path))
	}
	rt, err := a.RepoToken(ctx, t.ID, id, name, access == "write")
	if err != nil {
		return nil, forgeError(err)
	}
	return &bandav1.GitCredentialResponse{Username: "x-access-token", Password: rt.Token, ExpiresAt: timestamppb.New(rt.Expires)}, nil
}

// githubHosts are the hosts a GitHub App can be created on, github.com first.
func githubHosts() []string {
	var hosts []string
	for host, kind := range Forges.Known() {
		if kind == forge.GitHub && host != "github.com" {
			hosts = append(hosts, host)
		}
	}
	slices.Sort(hosts)
	return append([]string{"github.com"}, hosts...)
}

func (s *Service) GetGitHub(ctx context.Context, _ *bandav1.GetGitHubRequest) (*bandav1.GetGitHubResponse, error) {
	res := &bandav1.GetGitHubResponse{Hosts: githubHosts()}
	s.gh.mu.Lock()
	if r, ok := s.gh.refusals[user(ctx)]; ok && time.Since(r.at) < time.Hour {
		res.Refusal = r.r
	}
	s.gh.mu.Unlock()
	if c, err := s.store.LastGitHubAppChange(ctx); err != nil {
		return nil, err
	} else if c != nil {
		res.LastChange = &bandav1.GitHubAppChange{Action: c.Action, By: c.By, At: timestamppb.New(c.At), Host: c.Host, Slug: c.Slug, Owner: c.Owner,
			WasSlug: c.WasSlug, WasOwner: c.WasOwner}
	}
	legacy, err := s.legacyToken(ctx, user(ctx), forge.GitHub)
	if err != nil {
		return nil, err
	}
	res.LegacyToken = legacy.Value != ""
	a, appErr := s.gitHubApp(ctx)
	if a == nil && appErr == nil {
		return res, nil
	}
	if a == nil {
		res.App = &bandav1.GitHubApp{Error: appErr.Error()}
		return res, nil
	}
	res.App = &bandav1.GitHubApp{Host: a.Host, Slug: a.Slug, Url: a.URL(), InstallUrl: a.InstallURL()}
	if row, err := s.bandaMade(ctx, a); err != nil {
		return nil, err
	} else if row != nil {
		res.App.CreatedBy, res.App.CreatedAt, res.App.Owner, res.App.Personal = row.CreatedBy, timestamppb.New(row.CreatedAt), row.Owner, row.Personal
	}
	c, err := s.store.ForgeConnection(ctx, user(ctx), a.Host)
	if err != nil || c == nil {
		return res, err
	}
	res.Connection = &bandav1.ForgeConnection{Host: c.Host, Forge: c.Forge, Login: c.Login, AvatarUrl: c.AvatarURL,
		ConnectedAt: timestamppb.New(c.ConnectedAt), Broken: c.Broken}
	return res, nil
}

// bandaMade is the app's row if Banda made a from Settings, nil if it was
// made by hand (Helm, GitOps). Rows from before app ids were kept count as
// Banda's.
func (s *Service) bandaMade(ctx context.Context, a *forge.App) (*store.GitHubApp, error) {
	row, err := s.store.GitHubApp(ctx, a.Host)
	if err != nil || row == nil || row.AppID != 0 && row.AppID != a.ID {
		return nil, err
	}
	return row, nil
}

// replaceable is the id of the app a new one would replace (0: none), or
// why it can't be: one made by hand is changed where it was made, and
// replacing needs asking for (replace).
func (s *Service) replaceable(ctx context.Context, replace bool) (int64, error) {
	a, err := s.gitHubApp(ctx)
	if a == nil && err != nil {
		return 0, refuse("Banda can't use the GitHub App it has (" + err.Error() + "): remove it before creating another")
	}
	if a == nil {
		return 0, nil
	}
	row, err := s.bandaMade(ctx, a)
	if err != nil {
		return 0, err
	}
	if row == nil {
		return 0, refuse(fmt.Sprintf("the GitHub App %s comes from the deployment (the Secret %s, by Helm or GitOps): change it there", a.Slug, GitHubAppSecret))
	}
	if !replace {
		return 0, refuse(fmt.Sprintf("the team already has the GitHub App %s: replacing it ends everyone's connection, so it has to be asked for", a.Slug))
	}
	return a.ID, nil
}

var orgName = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

// GitHubAppManifest starts creating the team's app: the browser posts the
// manifest to the forge, whose answer returns to /github/app with a state
// bound to the caller, naming the owner asked for and the app it replaces.
func (s *Service) GitHubAppManifest(ctx context.Context, req *bandav1.GitHubAppManifestRequest) (*bandav1.GitHubAppManifestResponse, error) {
	host := strings.ToLower(cmp.Or(strings.TrimSpace(req.Host), "github.com"))
	if !slices.Contains(githubHosts(), host) {
		return nil, connect.NewError(connect.CodeInvalidArgument, host+" isn't a GitHub host Banda knows: name it in FORGE_HOSTS as "+host+"=github")
	}
	org := strings.TrimSpace(req.Organization)
	if org != "" && !orgName.MatchString(org) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "that isn't a GitHub organization name")
	}
	replaces, err := s.replaceable(ctx, req.Replace)
	if err != nil {
		return nil, err
	}
	s.gh.mu.Lock()
	delete(s.gh.refusals, user(ctx))
	s.gh.mu.Unlock()
	state := s.gh.key.sign(strings.Join([]string{"app", user(ctx), host, rand.Text(), org, strconv.FormatInt(replaces, 10)}, "|"), time.Now().Add(30*time.Minute))
	name := "Banda"
	if u, err := url.Parse(s.PublicURL); err == nil && u.Host != "" {
		name = "Banda (" + u.Host + ")"
	}
	if len(name) > 34 { // GitHub's limit; editable there anyway
		name = name[:33] + ")"
	}
	return &bandav1.GitHubAppManifestResponse{Url: Forges.NewAppURL(host, org, state), Manifest: forge.Manifest(name, s.PublicURL)}, nil
}

func (s *Service) ListGitHubInstallations(ctx context.Context, req *bandav1.ListGitHubInstallationsRequest) (*bandav1.ListGitHubInstallationsResponse, error) {
	a, err := s.gitHubApp(ctx)
	if a == nil {
		return &bandav1.ListGitHubInstallationsResponse{}, err
	}
	s.gh.mu.Lock()
	// None isn't kept: that is when someone is about to install it.
	inst, fresh := s.gh.inst, len(s.gh.inst) > 0 && time.Since(s.gh.instAt) < time.Minute && !req.Fresh
	s.gh.mu.Unlock()
	if !fresh {
		if inst, err = a.Installations(ctx); err != nil {
			return nil, forgeError(err)
		}
		// Asked here too, where Settings asks GitHub anyway: a transfer to an organization shows.
		if owner, personal, err := a.Owner(ctx); err != nil {
			slog.Warn("github app owner", "err", err)
		} else if err := s.store.GitHubAppOwner(ctx, a.ID, owner, personal); err != nil {
			return nil, err
		}
		s.gh.mu.Lock()
		s.gh.inst, s.gh.instAt = inst, time.Now()
		s.gh.mu.Unlock()
	}
	return &bandav1.ListGitHubInstallationsResponse{Installations: inst}, nil
}

// RemoveGitHubApp forgets the app and everyone's connections to it: their
// tokens are the app's. Postgres records who did it.
func (s *Service) RemoveGitHubApp(ctx context.Context, _ *bandav1.RemoveGitHubAppRequest) (*bandav1.RemoveGitHubAppResponse, error) {
	a, _ := s.gitHubApp(ctx)
	change := store.GitHubAppChange{Action: "removed", By: user(ctx)}
	if a != nil {
		change.Host, change.WasSlug = a.Host, a.Slug
		if row, err := s.bandaMade(ctx, a); err != nil {
			return nil, err
		} else if row != nil {
			change.WasOwner = row.Owner
		}
		if err := s.dropConnections(ctx, a); err != nil { // while its credentials still work
			return nil, err
		}
	}
	if err := s.sandboxes.DeleteSecret(ctx, GitHubAppSecret); err != nil {
		return nil, err
	}
	defer s.appChanged()
	if err := s.store.DeleteGitHubApps(ctx, change); err != nil {
		return nil, err
	}
	slog.Info("github app removed", "by", user(ctx), "slug", change.WasSlug)
	return &bandav1.RemoveGitHubAppResponse{}, nil
}

func (s *Service) forgetConnection(ctx context.Context, userID, host string) error {
	s.gh.tokens.forget(userID, host)
	if err := s.sandboxes.DeleteSecret(ctx, sandbox.ForgeSecret(userID, host)); err != nil {
		return err
	}
	return s.store.DeleteForgeConnection(ctx, userID, host)
}

// DisconnectGitHub revokes the caller's authorization of the app on the
// forge, then forgets it. A connection already refused needs no revoking.
func (s *Service) DisconnectGitHub(ctx context.Context, _ *bandav1.DisconnectGitHubRequest) (*bandav1.DisconnectGitHubResponse, error) {
	a, err := s.gitHubApp(ctx)
	if a == nil {
		return &bandav1.DisconnectGitHubResponse{}, err
	}
	token, err := s.userToken(ctx, user(ctx), a)
	if err != nil && !errors.Is(err, forge.ErrRevoked) {
		return nil, forgeError(err)
	}
	if token != "" {
		if err := a.Revoke(ctx, token); err != nil {
			return nil, forgeError(err)
		}
	}
	return &bandav1.DisconnectGitHubResponse{}, s.forgetConnection(ctx, user(ctx), a.Host)
}

// GitHubHandler serves the browser's side of the app's flows, returning to
// Settings with ?github=<outcome> (connect) or ?github-app=<outcome>:
//   - /github/connect starts connecting the user: to the forge with a state
//     bound to them, PKCE from it; only from Banda's own pages (crossSite);
//   - /github/callback finishes it, back on the thread it started from
//     (?back=, see backTo) once connected;
//   - /github/app is where the forge returns after creating the app;
//   - /github/installed after installing it.
func (s *Service) GitHubHandler(httpUser func(*http.Request) string) http.Handler {
	mux := http.NewServeMux()
	back := func(w http.ResponseWriter, r *http.Request, param, outcome, section string) {
		http.Redirect(w, r, "/settings?"+url.Values{param: {outcome}}.Encode()+"#"+section, http.StatusFound)
	}
	mux.HandleFunc("GET /github/connect", func(w http.ResponseWriter, r *http.Request) {
		u := httpUser(r)
		if u == "" {
			http.Redirect(w, r, "/signin?next=/settings%23github", http.StatusFound)
			return
		}
		if crossSite(r) { // only from Banda's own pages: another site can't make a teammate reconnect
			back(w, r, "github", "cross-site", "github")
			return
		}
		a, _ := s.gitHubApp(r.Context())
		if a == nil {
			back(w, r, "github", "no-app", "github")
			return
		}
		state := s.gh.key.sign(strings.Join([]string{"connect", u, a.Host, rand.Text(), backTo(r)}, "|"), time.Now().Add(10*time.Minute))
		http.Redirect(w, r, a.AuthorizeURL(s.PublicURL+"/github/callback", state, s.gh.verifier(state)), http.StatusFound)
	})
	mux.HandleFunc("GET /github/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		_, host, extra, ok := s.gh.checkState(q.Get("state"), "connect", httpUser(r))
		if !ok || len(extra) != 1 {
			back(w, r, "github", "state", "github")
			return
		}
		if q.Get("error") != "" { // access_denied: they cancelled
			back(w, r, "github", "denied", "github")
			return
		}
		if err := s.connect(r.Context(), httpUser(r), host, q.Get("code"), q.Get("state")); err != nil {
			slog.Error("connect github", "user", httpUser(r), "err", err)
			back(w, r, "github", "failed", "github")
			return
		}
		if extra[0] != "" {
			http.Redirect(w, r, extra[0], http.StatusFound)
			return
		}
		back(w, r, "github", "connected", "github")
	})
	// GitHub adds ?installation_id&setup_action to the app's setup URL: Settings
	// only needs to know to ask afresh where it is installed.
	mux.HandleFunc("GET /github/installed", func(w http.ResponseWriter, r *http.Request) {
		back(w, r, "github-app", "installed", "github-app")
	})
	mux.HandleFunc("GET /github/app", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		u, host, extra, ok := s.gh.checkState(q.Get("state"), "app", httpUser(r))
		if !ok || len(extra) != 2 {
			back(w, r, "github-app", "state", "github-app")
			return
		}
		org := extra[0]
		replaces, _ := strconv.ParseInt(extra[1], 10, 64) // signed by this API
		outcome, err := s.createApp(r.Context(), u, host, org, replaces, q.Get("code"))
		if err != nil {
			slog.Error("create github app", "user", u, "err", err)
		}
		back(w, r, "github-app", outcome, "github-app")
	})
	return mux
}

// threadPage is a thread's page in the web app.
var threadPage = regexp.MustCompile(`^/t/[A-Za-z0-9-]{1,64}$`)

// backTo is where a connect flow returns once connected: the thread page it
// started from (?back=), or "" for Settings. Only a thread's page, so the
// flow redirects nowhere else; failures go to Settings, which explains them.
func backTo(r *http.Request) string {
	if b := r.URL.Query().Get("back"); threadPage.MatchString(b) {
		return b
	}
	return ""
}

// verifier is the PKCE verifier of a state: derived, so it needs no cookie,
// and never leaves the API (only its challenge does).
func (g *gitHub) verifier(state string) string {
	return base64.RawURLEncoding.EncodeToString(g.key.mac("pkce " + state))
}

// checkState verifies a flow's state: signed by this API, unexpired, of that
// kind, and started by the user now in the browser. kind|user|host|nonce,
// then what the flow adds (extra).
func (g *gitHub) checkState(state, kind, httpUser string) (user, host string, extra []string, ok bool) {
	v, ok := g.key.verify(state, time.Now())
	parts := strings.Split(v, "|")
	if !ok || len(parts) < 4 || parts[0] != kind || httpUser == "" || subtle.ConstantTimeCompare([]byte(parts[1]), []byte(httpUser)) != 1 {
		return "", "", nil, false
	}
	return parts[1], parts[2], parts[4:], true
}

// connect finishes connecting the user: their token is saved before Postgres
// says they are connected.
func (s *Service) connect(ctx context.Context, userID, host, code, state string) error {
	a := s.appFor(ctx, host)
	if a == nil {
		return errors.New("the app changed during the flow")
	}
	tok, err := a.Exchange(ctx, s.PublicURL+"/github/callback", code, s.gh.verifier(state))
	if err != nil {
		return err
	}
	u, err := a.User(ctx, tok.Access)
	if err != nil {
		return err
	}
	if err := s.gh.tokens.put(ctx, userID, host, tok); err != nil {
		return err
	}
	s.gh.mu.Lock()
	for key := range s.gh.access { // another account may reach other repositories
		if strings.HasPrefix(key, userID+" ") {
			delete(s.gh.access, key)
		}
	}
	s.gh.mu.Unlock()
	return s.store.PutForgeConnection(ctx, store.ForgeConnection{UserID: userID, Host: host, Forge: forge.GitHub, Login: u.GetLogin(),
		ForgeID: u.GetID(), AvatarURL: u.GetAvatarURL()})
}

// createApp turns the forge's code into the app's Secret, if the app is what
// Banda asked for and the app it replaces (0: none) is still the team's. It
// answers the outcome Settings shows: created, refused (the app differs:
// Settings says how and where to delete it), changed (the team's app changed
// meanwhile) or failed.
func (s *Service) createApp(ctx context.Context, userID, host, org string, replaces int64, code string) (string, error) {
	cur, err := s.gitHubApp(ctx)
	if cur == nil && err != nil {
		return "failed", err
	}
	if id, err := s.replaceable(ctx, replaces != 0); err != nil || id != replaces {
		return "changed", cmp.Or(err, errors.New("the team's app changed during the flow"))
	}
	n, err := Forges.ConvertManifest(ctx, host, code)
	if err != nil {
		return "failed", err
	}
	a, err := Forges.App(n.Data)
	if err != nil {
		return "failed", err
	}
	why, err := a.Differs(ctx, n, s.PublicURL, org)
	if err != nil {
		return "failed", err
	}
	if why != "" {
		s.gh.mu.Lock()
		s.gh.refusals[userID] = refusal{&bandav1.GitHubAppRefusal{Reason: why, DeleteUrl: a.SettingsURL(n.Owner, n.Personal), Slug: a.Slug}, time.Now()}
		s.gh.mu.Unlock()
		return "refused", fmt.Errorf("the app %s isn't what Banda asked for: %s", a.Slug, why)
	}
	change := store.GitHubAppChange{Action: "created", By: userID, Host: host, Slug: a.Slug, Owner: n.Owner}
	if cur != nil {
		change.Action, change.WasSlug = "replaced", cur.Slug
		if row, err := s.bandaMade(ctx, cur); err == nil && row != nil {
			change.WasOwner = row.Owner
		}
		if err := s.dropConnections(ctx, cur); err != nil { // while its credentials still work
			return "failed", err
		}
	}
	if err := s.sandboxes.PutSecret(ctx, GitHubAppSecret, "github-app", n.Data); err != nil {
		return "failed", err
	}
	s.appChanged()
	slog.Info("github app "+change.Action, "by", userID, "host", host, "id", a.ID, "was", change.WasSlug)
	err = s.store.PutGitHubApp(ctx, store.GitHubApp{Host: host, Owner: n.Owner, CreatedBy: userID, AppID: a.ID, Slug: a.Slug, Personal: n.Personal}, change)
	if err != nil {
		return "failed", err
	}
	return "created", nil
}
