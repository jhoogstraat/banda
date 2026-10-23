package forge

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v93/github"
	"google.golang.org/protobuf/types/known/timestamppb"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

type gitHub struct {
	c           *github.Client
	owner, name string
	graphql     string // GraphQL endpoint: drafts can't be toggled over REST
	fix         string // see Token
}

func newGitHub(r Repo, base string, token Token, refused func()) (Forge, error) {
	c, err := gitHubClient(base, token.Value, github.WithTransport(newETags(token.Value, refused)))
	if err != nil {
		return nil, err
	}
	graphql := "https://api.github.com/graphql"
	if base != "https://github.com" {
		graphql = base + "/api/graphql"
	}
	owner, name, _ := strings.Cut(r.Path, "/")
	return &gitHub{c: c, owner: owner, name: name, graphql: graphql, fix: token.Fix}, nil
}

// gitHubClient is a client for the GitHub at base (https://<host>) with a
// bearer token, if any: github.com's API is api.github.com, GitHub Enterprise
// Server's (or a test's fake) is at <base>/api/v3/.
func gitHubClient(base, token string, opts ...github.ClientOptionsFunc) (*github.Client, error) {
	opts = append(opts, github.WithTimeout(30*time.Second))
	if token != "" {
		opts = append(opts, github.WithAuthToken(token))
	}
	if base != "https://github.com" {
		opts = append(opts, github.WithEnterpriseURLs(base+"/api/v3/", base+"/api/uploads/"))
	}
	return github.NewClient(opts...)
}

func (g *gitHub) fail(err error) error { return ghError(err, g.fix) }

// ghError maps go-github's errors to Error; fix says what to do about a refused token.
func ghError(err error, fix string) error {
	if err == nil {
		return nil
	}
	var rl *github.RateLimitError
	var abuse *github.AbuseRateLimitError
	var er *github.ErrorResponse
	switch {
	case errors.As(err, &rl), errors.As(err, &abuse):
		return &Error{RateLimited, "GitHub's rate limit is used up; Banda tries again later"}
	case errors.As(err, &er) && er.Response != nil:
		msg := er.Message
		for _, e := range er.Errors {
			msg = strings.TrimSuffix(msg+"; "+cmp.Or(e.Message, e.Code), "; ")
		}
		return fromStatus("GitHub", fix, er.Response.StatusCode, msg)
	}
	return unreachable("GitHub", err)
}

func (g *gitHub) Find(ctx context.Context, branch string) (int, error) {
	prs, _, err := g.c.PullRequests.List(ctx, g.owner, g.name, &github.PullRequestListOptions{State: "open", Head: g.owner + ":" + branch})
	if err != nil || len(prs) == 0 {
		return 0, g.fail(err)
	}
	return prs[0].GetNumber(), nil
}

func (g *gitHub) Create(ctx context.Context, n New) (int, error) {
	pr, _, err := g.c.PullRequests.Create(ctx, g.owner, g.name, github.CreatePullRequest{
		Title: &n.Title, Body: &n.Body, Head: n.Head, Base: n.Base, Draft: &n.Draft})
	return pr.GetNumber(), g.fail(err)
}

func ts(t *github.Timestamp) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(t.Time)
}

