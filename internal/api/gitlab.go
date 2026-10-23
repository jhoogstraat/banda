package api

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
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

// The team's GitLab applications, one per GitLab host (internal/forge/gitlab_app.go),
// and users' connections through them. Anyone signed in sets one up in
// Settings, as with team secrets: its ID and secret go to the Secret
// banda-gitlab-app-<host>, which no pod mounts; Postgres keeps who and when.
// Connecting authorizes the application twice, and both grants' tokens are in
// the user's banda-forge-<hash> Secret, never mounted either: api (merge
// requests, the repo picker) stays in the API, write_repository (git only) is
// what GitCredential hands the thread's pod. Nothing here logs or returns a
// token or the secret.

// GitLabAppSecret prefixes the applications' Secrets (+ host). Tests running
// APIs side by side in one namespace give each its own (TEST_GITLAB_APP_SECRET).
var GitLabAppSecret = "banda-gitlab-app-"

type gitLabs struct {
	mu   sync.Mutex
	apps map[string]cachedGitLabApp // host
	// pending holds a connection's api token between its two grants, in
	// memory only, keyed by the flow's nonce (signed into the git grant's
	// state): an abandoned flow leaves nothing stored, and its grant is
	// revoked when it is swept.
	pending map[string]pendingGrant // nonce
	// api and git keep the two grants' tokens, each refreshed on its own.
	api, git *tokenKeeper
}

type cachedGitLabApp struct {
	app *forge.GitLabApp
	err error
	at  time.Time
}

type pendingGrant struct {
	user, host string
	tok        forge.UserToken
	at         time.Time
}

// gitPrefix marks the git grant's keys in the user's forge Secret.
const gitPrefix = "git-"

func newGitLabs(sb *sandbox.Client) *gitLabs {
	api := &tokenKeeper{conns: map[string]*userConn{},
		load: func(ctx context.Context, user, host string) (map[string][]byte, error) {
			return sb.Secret(ctx, sandbox.ForgeSecret(user, host))
		},
		save: func(ctx context.Context, user, host string, data map[string][]byte) error {
			return sb.PutSecret(ctx, sandbox.ForgeSecret(user, host), "forge-connection", data)
		}}
	return &gitLabs{apps: map[string]cachedGitLabApp{}, pending: map[string]pendingGrant{}, api: api, git: prefixed(api, gitPrefix)}
}

// prefixed keeps tokens where base does, under keys with prefix: the git
// grant's beside the api grant's in one Secret (Secrets are merged on save).
func prefixed(base *tokenKeeper, prefix string) *tokenKeeper {
	return &tokenKeeper{conns: map[string]*userConn{},
		load: func(ctx context.Context, user, host string) (map[string][]byte, error) {
			data, err := base.load(ctx, user, host)
			out := map[string][]byte{}
			for k, v := range data {
				if rest, ok := strings.CutPrefix(k, prefix); ok {
					out[rest] = v
				}
			}
			return out, err
		},
		save: func(ctx context.Context, user, host string, data map[string][]byte) error {
			out := map[string][]byte{}
			for k, v := range data {
				out[prefix+k] = v
			}
			return base.save(ctx, user, host, out)
		}}
}

// gitlabHosts are the GitLab hosts Banda knows, gitlab.com first.
func gitlabHosts() []string {
	var hosts []string
	for host, kind := range Forges.Known() {
		if kind == forge.GitLab && host != "gitlab.com" {
			hosts = append(hosts, host)
		}
	}
	slices.Sort(hosts)
	if Forges.Known()["gitlab.com"] == forge.GitLab {
		hosts = append([]string{"gitlab.com"}, hosts...)
	}
	return hosts
}

// gitLabAppSecret is the name of host's application Secret; a port
// (FORGE_HOSTS' host:port) becomes -port, as Kubernetes names have no colon.
func gitLabAppSecret(host string) string {
	return GitLabAppSecret + strings.ReplaceAll(host, ":", "-")
}

