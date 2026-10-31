package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/identity"
	"github.com/jhoogstraat/banda/internal/sandbox"
	"github.com/jhoogstraat/banda/internal/store"
)

// Workload identity: each running thread's pod gets short-lived tokens
// naming its owner, one per audience of the team's, which secret managers and
// clouds that trust Banda's issuer exchange for their own credentials. Tokens
// are minted on request and never stored or logged.

const maxAudiences = 20

// IdTokens mints the thread's tokens. The owner is read now, so a handoff
// changes whom they name from the next fetch on. Only a connected runner gets
// them: a runner token taken from a pod mints nothing once its pod is gone.
// A set is reused while younger than the refresh interval (so it has more
// than 10 minutes left), which keeps a runner asking in a loop from making
// the API sign.
func (s *Service) IdTokens(ctx context.Context, req *bandav1.IdTokensRequest) (*bandav1.IdTokensResponse, error) {
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
	auds, err := s.store.Audiences(ctx)
	if err == nil && t.TeamIdentity && s.TeamIdentity {
		auds = append(auds, store.Audience{Audience: sandbox.TeamAudience})
	}
	if err != nil || len(auds) == 0 {
		return &bandav1.IdTokensResponse{RefreshSeconds: int32(s.IDTokenRefresh / time.Second)}, err
	}
	email, verified, shared, err := s.store.IdentityEmail(ctx, t.OwnerID)
	if err != nil {
		return nil, err
	}
	if refusal := identityRefusal(email, verified, shared); refusal != "" {
		s.refuseIdTokens(ctx, t.ID, refusal)
		return nil, connect.NewError(connect.CodeFailedPrecondition, refusal)
	}
	c := identity.Claims{Email: email, Thread: t.ID, Repo: repoClaim(t.RepoURL), Branch: t.Branch}
	key := strings.Join([]string{c.Email, c.Thread, c.Repo, c.Branch}, "\x00")
	for _, a := range auds {
		key += "\x00" + a.Audience
	}
	now := time.Now()
	s.mu.Lock()
	m := s.idTokens[t.ID]
	s.mu.Unlock()
	if m.key == key && now.Sub(m.at) < s.IDTokenRefresh {
		return m.res, nil
	}
	res := &bandav1.IdTokensResponse{RefreshSeconds: int32(s.IDTokenRefresh / time.Second)}
	for _, a := range auds {
		c.Audience, c.NoJTI = a.Audience, a.Audience == sandbox.TeamAudience
		tok, exp, err := s.Identity.Mint(c, now)
		if err != nil {
			return nil, err
		}
		res.Tokens = append(res.Tokens, &bandav1.IdToken{Audience: a.Audience, File: audienceFile(a.Audience), Token: tok, ExpiresAt: timestamppb.New(exp)})
	}
	s.mu.Lock()
	s.idTokens[t.ID] = mintedTokens{key: key, at: now, res: res}
	s.mu.Unlock()
	return res, nil
}

// mintedTokens is a thread's latest tokens, for the claims and audiences in
// key, or why it got none (refused).
type mintedTokens struct {
	key     string
	at      time.Time
	res     *bandav1.IdTokensResponse
	refused string
}

// identityRefusal says why tokens can't name the owner's email, or "". The
// email is the subject teams write policies for, so it must be the IdP's
// verified word and name one person.
func identityRefusal(email string, verified, shared bool) string {
	switch {
	case email == "":
		return "No identity tokens: they name the thread's owner by email, and the owner signed in without one."
	case !verified:
		return "No identity tokens: the identity provider hasn't verified the email of the thread's owner, " + email + ". Verify it there and sign in again."
	case shared:
		return "No identity tokens: another Banda user signed in with the email of the thread's owner, " + email + ", so it doesn't name one person."
	}
	return ""
}

// refuseIdTokens tells the thread's chat why it gets no tokens, once per
// reason, and forgets its cached set (tokens a pod already has run out within 15 minutes).
func (s *Service) refuseIdTokens(ctx context.Context, id, why string) {
	s.mu.Lock()
	seen := s.idTokens[id].refused == why
	s.idTokens[id] = mintedTokens{refused: why}
	s.mu.Unlock()
	if seen {
		return
	}
	e := &bandav1.Event{Actor: "system", Kind: &bandav1.Event_Notice{Notice: &bandav1.Notice{Text: why}}}
	if _, err := s.apply(ctx, id, nil, e); err != nil {
		slog.Error("report refused identity tokens", "thread", id, "err", err)
	}
}