func (g *gitHub) Get(ctx context.Context, number int) (*bandav1.PullRequest, error) {
	pr, _, err := g.c.PullRequests.Get(ctx, g.owner, g.name, number)
	if err != nil {
		return nil, g.fail(err)
	}
	out := &bandav1.PullRequest{
		Summary: &bandav1.PullRequestSummary{Forge: GitHub, Number: int32(number), Url: pr.GetHTMLURL(), Title: pr.GetTitle(),
			State: bandav1.PullState_PULL_STATE_OPEN},
		Body: pr.GetBody(), Author: pr.GetUser().GetLogin(), HeadBranch: pr.GetHead().GetRef(), BaseBranch: pr.GetBase().GetRef(),
		Additions: int32(pr.GetAdditions()), Deletions: int32(pr.GetDeletions()), ChangedFiles: int32(pr.GetChangedFiles()),
		CreatedAt: ts(pr.CreatedAt), MergedAt: ts(pr.MergedAt), MergedBy: pr.GetMergedBy().GetLogin(),
		HeadSha: pr.GetHead().GetSHA(), NodeId: pr.GetNodeID(),
	}
	s := out.Summary
	switch {
	case pr.GetMerged() || pr.MergedAt != nil:
		s.State = bandav1.PullState_PULL_STATE_MERGED
	case pr.GetState() == "closed":
		s.State = bandav1.PullState_PULL_STATE_CLOSED
	case pr.GetDraft():
		s.State = bandav1.PullState_PULL_STATE_DRAFT
	}
	for _, l := range pr.Labels {
		out.Labels = append(out.Labels, l.GetName())
	}
	if s.State == bandav1.PullState_PULL_STATE_OPEN || s.State == bandav1.PullState_PULL_STATE_DRAFT {
		switch pr.GetMergeableState() {
		case "dirty":
			s.Mergeable = bandav1.Mergeable_MERGEABLE_CONFLICTS
		case "clean", "unstable", "has_hooks":
			s.Mergeable = bandav1.Mergeable_MERGEABLE_CLEAN
		case "blocked":
			s.Mergeable, out.MergeDetail = bandav1.Mergeable_MERGEABLE_BLOCKED, "Blocked by the branch's rules (required checks or reviews)"
		case "behind":
			s.Mergeable, out.MergeDetail = bandav1.Mergeable_MERGEABLE_BLOCKED, "Behind "+out.BaseBranch+": the branch must be updated first"
		case "draft":
			s.Mergeable, out.MergeDetail = bandav1.Mergeable_MERGEABLE_BLOCKED, "A draft: mark it ready for review first"
		}
	}
	if err := g.checks(ctx, out); err != nil {
		return nil, err
	}
	if err := g.conversation(ctx, number, pr, out); err != nil {
		return nil, err
	}
	repo, _, err := g.c.Repositories.Get(ctx, g.owner, g.name)
	if err != nil {
		return nil, g.fail(err)
	}
	for method, allowed := range map[string]*bool{"merge": repo.AllowMergeCommit, "squash": repo.AllowSquashMerge, "rebase": repo.AllowRebaseMerge} {
		if allowed == nil || *allowed { // not shown without push access: offer it, GitHub decides
			out.MergeMethods = append(out.MergeMethods, method)
		}
	}
	slices.SortFunc(out.MergeMethods, func(a, b string) int { return cmp.Compare(methodOrder[a], methodOrder[b]) })
	return out, nil
}

var methodOrder = map[string]int{"squash": 0, "merge": 1, "rebase": 2}

// checks adds the check runs and commit statuses of the head.
// ponytail: the first 100 of each; enough for any CI we know.
func (g *gitHub) checks(ctx context.Context, out *bandav1.PullRequest) error {
	runs, _, err := g.c.Checks.ListCheckRunsForRef(ctx, g.owner, g.name, out.HeadSha, &github.ListCheckRunsOptions{ListOptions: github.ListOptions{PerPage: 100}})
	if err != nil {
		return g.fail(err)
	}
	for _, r := range runs.CheckRuns {
		c := &bandav1.PullRequestCheck{Name: r.GetName(), Status: r.GetStatus(), Url: cmp.Or(r.GetDetailsURL(), r.GetHTMLURL()),
			StartedAt: ts(r.StartedAt), CompletedAt: ts(r.CompletedAt), State: bandav1.ChecksState_CHECKS_STATE_PENDING}
		if r.GetStatus() == "completed" {
			c.Status = r.GetConclusion()
			switch r.GetConclusion() {
			case "success":
				c.State = bandav1.ChecksState_CHECKS_STATE_PASSING
			case "failure", "timed_out", "action_required", "startup_failure":
				c.State = bandav1.ChecksState_CHECKS_STATE_FAILING
			default: // cancelled, skipped, neutral, stale
				c.State = bandav1.ChecksState_CHECKS_STATE_SKIPPED
			}
		}
		out.Checks = append(out.Checks, c)
	}
	status, _, err := g.c.Repositories.GetCombinedStatus(ctx, g.owner, g.name, out.HeadSha, &github.ListOptions{PerPage: 100})
	if err != nil {
		return g.fail(err)
	}
	for _, st := range status.Statuses {
		c := &bandav1.PullRequestCheck{Name: st.GetContext(), Status: st.GetState(), Url: st.GetTargetURL(), StartedAt: ts(st.CreatedAt),
			State: map[string]bandav1.ChecksState{"success": bandav1.ChecksState_CHECKS_STATE_PASSING, "failure": bandav1.ChecksState_CHECKS_STATE_FAILING,
				"error": bandav1.ChecksState_CHECKS_STATE_FAILING}[st.GetState()]}
		if c.State == bandav1.ChecksState_CHECKS_STATE_UNSPECIFIED {
			c.State = bandav1.ChecksState_CHECKS_STATE_PENDING
		} else {
			c.CompletedAt = ts(st.UpdatedAt)
		}
		out.Checks = append(out.Checks, c)
	}
	out.Summary.Checks = Rollup(out.Checks)
	return nil
}

