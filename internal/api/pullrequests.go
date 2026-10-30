package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/forge"
	"github.com/jhoogstraat/banda/internal/store"
)

// Pull requests (GitLab: merge requests): everyone sees a thread's, fetched
// with its owner's token and kept in Postgres, so viewers and lists never hit
// the forge. The owner creates, links, edits and merges it with their token;
// anyone comments with their own.

// forgeTokens caches users' legacy forge tokens (secrets) briefly: reading
// three Secrets per call would make every page load hit Kubernetes. Never
// logged or returned.
// Forges says which repository hosts are GitHub or GitLab, for toProto too. Set before serving.
var Forges forge.Config

type forgeTokens struct {
	mu sync.Mutex
	m  map[string]cachedToken // user + " " + forge kind
	// backoff holds owners whose forge said its rate limit is used up, until when.
	backoff map[string]time.Time
}

type cachedToken struct {
	value, name string
	at          time.Time
}

const tokenTTL = 30 * time.Second

// tokenNames are the secrets a forge's token may be in, first match wins.
// GitLab takes GITLAB_TOKEN first, for someone who needs both forges.
var tokenNames = map[string][]string{forge.GitHub: {"GIT_TOKEN"}, forge.GitLab: {"GITLAB_TOKEN", "GIT_TOKEN"}}

// forgeToken is the user's token for a host. On the GitHub App's host it is
// their connection's (Settings, "Connect GitHub"), on a GitLab host with the
// team's application their api grant's ("Connect GitLab"); elsewhere, or
// until they connect, the secret their thread pods get (deprecated where an
// app is). Empty if they have neither.
func (s *Service) forgeToken(ctx context.Context, userID, kind, host string) (forge.Token, error) {
	if kind == forge.GitLab {
		if a := s.gitLabAppFor(ctx, host); a != nil {
			t, err := s.gitLabToken(ctx, s.gl.api, userID, a)
			if err != nil && !errors.Is(err, forge.ErrRevoked) {
				return forge.Token{}, err
			}
			if t.Access != "" {
				return forge.Token{Value: t.Access, Fix: "reconnect GitLab in Settings"}, nil
			}
		}
	}
	if a := s.appFor(ctx, host); a != nil {
		token, err := s.userToken(ctx, userID, a)
		if err != nil && !errors.Is(err, forge.ErrRevoked) {
			return forge.Token{}, err
		}
		if token != "" {
			return forge.Token{Value: token, Fix: "reconnect GitHub in Settings", App: true}, nil
		}
	}
	return s.legacyToken(ctx, userID, kind)
}

// legacyToken is the user's token for the forge kind from their secrets.
func (s *Service) legacyToken(ctx context.Context, userID, kind string) (forge.Token, error) {
	key := userID + " " + kind
	s.tokens.mu.Lock()
	c, ok := s.tokens.m[key]
	s.tokens.mu.Unlock()
	if !ok || time.Since(c.at) >= tokenTTL {
		c = cachedToken{at: time.Now()}
		for _, name := range tokenNames[kind] {
			v, err := s.sandboxes.Env(ctx, userID, name)
			if err != nil {
				return forge.Token{}, err
			}
			if v != "" {
				c.value, c.name = v, name
				break
			}
		}
		s.tokens.mu.Lock()
		s.tokens.m[key] = c
		s.tokens.mu.Unlock()
	}
	if c.value == "" {
		return forge.Token{}, nil
	}
	return forge.Token{Value: c.value, Fix: "check " + c.name + " in Settings"}, nil
}

// tokensChanged forgets cached tokens after a secret changed: the user's, or everyone's (empty).
func (s *Service) tokensChanged(userID string) {
	s.tokens.mu.Lock()
	defer s.tokens.mu.Unlock()
	for key := range s.tokens.m {
		if userID == "" || strings.HasPrefix(key, userID+" ") {
			delete(s.tokens.m, key)
		}
	}
}

var errNoForge = connect.NewError(connect.CodeFailedPrecondition,
	"Banda doesn't know this repository's forge: it works with GitHub and GitLab, and FORGE_HOSTS names self-hosted ones")

// openForge opens the thread's repository on its forge with the user's token.
func (s *Service) openForge(ctx context.Context, t store.Thread, userID string) (forge.Forge, error) {
	repo, ok := Forges.Repo(t.RepoURL)
	if !ok {
		return nil, errNoForge
	}
	token, err := s.forgeToken(ctx, userID, repo.Kind, repo.Host)
	if err != nil {
		return nil, forgeError(err)
	}
	if token.Value == "" {
		return nil, s.noToken(ctx, userID, repo)
	}
	return Forges.Open(repo, token)
}