func (s *Service) GetWorkloadIdentity(ctx context.Context, _ *bandav1.GetWorkloadIdentityRequest) (*bandav1.GetWorkloadIdentityResponse, error) {
	auds, err := s.store.Audiences(ctx)
	if err != nil {
		return nil, err
	}
	urls, err := s.store.RecentRepos(ctx, 20)
	if err != nil {
		return nil, err
	}
	who, err := s.store.GitIdentity(ctx, user(ctx))
	if err != nil {
		return nil, err
	}
	res := &bandav1.GetWorkloadIdentityResponse{Issuer: s.Identity.URL, JwksUrl: s.Identity.URL + identity.JWKSPath, Email: who.SignInEmail}
	for _, a := range auds {
		res.Audiences = append(res.Audiences, &bandav1.IdTokenAudience{Audience: a.Audience, Path: path.Join(sandbox.IDTokenDir, audienceFile(a.Audience)),
			AddedAt: timestamppb.New(a.AddedAt), AddedBy: a.AddedBy})
	}
	seen := map[string]bool{}
	for _, u := range urls {
		if r := repoClaim(u); r != "" && !seen[r] {
			seen[r] = true
			res.Repos = append(res.Repos, r)
		}
	}
	return res, nil
}

func (s *Service) AddIdTokenAudience(ctx context.Context, req *bandav1.AddIdTokenAudienceRequest) (*bandav1.AddIdTokenAudienceResponse, error) {
	aud := strings.TrimSpace(req.Audience)
	if aud == "" || len(aud) > 200 || badAudience.MatchString(aud) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "an audience is up to 200 characters without spaces, such as sts.amazonaws.com or vault")
	}
	if aud == sandbox.TeamAudience { // it would reach every thread, also those on a subscription
		return nil, connect.NewError(connect.CodeInvalidArgument, "that is the team's Console identity, which goes only to threads on it (docs/configuration.md)")
	}
	auds, err := s.store.Audiences(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range auds {
		if a.Audience == aud {
			return &bandav1.AddIdTokenAudienceResponse{}, nil
		}
		if audienceFile(a.Audience) == audienceFile(aud) {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Sprintf("its token file would be %s's, %s", a.Audience, audienceFile(aud)))
		}
	}
	if len(auds) >= maxAudiences {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Sprintf("at most %d audiences", maxAudiences))
	}
	if err := s.store.AddAudience(ctx, aud, user(ctx)); err != nil {
		return nil, err
	}
	slog.Info("identity token audience added", "audience", aud, "by", user(ctx))
	s.idTokensChanged()
	return &bandav1.AddIdTokenAudienceResponse{}, nil
}

func (s *Service) RemoveIdTokenAudience(ctx context.Context, req *bandav1.RemoveIdTokenAudienceRequest) (*bandav1.RemoveIdTokenAudienceResponse, error) {
	if err := s.store.RemoveAudience(ctx, req.Audience); err != nil {
		return nil, err
	}
	slog.Info("identity token audience removed", "audience", req.Audience, "by", user(ctx))
	s.idTokensChanged()
	return &bandav1.RemoveIdTokenAudienceResponse{}, nil
}

// idTokensChanged tells every connected thread runner to fetch its tokens,
// a second later: changes in that second go out together.
func (s *Service) idTokensChanged() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idTokensDue {
		return
	}
	s.idTokensDue = true
	time.AfterFunc(time.Second, func() {
		msg := &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_IdTokensChanged{IdTokensChanged: &bandav1.IdTokensChanged{}}}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.idTokensDue = false
		for key, r := range s.runners {
			if !strings.HasPrefix(key, loginPrefix) {
				go r.send(msg) // may wait for a slow runner; not under mu
			}
		}
	})
}

var (
	badAudience = regexp.MustCompile(`[\s\p{C}]`)
	notFileSafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)
	scheme      = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`)
)

// audienceFile is the file name of an audience's token: without a scheme,
// anything but letters, digits, '.', '_' and '-' an underscore, and never
// hidden ("." and ".." included).
func audienceFile(aud string) string {
	f := notFileSafe.ReplaceAllString(scheme.ReplaceAllString(aud, ""), "_")
	if f == "" || f[0] == '.' {
		f = "_" + f
	}
	return f
}

// repoClaim names a repository as host[:port]/path, without .git and
// without the scheme's default port: what policies bind the repo claim to.
// Empty without a repository, for a local one (file://), and for anything
// with characters other than letters, digits and "._/-" (":" only before
// the port) or with "." and ".." segments, so the claim can't pass for
// another repository or break out of the recipes it is pasted into.
func repoClaim(repo string) string {
	var host, port, p string
	if u, err := url.Parse(repo); err == nil && u.Scheme != "" {
		host, port, p = u.Hostname(), u.Port(), u.Path
		if port == defaultPorts[u.Scheme] {
			port = ""
		}
	} else if at := strings.Index(repo, "@"); at >= 0 { // user@host:path
		host, p, _ = strings.Cut(repo[at+1:], ":")
	}
	p = strings.TrimSuffix(strings.Trim(p, "/"), ".git")
	host = strings.ToLower(host)
	if port != "" {
		host += ":" + port
	}
	if !claimHost.MatchString(host) || p != "" && (!claimPath.MatchString(p) || path.Clean(p) != p) {
		return ""
	}
	if p == "" {
		return host
	}
	return host + "/" + p
}

var (
	defaultPorts = map[string]string{"https": "443", "http": "80", "ssh": "22", "git": "9418"}
	claimHost    = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*(:[0-9]+)?$`)
	claimPath    = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)
)