var reviewStates = map[string]bandav1.ReviewState{"APPROVED": bandav1.ReviewState_REVIEW_STATE_APPROVED,
	"CHANGES_REQUESTED": bandav1.ReviewState_REVIEW_STATE_CHANGES_REQUESTED, "COMMENTED": bandav1.ReviewState_REVIEW_STATE_COMMENTED}

// conversation adds the reviewers, the comments, the reviews and the review comments, oldest first.
// ponytail: the first 100 of each kind; a longer conversation shows its start.
func (g *gitHub) conversation(ctx context.Context, number int, pr *github.PullRequest, out *bandav1.PullRequest) error {
	page := &github.ListOptions{PerPage: 100}
	reviews, _, err := g.c.PullRequests.ListReviews(ctx, g.owner, g.name, number, page)
	if err != nil {
		return g.fail(err)
	}
	// A reviewer's verdict is their latest approval or change request (comments don't replace it).
	verdicts := map[string]bandav1.ReviewState{}
	var order []string
	for _, r := range reviews {
		login, state := r.GetUser().GetLogin(), reviewStates[r.GetState()]
		if r.GetState() == "DISMISSED" {
			state = bandav1.ReviewState_REVIEW_STATE_COMMENTED
		}
		if state == bandav1.ReviewState_REVIEW_STATE_UNSPECIFIED { // PENDING: a draft only its author sees
			continue
		}
		if _, seen := verdicts[login]; !seen {
			order = append(order, login)
		}
		if state != bandav1.ReviewState_REVIEW_STATE_COMMENTED || r.GetState() == "DISMISSED" || verdicts[login] == bandav1.ReviewState_REVIEW_STATE_UNSPECIFIED {
			verdicts[login] = state
		}
		if r.GetBody() != "" || state != bandav1.ReviewState_REVIEW_STATE_COMMENTED {
			out.Comments = append(out.Comments, &bandav1.PullRequestComment{Id: "review-" + strconv.FormatInt(r.GetID(), 10), Author: login,
				Body: r.GetBody(), CreatedAt: ts(r.SubmittedAt), Url: r.GetHTMLURL(), Review: state})
		}
	}
	for _, u := range pr.RequestedReviewers { // asked again after reviewing: requested again
		if _, seen := verdicts[u.GetLogin()]; !seen {
			order = append(order, u.GetLogin())
		}
		verdicts[u.GetLogin()] = bandav1.ReviewState_REVIEW_STATE_REQUESTED
	}
	for _, login := range order {
		out.Reviewers = append(out.Reviewers, &bandav1.PullRequestReviewer{Login: login, State: verdicts[login]})
	}
	out.Summary.Review = reviewRollup(out.Reviewers)

	comments, _, err := g.c.Issues.ListComments(ctx, g.owner, g.name, number, &github.IssueListCommentsOptions{ListOptions: *page})
	if err != nil {
		return g.fail(err)
	}
	for _, c := range comments {
		out.Comments = append(out.Comments, &bandav1.PullRequestComment{Id: strconv.FormatInt(c.GetID(), 10), Author: c.GetUser().GetLogin(),
			Body: c.GetBody(), CreatedAt: ts(c.CreatedAt), Url: c.GetHTMLURL()})
	}
	lines, _, err := g.c.PullRequests.ListComments(ctx, g.owner, g.name, number, &github.PullRequestListCommentsOptions{ListOptions: *page})
	if err != nil {
		return g.fail(err)
	}
	var threads map[int64]reviewThread
	if len(lines) > 0 {
		if threads, err = g.reviewThreads(ctx, number); err != nil {
			return err
		}
	}
	for _, c := range lines {
		pc := &bandav1.PullRequestComment{Id: strconv.FormatInt(c.GetID(), 10), Author: c.GetUser().GetLogin(), Body: c.GetBody(),
			CreatedAt: ts(c.CreatedAt), Url: c.GetHTMLURL(), Path: c.GetPath(), Line: int32(c.GetLine()), DiffHunk: c.GetDiffHunk()}
		if c.InReplyTo != nil {
			pc.ReplyTo = strconv.FormatInt(*c.InReplyTo, 10)
		} else if t, ok := threads[c.GetID()]; ok {
			pc.ThreadId, pc.Resolved = t.id, t.resolved
		}
		out.Comments = append(out.Comments, pc)
	}
	slices.SortStableFunc(out.Comments, func(a, b *bandav1.PullRequestComment) int {
		return a.CreatedAt.AsTime().Compare(b.CreatedAt.AsTime())
	})
	return nil
}