// gitLabApp is the team's application on host, nil if there is none, read
// from its Secret at most every 30 s (one made by hand applies within that).
// An unusable Secret is an error, which Settings shows. Another application
// ID in its place (a Secret edited by hand) ends the connections through the
// one before, as GitHub's gitHubApp does.
func (s *Service) gitLabApp(ctx context.Context, host string) (*forge.GitLabApp, error) {
	g := s.gl
	g.mu.Lock()
	c, ok := g.apps[host]
	if ok && time.Since(c.at) < 30*time.Second {
		g.mu.Unlock()
		return c.app, c.err
	}
	data, err := s.sandboxes.Secret(ctx, gitLabAppSecret(host))
	if err != nil {
		g.mu.Unlock()
		return nil, err // e.g. no RBAC: not cached, tried again
	}
	var app *forge.GitLabApp
	if data != nil {
		data[forge.GitLabAppSecret.Host] = []byte(host) // the Secret's name says which host
		if app, err = Forges.GitLabApp(data); err != nil {
			err = fmt.Errorf("the Secret %s is unusable: %w", gitLabAppSecret(host), err)
		}
	}
	var replaced *forge.GitLabApp
	switch {
	case app != nil && c.app != nil && app.ClientID == c.app.ClientID && app.ClientSecret == c.app.ClientSecret:
		app = c.app // the same application keeps its state (needs attention)
	case app != nil && c.app != nil && app.ClientID != c.app.ClientID:
		replaced = c.app
	}
	g.apps[host] = cachedGitLabApp{app, err, time.Now()}
	g.mu.Unlock()
	// ponytail: noticed by this API while running; connections through an application replaced by hand while it was down end when GitLab refuses them.
	if replaced != nil {
		slog.Info("gitlab app replaced", "host", host, "was", replaced.ClientID, "now", app.ClientID)
		if err := s.endGitLabConnections(context.WithoutCancel(ctx), replaced); err != nil {
			slog.Error("end connections through the replaced gitlab app", "host", host, "err", err)
		}
	}
	return app, err
}

// gitLabAppFor is the team's application on host, nil if there is none or it is unusable.
func (s *Service) gitLabAppFor(ctx context.Context, host string) *forge.GitLabApp {
	a, err := s.gitLabApp(ctx, host)
	if err != nil {
		slog.Error("gitlab app", "host", host, "err", err)
	}
	return a
}

// gitLabAppIs caches what Settings just put in host's Secret (nil: removed),
// so the next read doesn't take it for a change made by hand.
func (s *Service) gitLabAppIs(host string, a *forge.GitLabApp) {
	s.gl.mu.Lock()
	s.gl.apps[host] = cachedGitLabApp{a, nil, time.Now()}
	s.gl.mu.Unlock()
}

// gitLabToken is the user's token of one grant (s.gl.api or s.gl.git) on the
// application's host, refreshed when it is about to expire; empty if they
// haven't connected. A grant GitLab refuses breaks the connection.
func (s *Service) gitLabToken(ctx context.Context, k *tokenKeeper, userID string, a *forge.GitLabApp) (forge.UserToken, error) {
	t, err := k.fresh(ctx, a, a.Host, userID)
	if errors.Is(err, forge.ErrRevoked) {
		s.gitLabRefused(ctx, userID, a.Host)
	}
	return t, err
}

// gitLabRefused marks the user's connection broken, revokes what is left of
// it and drops its tokens.
func (s *Service) gitLabRefused(ctx context.Context, userID, host string) {
	ctx = context.WithoutCancel(ctx)
	if a := s.gitLabAppFor(ctx, host); a != nil {
		s.revokeGitLab(ctx, a, userID) // best effort: the user may have revoked the application already
	}
	s.gl.api.forget(userID, host)
	s.gl.git.forget(userID, host)
	if err := s.sandboxes.DeleteSecret(ctx, sandbox.ForgeSecret(userID, host)); err != nil {
		slog.Error("delete refused forge tokens", "user", userID, "err", err)
	}
	if err := s.store.BreakForgeConnection(ctx, userID, host); err != nil {
		slog.Error("break forge connection", "user", userID, "err", err)
	}
}