// noToken says what the user, or for someone else the owner, needs to do
// before Banda can act on the repository's forge. Messages for the caller
// name the action in Settings, which the web client turns into a button.
func (s *Service) noToken(ctx context.Context, userID string, repo forge.Repo) error {
	me := userID == user(ctx)
	if s.appFor(ctx, repo.Host) != nil || repo.Kind == forge.GitLab && s.gitLabAppFor(ctx, repo.Host) != nil {
		name := map[string]string{forge.GitHub: "GitHub", forge.GitLab: "GitLab"}[repo.Kind]
		c, err := s.store.ForgeConnection(ctx, userID, repo.Host)
		if err != nil {
			return err
		}
		switch {
		case c != nil && c.Broken && me:
			return refuse("Reconnect " + name + " in Settings: " + name + " no longer accepts your connection")
		case c != nil && c.Broken:
			return refuse("The owner's " + name + " connection no longer works: they need to reconnect it")
		case me:
			return refuse("Connect " + name + " in Settings to do this on " + repo.Host)
		}
		return refuse("The owner hasn't connected " + name + " yet")
	}
	who := "The owner has"
	if me {
		who = "You have"
	}
	return refuse(fmt.Sprintf("%s no token for %s: add %s in Settings", who, repo.Host, strings.Join(tokenNames[repo.Kind], " or ")))
}

// forgeErrors are the codes of forge.Error's codes; their messages are the forge's answer, made safe.
var forgeErrors = map[forge.Code]connect.Code{
	forge.Unauthorized: connect.CodeFailedPrecondition, forge.NotFound: connect.CodeNotFound, forge.Refused: connect.CodeFailedPrecondition,
	forge.RateLimited: connect.CodeResourceExhausted, forge.Unavailable: connect.CodeUnavailable,
}

// forgeReady says whether the thread's owner has a token for its forge.
func (s *Service) forgeReady(ctx context.Context, t store.Thread) bool {
	repo, ok := Forges.Repo(t.RepoURL)
	if !ok {
		return false
	}
	token, err := s.forgeToken(ctx, t.OwnerID, repo.Kind, repo.Host)
	if err != nil {
		slog.Error("forge token", "thread", t.ID, "err", err) // e.g. no RBAC for Secrets: the link still works
	}
	return token.Value != ""
}

// changes are what tell the chat (and so the agent) about a pull request,
// between two snapshots of it. A check passing is news only after it failed.
func changes(old, cur *bandav1.PullRequestSummary) []string {
	var out []string
	if old.State != cur.State {
		switch {
		case cur.State == bandav1.PullState_PULL_STATE_MERGED:
			out = append(out, "merged")
		case cur.State == bandav1.PullState_PULL_STATE_CLOSED:
			out = append(out, "closed")
		case old.State == bandav1.PullState_PULL_STATE_CLOSED:
			out = append(out, "reopened")
		case cur.State == bandav1.PullState_PULL_STATE_DRAFT:
			out = append(out, "draft")
		default:
			out = append(out, "ready")
		}
	}
	if old.Review != cur.Review {
		switch cur.Review {
		case bandav1.ReviewState_REVIEW_STATE_APPROVED:
			out = append(out, "approved")
		case bandav1.ReviewState_REVIEW_STATE_CHANGES_REQUESTED:
			out = append(out, "changes_requested")
		}
	}
	if old.Checks != cur.Checks {
		switch {
		case cur.Checks == bandav1.ChecksState_CHECKS_STATE_FAILING:
			out = append(out, "checks_failed")
		case cur.Checks == bandav1.ChecksState_CHECKS_STATE_PASSING && old.Checks == bandav1.ChecksState_CHECKS_STATE_FAILING:
			out = append(out, "checks_passed")
		}
	}
	return out
}

// pullRequestLine describes a change for the agent and transcripts.
func pullRequestLine(c *bandav1.PullRequestChanged) string {
	p := c.PullRequest
	what := map[string]string{"opened": "was opened", "linked": "was linked to this thread", "ready": "is ready for review",
		"draft": "is a draft again", "checks_failed": "has failing checks", "checks_passed": "passes its checks again",
		"approved": "was approved", "changes_requested": "has changes requested", "merged": "was merged", "closed": "was closed",
		"reopened": "was reopened"}[c.Change]
	return fmt.Sprintf("[Pull request #%d %q %s: %s]", p.Number, p.Title, what, p.Url)
}

