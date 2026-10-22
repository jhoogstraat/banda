// Package forge talks to the forge a thread's repository lives on (GitHub,
// GitLab; hosted or self-hosted) about its pull request (GitLab: merge
// request), with a user's token. The two implementations share Forge and the
// PullRequest message, so the API and the web client know one model.
package forge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

const (
	GitHub = "github"
	GitLab = "gitlab"
)

// Forge is one repository on a forge, as one user (their token) sees it.
type Forge interface {
	// Find returns the number of the open pull request whose head is branch, 0 if none.
	Find(ctx context.Context, branch string) (int, error)
	Create(ctx context.Context, n New) (int, error)
	// Get fetches everything Banda shows: state, checks, reviews, comments.
	Get(ctx context.Context, number int) (*bandav1.PullRequest, error)
	// Update changes what e sets; pr is the latest snapshot.
	Update(ctx context.Context, pr *bandav1.PullRequest, e Edit) error
	Merge(ctx context.Context, number int, method string) error
	// Comment comments on the pull request, or replies in the thread of the
	// review comment replyTo.
	Comment(ctx context.Context, number int, body, replyTo string) error
	// Choices lists who may be asked for a review and the repository's labels.
	Choices(ctx context.Context) (*bandav1.PullRequestChoicesResponse, error)
	// Resolve resolves or reopens the review thread (PullRequestComment.thread_id).
	Resolve(ctx context.Context, number int, thread string, resolved bool) error
}

type New struct {
	Title, Body, Head, Base string
	Draft                   bool
}

type Edit struct {
	Title, Body *string
	Draft       *bool
	Reviewers   []string
	// Labels to add and to remove.
	AddLabels, RemoveLabels []string
}

// Repo is a repository on a known forge.
type Repo struct {
	Kind string // GitHub or GitLab
	Host string
	Path string // owner/name, or GitLab's group/subgroup/name
}

// Config says which hosts are which forge, and where their APIs are.
type Config struct {
	// Hosts maps self-hosted forges' hosts to their kind; github.com and
	// gitlab.com are known.
	Hosts map[string]string
	// TestURL replaces https://<host> for API calls (tests only): GitHub's API
	// is then at TestURL/api/v3/, GitLab's at TestURL/api/v4/.
	TestURL string
}

// ParseHosts reads FORGE_HOSTS: host=github|gitlab, comma-separated.
func ParseHosts(s string) (map[string]string, error) {
	hosts := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		host, kind, ok := strings.Cut(part, "=")
		if !ok || (kind != GitHub && kind != GitLab) || host == "" {
			return nil, fmt.Errorf("%q: want host=github or host=gitlab", part)
		}
		hosts[strings.ToLower(host)] = kind
	}
	return hosts, nil
}

var scpLike = regexp.MustCompile(`^[\w.~-]+@([\w.-]+):(.+)$`)