// gitLabTokenRefused hears of a token GitLab answered 401 to: if it is a
// user's api token, they revoked the application.
func (s *Service) gitLabTokenRefused(token string) {
	if user, host := s.gl.api.owner(token); user != "" {
		slog.Info("gitlab refused a user's token", "user", user)
		s.gitLabRefused(context.Background(), user, host)
	}
}

// revokeGitLab revokes both of the user's grants on GitLab, by their refresh
// tokens (which outlive the access tokens), and says whether that worked.
func (s *Service) revokeGitLab(ctx context.Context, a *forge.GitLabApp, userID string) error {
	var errs []error
	for _, k := range []*tokenKeeper{s.gl.api, s.gl.git} {
		c := k.conn(userID, a.Host)
		c.mu.Lock()
		if !c.loaded {
			data, err := k.load(ctx, userID, a.Host)
			if err != nil {
				c.mu.Unlock()
				return err
			}
			c.tok, c.loaded = decodeUserToken(data), true
		}
		tok := c.tok
		c.mu.Unlock()
		if t := cmp.Or(tok.Refresh, tok.Access); t != "" {
			errs = append(errs, a.Revoke(ctx, t))
		}
	}
	return errors.Join(errs...)
}

// forgetGitLab forgets the user's connection on host: tokens, Secret and row.
func (s *Service) forgetGitLab(ctx context.Context, userID, host string) error {
	s.gl.api.forget(userID, host)
	s.gl.git.forget(userID, host)
	if err := s.sandboxes.DeleteSecret(ctx, sandbox.ForgeSecret(userID, host)); err != nil {
		return err
	}
	return s.store.DeleteForgeConnection(ctx, userID, host)
}

// gitLabCredential gives the thread's runner its owner's git-only token for
// the thread's GitLab host, as Postgres has the repository; the pod's word
// counts for nothing. GitLab can't narrow it to the one project: it reaches
// every project the owner can push to, over git, for at most two hours.
func (s *Service) gitLabCredential(ctx context.Context, t store.Thread, repo forge.Repo) (*bandav1.GitCredentialResponse, error) {
	a, err := s.gitLabApp(ctx, repo.Host)
	if err != nil {
		return nil, refuse(fmt.Sprintf("the team's GitLab application for %s needs attention: see GitLab in Settings", repo.Host))
	}
	if a == nil {
		return nil, refuse(fmt.Sprintf("set up GitLab for %s in Settings, so Banda can push as you (or add a GIT_TOKEN secret)", repo.Host))
	}
	tok, err := s.gitLabToken(ctx, s.gl.git, t.OwnerID, a)
	if errors.Is(err, forge.ErrRevoked) {
		return nil, refuse("reconnect GitLab in Settings: GitLab no longer accepts your connection")
	}
	if err != nil {
		return nil, forgeError(err)
	}
	if tok.Access == "" {
		c, err := s.store.ForgeConnection(ctx, t.OwnerID, repo.Host)
		if err != nil {
			return nil, err
		}
		if c != nil && c.Broken {
			return nil, refuse("reconnect GitLab in Settings: GitLab no longer accepts your connection")
		}
		return nil, refuse("connect GitLab in Settings, so Banda can push as you")
	}
	res := &bandav1.GitCredentialResponse{Username: "oauth2", Password: tok.Access}
	if !tok.Expiry.IsZero() {
		res.ExpiresAt = timestamppb.New(tok.Expiry)
	}
	return res, nil
}