// storePullRequest stores a fetched pull request and tells the thread: a
// line per change (by actor), else an ephemeral refresh if anything changed.
func (s *Service) storePullRequest(ctx context.Context, t store.Thread, old, cur *bandav1.PullRequest, actor string, first string) error {
	cur.FetchedAt = timestamppb.Now()
	if err := s.store.PutPullRequest(ctx, t.ID, cur); err != nil {
		return err
	}
	var what []string
	if first != "" {
		what = []string{first}
	} else if old != nil {
		what = changes(old.Summary, cur.Summary)
	}
	if len(what) > 0 {
		_, events, err := s.store.Update(ctx, t.ID, func(*store.Store, *store.Thread) ([]*bandav1.Event, error) {
			var out []*bandav1.Event
			for _, c := range what {
				out = append(out, &bandav1.Event{Actor: actor, Kind: &bandav1.Event_PullRequestChanged{
					PullRequestChanged: &bandav1.PullRequestChanged{PullRequest: cur.Summary, Change: c}}})
			}
			return out, nil
		})
		if err != nil {
			return err
		}
		s.broadcast(t.ID, events)
		return nil
	}
	if old != nil && equalIgnoringFetch(old, cur) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcastLocked(t.ID, []*bandav1.Event{{ThreadId: t.ID, Actor: "system", Kind: &bandav1.Event_PullRequestChanged{
		PullRequestChanged: &bandav1.PullRequestChanged{PullRequest: cur.Summary}}}})
	for w := range s.inbox { // lists show the summary
		if w.team || w.ids[t.ID] {
			w.wake()
		}
	}
	return nil
}

func equalIgnoringFetch(a, b *bandav1.PullRequest) bool {
	a, b = proto.CloneOf(a), proto.CloneOf(b)
	a.FetchedAt, b.FetchedAt = nil, nil
	return proto.Equal(a, b)
}

// refresh fetches the thread's pull request with the owner's token and
// stores it; changes are told as by actor. A failure is kept with the last
// snapshot, so everyone sees why it is stale.
func (s *Service) refresh(ctx context.Context, t store.Thread, old *bandav1.PullRequest, actor string) (*bandav1.PullRequest, error) {
	f, err := s.openForge(ctx, t, t.OwnerID)
	var cur *bandav1.PullRequest
	if err == nil {
		cur, err = f.Get(ctx, int(old.Summary.Number))
	}
	if err != nil {
		stale := proto.CloneOf(old)
		var ce *connect.Error
		errors.As(publicError(ctx, "forge", forgeError(err)), &ce)
		stale.Error = ce.Message()
		if serr := s.storePullRequest(ctx, t, old, stale, actor, ""); serr != nil {
			return nil, serr
		}
		return stale, err
	}
	return cur, s.storePullRequest(ctx, t, old, cur, actor, "")
}

// forgeError turns a forge.Error into what clients see.
func forgeError(err error) error {
	var fe *forge.Error
	if errors.As(err, &fe) {
		return connect.NewError(forgeErrors[fe.Code], fe.Msg)
	}
	return err
}