// Repo finds the forge of a repository URL (https, http, ssh or user@host:path).
func (c Config) Repo(repoURL string) (Repo, bool) {
	var host, path string
	if m := scpLike.FindStringSubmatch(repoURL); m != nil {
		host, path = m[1], m[2]
	} else if u, err := url.Parse(repoURL); err == nil && slices.Contains([]string{"https", "http", "ssh"}, u.Scheme) {
		host, path = u.Hostname(), u.Path
	}
	host = strings.ToLower(host)
	kind := c.Hosts[host]
	if kind == "" {
		kind = map[string]string{"github.com": GitHub, "gitlab.com": GitLab}[host]
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if kind == "" || strings.Count(path, "/") < 1 || (kind == GitHub && strings.Count(path, "/") != 1) {
		return Repo{}, false
	}
	return Repo{Kind: kind, Host: host, Path: path}, true
}

// Open returns the repository's forge as the token's user.
func (c Config) Open(r Repo, token string) (Forge, error) {
	base := "https://" + r.Host
	if c.TestURL != "" {
		base = strings.TrimSuffix(c.TestURL, "/")
	}
	if r.Kind == GitHub {
		return newGitHub(r, base, token)
	}
	return newGitLab(r, base, token)
}

// Known lists every host Banda knows the forge of.
func (c Config) Known() map[string]string {
	hosts := map[string]string{"github.com": GitHub, "gitlab.com": GitLab}
	maps.Copy(hosts, c.Hosts)
	return hosts
}

// Repos lists the token's user's 100 most recently pushed repositories on the
// host, or with a query the first 100 of theirs whose name matches.
func (c Config) Repos(ctx context.Context, kind, host, token, query string) ([]*bandav1.ForgeRepo, error) {
	r := Repo{Kind: kind, Host: host}
	f, err := c.Open(r, token)
	if err != nil {
		return nil, err
	}
	var out []*bandav1.ForgeRepo
	switch f := f.(type) {
	case *gitHub:
		repos, err := f.repos(ctx, query)
		if err != nil {
			return nil, err
		}
		for _, p := range repos {
			out = append(out, &bandav1.ForgeRepo{Url: "https://" + host + "/" + p.GetFullName() + ".git", Name: p.GetFullName(), Description: p.GetDescription(), Forge: kind})
		}
	case *gitLab:
		opt := &gitlab.ListProjectsOptions{Membership: gitlab.Ptr(true), OrderBy: gitlab.Ptr("last_activity_at"), ListOptions: gitlab.ListOptions{PerPage: 100}}
		if query != "" {
			opt.Search = &query
		}
		projects, _, err := f.c.Projects.ListProjects(opt, gitlab.WithContext(ctx))
		if err != nil {
			return nil, glError(err)
		}
		for _, p := range projects {
			out = append(out, &bandav1.ForgeRepo{Url: "https://" + host + "/" + p.PathWithNamespace + ".git", Name: p.PathWithNamespace, Description: p.Description, Forge: kind})
		}
	}
	return out, nil
}

// Error is a forge's answer that clients may see; anything else is internal.
type Error struct {
	Code Code
	Msg  string
}

type Code int

const (
	// The token was refused, or may not do this.
	Unauthorized Code = iota + 1
	NotFound
	// The forge refused the request, e.g. not mergeable, a pull request exists.
	Refused
	RateLimited
	// The forge couldn't be reached or failed.
	Unavailable
)

func (e *Error) Error() string { return e.Msg }

// fromStatus maps an HTTP error status and the forge's message to an Error.
func fromStatus(name string, status int, msg string) error {
	msg = strings.TrimSpace(msg)
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	switch {
	case status == http.StatusUnauthorized:
		return &Error{Unauthorized, name + " refused the token: check GIT_TOKEN in Settings"}
	case status == http.StatusTooManyRequests:
		return &Error{RateLimited, name + "'s rate limit is used up; Banda tries again later"}
	case status == http.StatusForbidden:
		return &Error{Unauthorized, name + " doesn't let this token do that: " + msg}
	case status == http.StatusNotFound:
		return &Error{NotFound, name + " has no such pull request, or the token can't see it"}
	case status >= 400 && status < 500:
		return &Error{Refused, name + ": " + msg}
	}
	return &Error{Unavailable, fmt.Sprintf("%s answered %d", name, status)}
}

// unreachable wraps a transport error, which may name internal addresses, in a plain one.
func unreachable(name string, err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && !errors.Is(err, context.Canceled) {
		return &Error{Unavailable, name + " couldn't be reached"}
	}
	return err
}

// etags makes GETs conditional: a 304 is served from the last 200 for that
// URL and token, and doesn't count against GitHub's rate limit.
// ponytail: one process-wide cache, emptied whole when it reaches 5000 entries.
type etags struct {
	next  http.RoundTripper
	token string // hashed into the key: another token may see something else
}

type cached struct {
	etag string
	body []byte
	head http.Header
}

var etagCache = struct {
	sync.Mutex
	m map[string]cached
}{m: map[string]cached{}}

func newETags(token string) *etags {
	sum := sha256.Sum256([]byte(token))
	return &etags{next: http.DefaultTransport, token: fmt.Sprintf("%x", sum[:8])}
}

func (t *etags) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return t.next.RoundTrip(req)
	}
	key := t.token + " " + req.URL.String()
	etagCache.Lock()
	c, ok := etagCache.m[key]
	etagCache.Unlock()
	if ok {
		req = req.Clone(req.Context())
		req.Header.Set("If-None-Match", c.etag)
	}
	res, err := t.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if ok && res.StatusCode == http.StatusNotModified {
		res.Body.Close()
		h := c.head.Clone()
		for k, v := range res.Header { // fresh rate limit headers
			h[k] = v
		}
		return &http.Response{Status: "200 OK", StatusCode: http.StatusOK, Header: h, Body: io.NopCloser(bytes.NewReader(c.body)),
			ContentLength: int64(len(c.body)), Request: req, Proto: res.Proto, ProtoMajor: res.ProtoMajor, ProtoMinor: res.ProtoMinor}, nil
	}
	if etag := res.Header.Get("ETag"); etag != "" && res.StatusCode == http.StatusOK {
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil {
			return nil, err
		}
		etagCache.Lock()
		if len(etagCache.m) >= 5000 {
			clear(etagCache.m)
		}
		etagCache.m[key] = cached{etag, body, res.Header.Clone()}
		etagCache.Unlock()
		res.Body = io.NopCloser(bytes.NewReader(body))
	}
	return res, nil
}

// Rollup is the state of a set of checks: failing if any fails, else pending
// if any runs, else passing if any ran.
func Rollup(checks []*bandav1.PullRequestCheck) bandav1.ChecksState {
	out := bandav1.ChecksState_CHECKS_STATE_UNSPECIFIED
	for _, c := range checks {
		switch c.State {
		case bandav1.ChecksState_CHECKS_STATE_FAILING:
			return c.State
		case bandav1.ChecksState_CHECKS_STATE_PENDING:
			out = c.State
		case bandav1.ChecksState_CHECKS_STATE_PASSING, bandav1.ChecksState_CHECKS_STATE_SKIPPED:
			if out == bandav1.ChecksState_CHECKS_STATE_UNSPECIFIED {
				out = bandav1.ChecksState_CHECKS_STATE_PASSING
			}
		}
	}
	return out
}

// reviewRollup is the review state of the reviewers: changes requested by
// anyone wins, then an approval, then a pending request, then comments.
func reviewRollup(reviewers []*bandav1.PullRequestReviewer) bandav1.ReviewState {
	rank := map[bandav1.ReviewState]int{bandav1.ReviewState_REVIEW_STATE_COMMENTED: 1, bandav1.ReviewState_REVIEW_STATE_REQUESTED: 2,
		bandav1.ReviewState_REVIEW_STATE_APPROVED: 3, bandav1.ReviewState_REVIEW_STATE_CHANGES_REQUESTED: 4}
	out := bandav1.ReviewState_REVIEW_STATE_UNSPECIFIED
	for _, r := range reviewers {
		if rank[r.State] > rank[out] {
			out = r.State
		}
	}
	return out
}