func (s *Service) GetGitLab(ctx context.Context, _ *bandav1.GetGitLabRequest) (*bandav1.GetGitLabResponse, error) {
	res := &bandav1.GetGitLabResponse{RedirectUri: s.PublicURL + "/gitlab/callback", Scopes: []string{forge.GitLabAPIScope, forge.GitLabGitScope}}
	legacy, err := s.legacyToken(ctx, user(ctx), forge.GitLab)
	if err != nil {
		return nil, err
	}
	res.LegacyToken = legacy.Value != ""
	for _, host := range gitlabHosts() {
		h := &bandav1.GitLabHost{Host: host, GroupAppsUrl: Forges.GitLabAppsURL(host, "group", "{group}")}
		if host != "gitlab.com" {
			h.AdminAppsUrl = Forges.GitLabAppsURL(host, "admin", "")
		}
		res.Hosts = append(res.Hosts, h)
		if c, err := s.store.LastGitLabAppChange(ctx, host); err != nil {
			return nil, err
		} else if c != nil {
			h.LastChange = &bandav1.GitLabAppChange{Action: c.Action, By: c.By, At: timestamppb.New(c.At), ApplicationId: c.ApplicationID,
				WasApplicationId: c.WasApplicationID}
		}
		a, appErr := s.gitLabApp(ctx, host)
		if a == nil && appErr == nil {
			continue
		}
		if a == nil {
			h.App = &bandav1.GitLabApp{Error: appErr.Error()}
			continue
		}
		h.App = &bandav1.GitLabApp{ApplicationId: a.ClientID, NeedsAttention: a.Misconfigured()}
		if row, err := s.gitLabBandaMade(ctx, a); err != nil {
			return nil, err
		} else if row != nil {
			h.App.CreatedBy, h.App.CreatedAt = row.CreatedBy, timestamppb.New(row.CreatedAt)
		}
		c, err := s.store.ForgeConnection(ctx, user(ctx), host)
		if err != nil {
			return nil, err
		}
		if c != nil {
			h.Connection = &bandav1.ForgeConnection{Host: c.Host, Forge: c.Forge, Login: c.Login, AvatarUrl: c.AvatarURL,
				ConnectedAt: timestamppb.New(c.ConnectedAt), Broken: c.Broken}
		}
	}
	return res, nil
}

// knownGitLab checks a host the browser names.
func knownGitLab(host string) (string, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		host = "gitlab.com"
	}
	if !slices.Contains(gitlabHosts(), host) {
		return "", connect.NewError(connect.CodeInvalidArgument, host+" isn't a GitLab host Banda knows: name it in FORGE_HOSTS as "+host+"=gitlab")
	}
	return host, nil
}

// SetGitLabApp keeps the application's ID and secret once GitLab accepts
// them. Another application on the host ends the connections through the old one.
func (s *Service) SetGitLabApp(ctx context.Context, req *bandav1.SetGitLabAppRequest) (*bandav1.SetGitLabAppResponse, error) {
	host, err := knownGitLab(req.Host)
	if err != nil {
		return nil, err
	}
	id, secret := strings.TrimSpace(req.ApplicationId), strings.TrimSpace(req.Secret)
	if id == "" || secret == "" || len(id) > 256 || len(secret) > 256 || strings.ContainsAny(id+secret, " \t\r\n") {
		return nil, connect.NewError(connect.CodeInvalidArgument, "paste the Application ID and the Secret from the application's page on GitLab")
	}
	data := map[string][]byte{forge.GitLabAppSecret.Host: []byte(host), forge.GitLabAppSecret.ID: []byte(id), forge.GitLabAppSecret.Secret: []byte(secret)}
	a, err := Forges.GitLabApp(data)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error())
	}
	old, err := s.gitLabReplaceable(ctx, host, req.Replace)
	if err != nil {
		return nil, err
	}
	if err := a.Verify(ctx); err != nil {
		return nil, forgeError(err)
	}
	change := store.GitLabAppChange{Action: "created", By: user(ctx), Host: host, ApplicationID: a.ClientID}
	if old != nil {
		change.Action, change.WasApplicationID = "replaced", old.ClientID
		if old.ClientID != a.ClientID { // a renewed secret of the same application keeps its connections
			if err := s.endGitLabConnections(ctx, old); err != nil { // while its credentials still work
				return nil, err
			}
		}
	}
	if err := s.sandboxes.PutSecret(ctx, gitLabAppSecret(host), "gitlab-app", data); err != nil {
		return nil, err
	}
	s.gitLabAppIs(host, a)
	slog.Info("gitlab app "+change.Action, "by", user(ctx), "host", host, "id", a.ClientID, "was", change.WasApplicationID)
	return &bandav1.SetGitLabAppResponse{}, s.store.PutGitLabApp(ctx, store.GitLabApp{Host: host, CreatedBy: user(ctx), ApplicationID: a.ClientID}, change)
}