func (s *Service) PullRequestGet(ctx context.Context, req *bandav1.PullRequestGetRequest) (*bandav1.PullRequestGetResponse, error) {
	t, err := s.store.Get(ctx, req.ThreadId)
	if err != nil {
		return nil, err
	}
	pr, err := s.store.PullRequest(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	if pr != nil && req.Refresh {
		pr, _ = s.refresh(ctx, t, pr, "system") // a failure is in pr.Error
	}
	res := &bandav1.PullRequestGetResponse{PullRequest: pr}
	if repo, ok := Forges.Repo(t.RepoURL); ok {
		token, err := s.forgeToken(ctx, user(ctx), repo.Kind, repo.Host)
		if err != nil {
			return nil, forgeError(err)
		}
		res.CanComment = token.Value != ""
	}
	return res, nil
}

// ownerForge returns the caller's thread, its pull request (nil if none
// is linked) and its forge with the caller's token.
func (s *Service) ownerForge(ctx context.Context, id string) (store.Thread, *bandav1.PullRequest, forge.Forge, error) {
	t, err := s.owned(ctx, id)
	if err != nil {
		return t, nil, nil, err
	}
	if t.RepoURL == "" {
		return t, nil, nil, connect.NewError(connect.CodeFailedPrecondition, "this thread has no repository")
	}
	pr, err := s.store.PullRequest(ctx, t.ID)
	if err != nil {
		return t, nil, nil, err
	}
	f, err := s.openForge(ctx, t, t.OwnerID)
	return t, pr, f, err
}

// link fetches pull request n and links it to the thread, telling the chat.
func (s *Service) link(ctx context.Context, t store.Thread, f forge.Forge, n int, change string) (*bandav1.PullRequest, error) {
	pr, err := f.Get(ctx, n)
	if err != nil {
		return nil, forgeError(err)
	}
	return pr, s.storePullRequest(ctx, t, nil, pr, user(ctx), change)
}

func alreadyLinked(pr *bandav1.PullRequest) error {
	return connect.NewError(connect.CodeAlreadyExists, fmt.Sprintf("this thread already has pull request #%d", pr.Summary.Number))
}

func (s *Service) PullRequestCreate(ctx context.Context, req *bandav1.PullRequestCreateRequest) (*bandav1.PullRequestCreateResponse, error) {
	if strings.TrimSpace(req.Title) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "a title is required")
	}
	t, pr, f, err := s.ownerForge(ctx, req.ThreadId)
	if err != nil {
		return nil, err
	}
	if pr != nil && pr.Summary.State != bandav1.PullState_PULL_STATE_CLOSED {
		return nil, alreadyLinked(pr)
	}
	n, err := f.Create(ctx, forge.New{Title: req.Title, Body: req.Body, Head: t.Branch, Base: t.BaseBranch, Draft: req.Draft})
	if err != nil {
		return nil, forgeError(err)
	}
	// The forges open it first, then ask for reviews and add labels; a refusal there leaves it opened and linked.
	var editErr error
	if len(req.Reviewers) > 0 || len(req.Labels) > 0 {
		opened := &bandav1.PullRequest{Summary: &bandav1.PullRequestSummary{Number: int32(n), Title: req.Title}}
		editErr = f.Update(ctx, opened, forge.Edit{Reviewers: req.Reviewers, AddLabels: req.Labels})
	}
	pr, err = s.link(ctx, t, f, n, "opened")
	if err == nil && editErr != nil {
		msg, ce := editErr.Error(), (*connect.Error)(nil)
		if editErr = forgeError(editErr); errors.As(editErr, &ce) {
			msg = ce.Message()
		}
		err = connect.NewError(connect.CodeOf(editErr), fmt.Sprintf("#%d was opened, but reviewers or labels weren't added: %s", n, msg))
	}
	return &bandav1.PullRequestCreateResponse{PullRequest: pr}, err
}

func (s *Service) PullRequestLink(ctx context.Context, req *bandav1.PullRequestLinkRequest) (*bandav1.PullRequestLinkResponse, error) {
	t, pr, f, err := s.ownerForge(ctx, req.ThreadId)
	if err != nil {
		return nil, err
	}
	if pr != nil && pr.Summary.State != bandav1.PullState_PULL_STATE_CLOSED && pr.Summary.Number != req.Number {
		return nil, alreadyLinked(pr)
	}
	n := int(req.Number)
	if n == 0 {
		if n, err = f.Find(ctx, t.Branch); err != nil {
			return nil, forgeError(err)
		}
		if n == 0 {
			return nil, connect.NewError(connect.CodeNotFound, "no open pull request has "+t.Branch+" as its branch")
		}
	}
	pr, err = s.link(ctx, t, f, n, "linked")
	return &bandav1.PullRequestLinkResponse{PullRequest: pr}, err
}

// linked returns the caller's thread with its pull request and forge, refusing a thread without one.
func (s *Service) linked(ctx context.Context, id string) (store.Thread, *bandav1.PullRequest, forge.Forge, error) {
	t, pr, f, err := s.ownerForge(ctx, id)
	if err == nil && pr == nil {
		err = connect.NewError(connect.CodeFailedPrecondition, "this thread has no pull request")
	}
	return t, pr, f, err
}