func (g *gitHub) Update(ctx context.Context, pr *bandav1.PullRequest, e Edit) error {
	n := int(pr.Summary.Number)
	if e.Title != nil || e.Body != nil {
		if _, _, err := g.c.PullRequests.Edit(ctx, g.owner, g.name, n, &github.PullRequest{Title: e.Title, Body: e.Body}); err != nil {
			return g.fail(err)
		}
	}
	if e.Draft != nil && *e.Draft != (pr.Summary.State == bandav1.PullState_PULL_STATE_DRAFT) {
		mutation := "mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { clientMutationId } }"
		if *e.Draft {
			mutation = "mutation($id: ID!) { convertPullRequestToDraft(input: {pullRequestId: $id}) { clientMutationId } }"
		}
		if err := g.graphQL(ctx, mutation, map[string]any{"id": pr.NodeId}, nil); err != nil {
			return err
		}
	}
	if len(e.Reviewers) > 0 {
		if _, _, err := g.c.PullRequests.RequestReviewers(ctx, g.owner, g.name, n, github.ReviewersRequest{Reviewers: e.Reviewers}); err != nil {
			return g.fail(err)
		}
	}
	if len(e.AddLabels) > 0 {
		if _, _, err := g.c.Issues.AddLabelsToIssue(ctx, g.owner, g.name, n, e.AddLabels); err != nil {
			return g.fail(err)
		}
	}
	for _, l := range e.RemoveLabels {
		if _, err := g.c.Issues.RemoveLabelForIssue(ctx, g.owner, g.name, n, l); err != nil {
			return g.fail(err)
		}
	}
	return nil
}

// ponytail: the first 100 collaborators and labels.
func (g *gitHub) Choices(ctx context.Context) (*bandav1.PullRequestChoicesResponse, error) {
	out := &bandav1.PullRequestChoicesResponse{}
	users, _, err := g.c.Repositories.ListCollaborators(ctx, g.owner, g.name, &github.ListCollaboratorsOptions{ListOptions: github.ListOptions{PerPage: 100}})
	if err != nil {
		return nil, g.fail(err)
	}
	for _, u := range users {
		out.Reviewers = append(out.Reviewers, u.GetLogin())
	}
	labels, _, err := g.c.Issues.ListLabels(ctx, g.owner, g.name, &github.ListOptions{PerPage: 100})
	if err != nil {
		return nil, g.fail(err)
	}
	for _, l := range labels {
		out.Labels = append(out.Labels, l.GetName())
	}
	return out, nil
}

func (g *gitHub) Resolve(ctx context.Context, _ int, thread string, resolved bool) error {
	mutation := "mutation($id: ID!) { unresolveReviewThread(input: {threadId: $id}) { clientMutationId } }"
	if resolved {
		mutation = "mutation($id: ID!) { resolveReviewThread(input: {threadId: $id}) { clientMutationId } }"
	}
	return g.graphQL(ctx, mutation, map[string]any{"id": thread}, nil)
}