// gitLabBandaMade is the application's row if Banda set a up from Settings,
// nil if it was made by hand (Helm, GitOps). Rows from before application
// ids were kept count as Banda's.
func (s *Service) gitLabBandaMade(ctx context.Context, a *forge.GitLabApp) (*store.GitLabApp, error) {
	row, err := s.store.GitLabApp(ctx, a.Host)
	if err != nil || row == nil || row.ApplicationID != "" && row.ApplicationID != a.ClientID {
		return nil, err
	}
	return row, nil
}

// gitLabReplaceable is host's application a new one would replace (nil:
// none), or why it can't be: one made by hand is changed where it was made,
// and replacing needs asking for (replace), as with the GitHub App.
func (s *Service) gitLabReplaceable(ctx context.Context, host string, replace bool) (*forge.GitLabApp, error) {
	a, err := s.gitLabApp(ctx, host)
	if a == nil && err != nil {
		return nil, refuse("Banda can't use the GitLab application it has for " + host + " (" + err.Error() + "): remove it before setting up another")
	}
	if a == nil {
		return nil, nil
	}
	row, err := s.gitLabBandaMade(ctx, a)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, refuse(fmt.Sprintf("the GitLab application for %s comes from the deployment (the Secret %s, by Helm or GitOps): change it there", host, gitLabAppSecret(host)))
	}
	if !replace {
		return nil, refuse(fmt.Sprintf("the team already has a GitLab application for %s: replacing it ends everyone's connection, so it has to be asked for", host))
	}
	return a, nil
}