func (s *Service) PullRequestUpdate(ctx context.Context, req *bandav1.PullRequestUpdateRequest) (*bandav1.PullRequestUpdateResponse, error) {
	t, pr, f, err := s.linked(ctx, req.ThreadId)
	if err != nil {
		return nil, err
	}
	if req.Title != nil && strings.TrimSpace(*req.Title) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "a title is required")
	}
	if err := f.Update(ctx, pr, forge.Edit{Title: req.Title, Body: req.Body, Draft: req.Draft, Reviewers: req.AddReviewers,
		AddLabels: req.AddLabels, RemoveLabels: req.RemoveLabels}); err != nil {
		return nil, forgeError(err)
	}
	pr, err = s.refresh(ctx, t, pr, user(ctx))
	return &bandav1.PullRequestUpdateResponse{PullRequest: pr}, forgeError(err)
}

func (s *Service) PullRequestMerge(ctx context.Context, req *bandav1.PullRequestMergeRequest) (*bandav1.PullRequestMergeResponse, error) {
	t, pr, f, err := s.linked(ctx, req.ThreadId)
	if err != nil {
		return nil, err
	}
	if err := f.Merge(ctx, int(pr.Summary.Number), req.Method); err != nil {
		return nil, forgeError(err)
	}
	pr, err = s.refresh(ctx, t, pr, user(ctx))
	return &bandav1.PullRequestMergeResponse{PullRequest: pr}, forgeError(err)
}

func (s *Service) PullRequestComment(ctx context.Context, req *bandav1.PullRequestCommentRequest) (*bandav1.PullRequestCommentResponse, error) {
	if strings.TrimSpace(req.Body) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "the comment is empty")
	}
	pr, err := s.asCaller(ctx, req.ThreadId, func(f forge.Forge, n int) error { return f.Comment(ctx, n, req.Body, req.ReplyTo) })
	return &bandav1.PullRequestCommentResponse{PullRequest: pr}, err
}

func (s *Service) PullRequestResolve(ctx context.Context, req *bandav1.PullRequestResolveRequest) (*bandav1.PullRequestResolveResponse, error) {
	if req.ReviewThread == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "which review thread?")
	}
	pr, err := s.asCaller(ctx, req.ThreadId, func(f forge.Forge, n int) error { return f.Resolve(ctx, n, req.ReviewThread, req.Resolved) })
	return &bandav1.PullRequestResolveResponse{PullRequest: pr}, err
}

// asCaller does something on the thread's pull request with the caller's own
// token, so it is theirs on the forge (comments, resolving), and refreshes it.
func (s *Service) asCaller(ctx context.Context, id string, do func(f forge.Forge, number int) error) (*bandav1.PullRequest, error) {
	t, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	pr, err := s.store.PullRequest(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	if pr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, "this thread has no pull request")
	}
	f, err := s.openForge(ctx, t, user(ctx))
	if err != nil {
		return nil, err
	}
	if err := do(f, int(pr.Summary.Number)); err != nil {
		return nil, forgeError(err)
	}
	pr, _ = s.refresh(ctx, t, pr, "system") // a failure is in pr.Error; it is done
	return pr, nil
}

func (s *Service) PullRequestChoices(ctx context.Context, req *bandav1.PullRequestChoicesRequest) (*bandav1.PullRequestChoicesResponse, error) {
	_, _, f, err := s.ownerForge(ctx, req.ThreadId)
	if err != nil {
		return nil, err
	}
	res, err := f.Choices(ctx)
	return res, forgeError(err)
}

var spotting sync.Map // thread ids

// spot links the open pull request of the thread's branch when the thread has
// none: one the agent opened itself (gh, a GitLab push option) or someone on the forge. It
// says so like a link, by nobody. Quiet when the forge or the owner's token is
// unknown.
// ponytail: asks the forge at every turn's end and push while nothing is linked; a webhook would say when.
func (s *Service) spot(ctx context.Context, id string) {
	if _, busy := spotting.LoadOrStore(id, true); busy { // a turn's end and its push come together
		return
	}
	defer spotting.Delete(id)
	t, err := s.store.Get(ctx, id)
	if err != nil || t.RepoURL == "" || t.Branch == "" || !s.forgeReady(ctx, t) {
		return
	}
	if pr, err := s.store.PullRequest(ctx, id); err != nil || pr != nil {
		return
	}
	f, err := s.openForge(ctx, t, t.OwnerID)
	var n int
	if err == nil {
		n, err = f.Find(ctx, t.Branch)
	}
	var pr *bandav1.PullRequest
	if err == nil && n != 0 {
		pr, err = f.Get(ctx, n)
	}
	if err == nil && pr != nil {
		if cur, _ := s.store.PullRequest(ctx, id); cur == nil { // the owner may have linked one meanwhile
			err = s.storePullRequest(ctx, t, nil, pr, "system", "linked")
		}
	}
	if err != nil {
		slog.Info("spot pull request", "thread", id, "err", err)
	}
}