// repos lists the user's most recently pushed repositories, or searches theirs
// and those of the owners among the 100 most recently pushed by name.
// ponytail: owners not among those 100 aren't searched; listing the user's
// organizations would need read:org.
func (g *gitHub) repos(ctx context.Context, query string) ([]*github.Repository, error) {
	recent, _, err := g.c.Repositories.ListByAuthenticatedUser(ctx, &github.RepositoryListByAuthenticatedUserOptions{Sort: "pushed", ListOptions: github.ListOptions{PerPage: 100}})
	if err != nil || query == "" {
		return recent, g.fail(err)
	}
	me, _, err := g.c.Users.Get(ctx, "")
	if err != nil {
		return nil, g.fail(err)
	}
	owners := []string{me.GetLogin()}
	for _, r := range recent {
		if o := r.GetOwner().GetLogin(); o != "" && !slices.Contains(owners, o) {
			owners = append(owners, o)
		}
	}
	q := query + " in:name fork:true"
	for _, o := range owners {
		if len(q)+len(o)+6 > 256 { // GitHub's limit for a query
			break
		}
		q += " user:" + o
	}
	res, _, err := g.c.Search.Repositories(ctx, q, &github.SearchOptions{ListOptions: github.ListOptions{PerPage: 100}})
	if err != nil {
		return nil, g.fail(err)
	}
	return res.Repositories, nil
}

// graphQL runs a query or mutation and decodes its data into out (if not
// nil); GraphQL reports failures in the body with a 200.
func (g *gitHub) graphQL(ctx context.Context, query string, vars map[string]any, out any) error {
	req, err := g.c.NewRequest(ctx, http.MethodPost, g.graphql, map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}
	var res struct {
		Data   json.RawMessage            `json:"data"`
		Errors []struct{ Message string } `json:"errors"`
	}
	if _, err := g.c.Do(req, &res); err != nil {
		return g.fail(err)
	}
	if len(res.Errors) > 0 {
		return &Error{Refused, "GitHub: " + res.Errors[0].Message}
	}
	if out != nil && len(res.Data) > 0 {
		return json.Unmarshal(res.Data, out)
	}
	return nil
}

// reviewThreads maps the first comment of each review thread to the thread:
// REST knows neither threads nor whether they are resolved.
// ponytail: the first 100 threads.
func (g *gitHub) reviewThreads(ctx context.Context, number int) (map[int64]reviewThread, error) {
	var data struct {
		Repository struct {
			PullRequest struct {
				ReviewThreads struct {
					Nodes []struct {
						ID         string
						IsResolved bool
						Comments   struct{ Nodes []struct{ DatabaseID int64 } }
					}
				}
			}
		}
	}
	const query = `query($owner: String!, $name: String!, $number: Int!) { repository(owner: $owner, name: $name) { pullRequest(number: $number) {
		reviewThreads(first: 100) { nodes { id isResolved comments(first: 1) { nodes { databaseId } } } } } } }`
	if err := g.graphQL(ctx, query, map[string]any{"owner": g.owner, "name": g.name, "number": number}, &data); err != nil {
		return nil, err
	}
	out := map[int64]reviewThread{}
	for _, t := range data.Repository.PullRequest.ReviewThreads.Nodes {
		if len(t.Comments.Nodes) > 0 {
			out[t.Comments.Nodes[0].DatabaseID] = reviewThread{t.ID, t.IsResolved}
		}
	}
	return out, nil
}

type reviewThread struct {
	id       string
	resolved bool
}

func (g *gitHub) Merge(ctx context.Context, number int, method string) error {
	_, _, err := g.c.PullRequests.Merge(ctx, g.owner, g.name, number, "", &github.PullRequestOptions{MergeMethod: method})
	return g.fail(err)
}

func (g *gitHub) Comment(ctx context.Context, number int, body, replyTo string) error {
	if replyTo == "" {
		_, _, err := g.c.Issues.CreateComment(ctx, g.owner, g.name, number, github.IssueCommentRequest{Body: body})
		return g.fail(err)
	}
	id, err := strconv.ParseInt(replyTo, 10, 64)
	if err != nil {
		return &Error{NotFound, "GitHub has no such review comment"}
	}
	_, _, err = g.c.PullRequests.CreateCommentInReplyTo(ctx, g.owner, g.name, number, body, id)
	return g.fail(err)
}