// endGitLabConnections revokes and forgets everyone's connection through a,
// best effort at GitLab: their tokens are the application's.
func (s *Service) endGitLabConnections(ctx context.Context, a *forge.GitLabApp) error {
	users, err := s.store.ForgeConnectionUsers(ctx, a.Host)
	if err != nil {
		return err
	}
	for _, u := range users {
		if err := s.revokeGitLab(ctx, a, u); err != nil {
			slog.Info("revoke gitlab connection", "user", u, "err", err)
		}
		if err := s.forgetGitLab(ctx, u, a.Host); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) RemoveGitLabApp(ctx context.Context, req *bandav1.RemoveGitLabAppRequest) (*bandav1.RemoveGitLabAppResponse, error) {
	host, err := knownGitLab(req.Host)
	if err != nil {
		return nil, err
	}
	change := store.GitLabAppChange{Action: "removed", By: user(ctx), Host: host}
	if a, _ := s.gitLabApp(ctx, host); a != nil {
		change.WasApplicationID = a.ClientID
		if err := s.endGitLabConnections(ctx, a); err != nil { // while its credentials still work
			return nil, err
		}
	}
	if err := s.sandboxes.DeleteSecret(ctx, gitLabAppSecret(host)); err != nil {
		return nil, err
	}
	s.gitLabAppIs(host, nil)
	slog.Info("gitlab app removed", "by", user(ctx), "host", host, "was", change.WasApplicationID)
	return &bandav1.RemoveGitLabAppResponse{}, s.store.DeleteGitLabApp(ctx, change)
}

// DisconnectGitLab revokes both of the caller's grants on GitLab, then
// forgets them. One already refused needs no revoking.
func (s *Service) DisconnectGitLab(ctx context.Context, req *bandav1.DisconnectGitLabRequest) (*bandav1.DisconnectGitLabResponse, error) {
	host, err := knownGitLab(req.Host)
	if err != nil {
		return nil, err
	}
	if a := s.gitLabAppFor(ctx, host); a != nil {
		if err := s.revokeGitLab(ctx, a, user(ctx)); err != nil {
			return nil, forgeError(err)
		}
	}
	return &bandav1.DisconnectGitLabResponse{}, s.forgetGitLab(ctx, user(ctx), host)
}

// GitLabHandler serves the browser's side of connecting, returning to
// Settings with ?gitlab=<outcome>:
//   - /gitlab/connect?host= starts it: to GitLab for the api grant, with a
//     state bound to the user and PKCE from it; only from Banda's own pages
//     (crossSite), so another site can't make a signed-in teammate reconnect;
//   - /gitlab/callback takes the api grant's code and goes on to GitLab for
//     the git grant (write_repository), then takes that one's and is done,
//     back on the thread it started from (?back=, see backTo) if any.
//
// Both grants or neither: the api token waits in memory for the git one.
func (s *Service) GitLabHandler(httpUser func(*http.Request) string) http.Handler {
	mux := http.NewServeMux()
	back := func(w http.ResponseWriter, r *http.Request, outcome string, more ...string) {
		q := url.Values{"gitlab": {outcome}}
		for i := 0; i+1 < len(more); i += 2 {
			q.Set(more[i], more[i+1])
		}
		http.Redirect(w, r, "/settings?"+q.Encode()+"#gitlab", http.StatusFound)
	}
	redirect := s.PublicURL + "/gitlab/callback"
	mux.HandleFunc("GET /gitlab/connect", func(w http.ResponseWriter, r *http.Request) {
		u := httpUser(r)
		if u == "" {
			http.Redirect(w, r, "/signin?next=/settings%23gitlab", http.StatusFound)
			return
		}
		if crossSite(r) {
			back(w, r, "cross-site")
			return
		}
		host, err := knownGitLab(r.URL.Query().Get("host"))
		a := s.gitLabAppFor(r.Context(), host)
		if err != nil || a == nil {
			back(w, r, "no-app")
			return
		}
		state := s.gh.key.sign(strings.Join([]string{"gitlab-api", u, host, rand.Text(), backTo(r)}, "|"), time.Now().Add(10*time.Minute))
		http.Redirect(w, r, a.AuthorizeURL(redirect, forge.GitLabAPIScope, state, s.gh.verifier(state)), http.StatusFound)
	})
	mux.HandleFunc("GET /gitlab/callback", func(w http.ResponseWriter, r *http.Request) {
		q, u := r.URL.Query(), httpUser(r)
		state := q.Get("state")
		_, host, apiExtra, first := s.gh.checkState(state, "gitlab-api", u)
		_, gitHost, extra, second := s.gh.checkState(state, "gitlab-git", u)
		first, second = first && len(apiExtra) == 1, second && len(extra) == 2
		if !first && !second {
			back(w, r, "state")
			return
		}
		if q.Get("error") != "" { // access_denied: they cancelled
			if second {
				s.dropPending(r.Context(), extra[0])
			}
			back(w, r, "denied")
			return
		}
		if first {
			next, err := s.gitLabFirstGrant(r.Context(), u, host, q.Get("code"), state, apiExtra[0])
			if err != nil {
				slog.Error("connect gitlab", "user", u, "err", err)
				back(w, r, "failed")
				return
			}
			http.Redirect(w, r, next, http.StatusFound)
			return
		}
		err := s.gitLabSecondGrant(r.Context(), u, gitHost, extra[0], q.Get("code"), state)
		var other *otherAccount
		if errors.As(err, &other) {
			back(w, r, "account", "login", other.login)
			return
		}
		if err != nil {
			slog.Error("connect gitlab", "user", u, "err", err)
			back(w, r, "failed")
			return
		}
		if extra[1] != "" {
			http.Redirect(w, r, extra[1], http.StatusFound)
			return
		}
		back(w, r, "connected")
	})
	return mux
}

// crossSite: the browser says another site sent this request (a link or a
// form there). Banda's own pages are same-origin; typing the URL or an old
// browser sends none or no header, which is fine.
func crossSite(r *http.Request) bool {
	site := r.Header.Get("Sec-Fetch-Site")
	return site == "cross-site" || site == "same-site"
}

// otherAccount: the git grant came from another GitLab account than the api
// grant (the browser's GitLab session changed between the two).
type otherAccount struct{ login string }

func (e *otherAccount) Error() string {
	return "the two grants came from different GitLab accounts; the first was @" + e.login
}

// gitLabFirstGrant takes the api grant and returns where the git grant
// starts. The api token waits under a new nonce, which the git grant's state carries.
func (s *Service) gitLabFirstGrant(ctx context.Context, userID, host, code, state, back string) (string, error) {
	a := s.gitLabAppFor(ctx, host)
	if a == nil {
		return "", errors.New("the application changed during the flow")
	}
	redirect := s.PublicURL + "/gitlab/callback"
	tok, err := a.Exchange(ctx, redirect, forge.GitLabAPIScope, code, s.gh.verifier(state))
	if err != nil {
		return "", err
	}
	nonce := rand.Text()
	s.gl.mu.Lock()
	var stale []pendingGrant
	for k, p := range s.gl.pending { // ponytail: swept on use; a team starts few flows
		if time.Since(p.at) > 10*time.Minute {
			stale = append(stale, p)
			delete(s.gl.pending, k)
		}
	}
	s.gl.pending[nonce] = pendingGrant{userID, host, tok, time.Now()}
	s.gl.mu.Unlock()
	for _, p := range stale { // abandoned flows: their api grants end on GitLab too
		s.revokePending(ctx, p)
	}
	next := s.gh.key.sign(strings.Join([]string{"gitlab-git", userID, host, rand.Text(), nonce, back}, "|"), time.Now().Add(10*time.Minute))
	return a.AuthorizeURL(redirect, forge.GitLabGitScope, next, s.gh.verifier(next)), nil
}

// takePending removes and returns the api grant waiting under nonce.
func (s *Service) takePending(nonce string) (pendingGrant, bool) {
	s.gl.mu.Lock()
	defer s.gl.mu.Unlock()
	p, ok := s.gl.pending[nonce]
	delete(s.gl.pending, nonce)
	return p, ok
}

// dropPending revokes and forgets an api grant whose git grant won't come.
func (s *Service) dropPending(ctx context.Context, nonce string) {
	if p, ok := s.takePending(nonce); ok {
		s.revokePending(ctx, p)
	}
}

func (s *Service) revokePending(ctx context.Context, p pendingGrant) {
	if a := s.gitLabAppFor(ctx, p.host); a != nil {
		if err := a.Revoke(ctx, p.tok.Refresh); err != nil {
			slog.Info("revoke abandoned gitlab grant", "user", p.user, "err", err)
		}
	}
}

// gitLabSecondGrant takes the git grant and saves the connection: tokens
// first, then the row that says it exists. Both grants must be the same
// GitLab account's. Grants it replaces are revoked.
func (s *Service) gitLabSecondGrant(ctx context.Context, userID, host, nonce, code, state string) error {
	p, ok := s.takePending(nonce)
	a := s.gitLabAppFor(ctx, host)
	if !ok || a == nil || p.user != userID || p.host != host {
		return errors.New("the api grant is gone (the API restarted, or the application changed)")
	}
	gitTok, err := a.Exchange(ctx, s.PublicURL+"/gitlab/callback", forge.GitLabGitScope, code, s.gh.verifier(state))
	if err != nil {
		a.Revoke(ctx, p.tok.Refresh)
		return err
	}
	revoke := func() {
		a.Revoke(ctx, p.tok.Refresh)
		a.Revoke(ctx, gitTok.Refresh)
	}
	u, err := a.User(ctx, p.tok.Access)
	if err != nil {
		revoke()
		return err
	}
	gitOwner, err := a.Owner(ctx, gitTok.Access)
	if err != nil {
		revoke()
		return err
	}
	if gitOwner != u.ID {
		revoke()
		return &otherAccount{u.Username}
	}
	if err := s.revokeGitLab(ctx, a, userID); err != nil { // an earlier connection's
		slog.Info("revoke replaced gitlab grants", "user", userID, "err", err)
	}
	if err := s.gl.api.put(ctx, userID, host, p.tok); err == nil {
		err = s.gl.git.put(ctx, userID, host, gitTok)
	}
	if err == nil {
		err = s.store.PutForgeConnection(ctx, store.ForgeConnection{UserID: userID, Host: host, Forge: forge.GitLab, Login: u.Username,
			ForgeID: u.ID, AvatarURL: u.AvatarURL})
	}
	if err != nil {
		revoke()
		s.forgetGitLab(context.WithoutCancel(ctx), userID, host)
	}
	return err
}