// ListForgeRepos asks every known forge the caller has a token for, at once;
// one that fails or takes over 10 s is left out (the URL field still works).
// Without a query, the 100 most recently pushed per forge, filtered in the browser; with one, the forges search.
// On the GitHub App's host it lists what the caller reaches through the app,
// and says so if they haven't connected.
func (s *Service) ListForgeRepos(ctx context.Context, req *bandav1.ListForgeReposRequest) (*bandav1.ListForgeReposResponse, error) {
	query := strings.TrimSpace(req.Query)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	found := map[string][]*bandav1.ForgeRepo{}
	res := &bandav1.ListForgeReposResponse{}
	for host, kind := range Forges.Known() {
		token, err := s.forgeToken(ctx, user(ctx), kind, host)
		if err != nil {
			slog.Info("forge token", "host", host, "user", user(ctx), "err", err)
			continue
		}
		a := s.appFor(ctx, host)
		if token.Value == "" {
			// ponytail: one host to connect is offered; a team rarely has two unconnected forges with apps.
			if a != nil || kind == forge.GitLab && s.gitLabAppFor(ctx, host) != nil {
				res.ConnectHost, res.ConnectForge = host, kind
			}
			continue
		}
		wg.Go(func() {
			var repos []*bandav1.ForgeRepo
			var err error
			if token.App {
				repos, err = a.Repos(ctx, token.Value, query)
			} else {
				repos, err = Forges.Repos(ctx, kind, host, token, query)
			}
			var fe *forge.Error
			if errors.As(err, &fe) && fe.Code == forge.Unauthorized && token.App { // they revoked Banda on GitHub
				s.connectionRefused(ctx, user(ctx), host)
			}
			if err != nil {
				slog.Info("list forge repos", "host", host, "user", user(ctx), "err", err)
			}
			mu.Lock()
			found[host] = repos
			mu.Unlock()
		})
	}
	wg.Wait()
	for _, host := range slices.Sorted(maps.Keys(found)) {
		res.Repos = append(res.Repos, found[host]...)
	}
	return res, nil
}

// PollPullRequests refreshes linked pull requests from their forges: those
// someone has open every `watched`, other open ones every 10×, closed ones
// only while watched, merged ones never. An owner whose forge reported its
// rate limit used up waits 5 minutes.
// ponytail: polls one at a time from one API; webhooks would make it instant, LISTEN/NOTIFY several APIs.
func (s *Service) PollPullRequests(ctx context.Context, watched time.Duration) {
	tick := time.NewTicker(max(watched/3, time.Second))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		due, err := s.store.PullRequestsToPoll(ctx)
		if err != nil {
			slog.Error("pull requests to poll", "err", err)
			continue
		}
		for _, p := range due {
			s.mu.Lock()
			seen := len(s.present[p.ThreadID]) > 0
			s.mu.Unlock()
			every := watched
			if !seen {
				every = 10 * watched
			}
			if time.Since(p.PolledAt) < every || (!seen && p.State == bandav1.PullState_PULL_STATE_CLOSED) {
				continue
			}
			s.poll(ctx, p.ThreadID)
		}
	}
}

func (s *Service) poll(ctx context.Context, id string) {
	t, err := s.store.Get(ctx, id)
	if err == nil {
		s.tokens.mu.Lock()
		wait := time.Now().Before(s.tokens.backoff[t.OwnerID])
		s.tokens.mu.Unlock()
		if wait {
			return
		}
	}
	var pr *bandav1.PullRequest
	if err == nil {
		pr, err = s.store.PullRequest(ctx, id)
	}
	if err != nil || pr == nil {
		if err != nil {
			slog.Error("poll pull request", "thread", id, "err", err)
		}
		return
	}
	_, err = s.refresh(ctx, t, pr, "system")
	var fe *forge.Error
	if errors.As(err, &fe) && fe.Code == forge.RateLimited {
		s.tokens.mu.Lock()
		s.tokens.backoff[t.OwnerID] = time.Now().Add(5 * time.Minute)
		s.tokens.mu.Unlock()
	}
}
