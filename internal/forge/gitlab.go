package forge

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	"google.golang.org/protobuf/types/known/timestamppb"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

type gitLab struct {
	c    *gitlab.Client
	path string // the project's full path, which the client escapes
	fix  string // see Token
}

func newGitLab(r Repo, base string, token Token, refused func()) (Forge, error) {
	c, err := gitLabClient(base, token.Value, refused)
	if err != nil {
		return nil, err
	}
	return &gitLab{c: c, path: r.Path, fix: token.Fix}, nil
}

// gitLabClient is a client for the GitLab at base with a bearer token: an
// OAuth token, or a personal access token, which GitLab takes that way too.
// refused, if set, hears of a 401.
func gitLabClient(base, token string, refused func()) (*gitlab.Client, error) {
	// No retries: a poll that fails is retried by the next poll, a person's action by the person.
	hc := &http.Client{Timeout: 30 * time.Second, Transport: &etags{next: http.DefaultTransport, refused: refused, uncached: true}}
	return gitlab.NewOAuthClient(token, gitlab.WithBaseURL(base+"/api/v4"), gitlab.WithoutRetries(), gitlab.WithHTTPClient(hc))
}

func (f *gitLab) fail(err error) error {
	var er *gitlab.ErrorResponse
	if errors.As(err, &er) && er.Response != nil {
		return fromStatus("GitLab", f.fix, er.Response.StatusCode, er.Message)
	}
	if err == nil {
		return nil
	}
	return unreachable("GitLab", err)
}

func tm(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

func (g *gitLab) Find(ctx context.Context, branch string) (int, error) {
	mrs, _, err := g.c.MergeRequests.ListProjectMergeRequests(g.path, &gitlab.ListProjectMergeRequestsOptions{
		SourceBranch: &branch, State: gitlab.Ptr("opened")}, gitlab.WithContext(ctx))
	if err != nil || len(mrs) == 0 {
		return 0, g.fail(err)
	}
	return int(mrs[0].IID), nil
}

// GitLab marks a draft by its title's prefix.
var draftPrefix = regexp.MustCompile(`(?i)^\s*(\[draft\]|\(draft\)|draft:|draft -|\[wip\]|wip:)\s*`)

func draftTitle(title string, draft bool) string {
	title = draftPrefix.ReplaceAllString(title, "")
	if draft {
		return "Draft: " + title
	}
	return title
}

func (g *gitLab) Create(ctx context.Context, n New) (int, error) {
	mr, _, err := g.c.MergeRequests.CreateMergeRequest(g.path, &gitlab.CreateMergeRequestOptions{
		Title: gitlab.Ptr(draftTitle(n.Title, n.Draft)), Description: &n.Body, SourceBranch: &n.Head, TargetBranch: &n.Base}, gitlab.WithContext(ctx))
	if err != nil {
		return 0, g.fail(err)
	}
	return int(mr.IID), nil
}

// blocked are GitLab's detailed merge statuses that wait on something, in words.
var blocked = map[string]string{
	"not_approved": "Not approved yet", "ci_must_pass": "The pipeline must pass", "ci_still_running": "The pipeline is still running",
	"discussions_not_resolved": "Unresolved discussions", "draft_status": "A draft: mark it ready first", "need_rebase": "Needs a rebase onto the target branch",
	"blocked_status": "Blocked by another merge request", "not_open": "Not open", "jira_association_missing": "Needs a Jira issue",
	"requested_changes": "Changes were requested", "merge_request_blocked": "Blocked by another merge request", "security_policy_violations": "Blocked by a security policy",
}

func (g *gitLab) Get(ctx context.Context, number int) (*bandav1.PullRequest, error) {
	iid := int64(number)
	mr, _, err := g.c.MergeRequests.GetMergeRequest(g.path, iid, nil, gitlab.WithContext(ctx))
	if err != nil {
		return nil, g.fail(err)
	}
	out := &bandav1.PullRequest{
		Summary: &bandav1.PullRequestSummary{Forge: GitLab, Number: int32(number), Url: mr.WebURL, Title: mr.Title,
			State: bandav1.PullState_PULL_STATE_OPEN},
		Body: mr.Description, HeadBranch: mr.SourceBranch, BaseBranch: mr.TargetBranch, Labels: mr.Labels,
		CreatedAt: tm(mr.CreatedAt), MergedAt: tm(mr.MergedAt), HeadSha: mr.SHA, MergeMethods: []string{"merge", "squash"},
	}
	if mr.Author != nil {
		out.Author = mr.Author.Username
	}
	if mr.MergedBy != nil {
		out.MergedBy = mr.MergedBy.Username
	} else if mr.MergeUser != nil {
		out.MergedBy = mr.MergeUser.Username
	}
	if n, err := strconv.Atoi(strings.TrimSuffix(mr.ChangesCount, "+")); err == nil {
		out.ChangedFiles = int32(n)
	}
	s := out.Summary
	switch {
	case mr.State == "merged":
		s.State = bandav1.PullState_PULL_STATE_MERGED
	case mr.State == "closed":
		s.State = bandav1.PullState_PULL_STATE_CLOSED
	case mr.Draft || mr.WorkInProgress:
		s.State = bandav1.PullState_PULL_STATE_DRAFT
	}
	if s.State == bandav1.PullState_PULL_STATE_OPEN || s.State == bandav1.PullState_PULL_STATE_DRAFT {
		switch st := mr.DetailedMergeStatus; {
		case mr.HasConflicts || st == "conflict" || st == "broken_status":
			s.Mergeable = bandav1.Mergeable_MERGEABLE_CONFLICTS
		case st == "mergeable":
			s.Mergeable = bandav1.Mergeable_MERGEABLE_CLEAN
		case blocked[st] != "":
			s.Mergeable, out.MergeDetail = bandav1.Mergeable_MERGEABLE_BLOCKED, blocked[st]
		}
	}
	if mr.HeadPipeline != nil {
		// ponytail: the first 100 jobs of the head pipeline.
		jobs, _, err := g.c.Jobs.ListPipelineJobs(g.path, mr.HeadPipeline.ID, &gitlab.ListJobsOptions{ListOptions: gitlab.ListOptions{PerPage: 100}}, gitlab.WithContext(ctx))
		if err != nil {
			return nil, g.fail(err)
		}
		for i := len(jobs) - 1; i >= 0; i-- { // GitLab lists the newest first
			j := jobs[i]
			c := &bandav1.PullRequestCheck{Name: j.Stage + " / " + j.Name, Status: j.Status, Url: j.WebURL, StartedAt: tm(j.StartedAt), CompletedAt: tm(j.FinishedAt),
				State: bandav1.ChecksState_CHECKS_STATE_PENDING}
			switch j.Status {
			case "success":
				c.State = bandav1.ChecksState_CHECKS_STATE_PASSING
			case "failed":
				c.State = bandav1.ChecksState_CHECKS_STATE_FAILING
				if j.AllowFailure {
					c.State = bandav1.ChecksState_CHECKS_STATE_SKIPPED
				}
			case "canceled", "canceling", "skipped", "manual":
				c.State = bandav1.ChecksState_CHECKS_STATE_SKIPPED
			}
			out.Checks = append(out.Checks, c)
		}
		s.Checks = Rollup(out.Checks)
	}
	approvals, _, err := g.c.MergeRequestApprovals.GetConfiguration(g.path, iid, gitlab.WithContext(ctx))
	if err != nil {
		return nil, g.fail(err)
	}
	approved := map[string]bool{}
	for _, a := range approvals.ApprovedBy {
		if a.User != nil {
			approved[a.User.Username] = true
			out.Reviewers = append(out.Reviewers, &bandav1.PullRequestReviewer{Login: a.User.Username, State: bandav1.ReviewState_REVIEW_STATE_APPROVED})
		}
	}
	for _, r := range mr.Reviewers {
		if !approved[r.Username] {
			out.Reviewers = append(out.Reviewers, &bandav1.PullRequestReviewer{Login: r.Username, State: bandav1.ReviewState_REVIEW_STATE_REQUESTED})
		}
	}
	s.Review = reviewRollup(out.Reviewers)

	// ponytail: the first 100 discussions; a longer conversation shows its start.
	discussions, _, err := g.c.Discussions.ListMergeRequestDiscussions(g.path, iid, &gitlab.ListMergeRequestDiscussionsOptions{ListOptions: gitlab.ListOptions{PerPage: 100}}, gitlab.WithContext(ctx))
	if err != nil {
		return nil, g.fail(err)
	}
	for _, d := range discussions {
		var first string
		for _, n := range d.Notes {
			if n.System {
				continue
			}
			c := &bandav1.PullRequestComment{Id: strconv.FormatInt(n.ID, 10), Author: n.Author.Username, Body: n.Body,
				CreatedAt: tm(n.CreatedAt), Url: mr.WebURL + "#note_" + strconv.FormatInt(n.ID, 10), ReplyTo: first}
			if p := n.Position; p != nil {
				c.Path, c.Line = p.NewPath, int32(p.NewLine)
				if c.Path == "" {
					c.Path = p.OldPath
				}
			}
			if first == "" && !d.IndividualNote {
				first = c.Id
				if n.Resolvable {
					c.ThreadId, c.Resolved = d.ID, n.Resolved
				}
			}
			out.Comments = append(out.Comments, c)
		}
	}
	return out, nil
}

func (g *gitLab) Update(ctx context.Context, pr *bandav1.PullRequest, e Edit) error {
	opt := &gitlab.UpdateMergeRequestOptions{Description: e.Body}
	title := pr.Summary.Title
	if e.Title != nil {
		title, opt.Title = *e.Title, e.Title
	}
	if e.Draft != nil {
		opt.Title = gitlab.Ptr(draftTitle(title, *e.Draft))
	}
	if len(e.Reviewers) > 0 {
		iid := int64(pr.Summary.Number)
		mr, _, err := g.c.MergeRequests.GetMergeRequest(g.path, iid, nil, gitlab.WithContext(ctx))
		if err != nil {
			return g.fail(err)
		}
		var ids []int64
		for _, r := range mr.Reviewers {
			ids = append(ids, r.ID)
		}
		for _, name := range e.Reviewers {
			users, _, err := g.c.Users.ListUsers(&gitlab.ListUsersOptions{Username: &name}, gitlab.WithContext(ctx))
			if err != nil {
				return g.fail(err)
			}
			if len(users) == 0 {
				return &Error{NotFound, "GitLab has no user " + name}
			}
			ids = append(ids, users[0].ID)
		}
		opt.ReviewerIDs = &ids
	}
	if len(e.AddLabels) > 0 {
		opt.AddLabels = (*gitlab.LabelOptions)(&e.AddLabels)
	}
	if len(e.RemoveLabels) > 0 {
		opt.RemoveLabels = (*gitlab.LabelOptions)(&e.RemoveLabels)
	}
	_, _, err := g.c.MergeRequests.UpdateMergeRequest(g.path, int64(pr.Summary.Number), opt, gitlab.WithContext(ctx))
	return g.fail(err)
}

func (g *gitLab) Merge(ctx context.Context, number int, method string) error {
	if method != "merge" && method != "squash" {
		return &Error{Refused, "GitLab merges with merge or squash"}
	}
	_, _, err := g.c.MergeRequests.AcceptMergeRequest(g.path, int64(number), &gitlab.AcceptMergeRequestOptions{Squash: gitlab.Ptr(method == "squash")}, gitlab.WithContext(ctx))
	return g.fail(err)
}

func (g *gitLab) Comment(ctx context.Context, number int, body, replyTo string) error {
	iid := int64(number)
	if replyTo == "" {
		_, _, err := g.c.Discussions.CreateMergeRequestDiscussion(g.path, iid, &gitlab.CreateMergeRequestDiscussionOptions{Body: &body}, gitlab.WithContext(ctx))
		return g.fail(err)
	}
	discussions, _, err := g.c.Discussions.ListMergeRequestDiscussions(g.path, iid, &gitlab.ListMergeRequestDiscussionsOptions{ListOptions: gitlab.ListOptions{PerPage: 100}}, gitlab.WithContext(ctx))
	if err != nil {
		return g.fail(err)
	}
	for _, d := range discussions {
		if len(d.Notes) > 0 && strconv.FormatInt(d.Notes[0].ID, 10) == replyTo {
			_, _, err := g.c.Discussions.AddMergeRequestDiscussionNote(g.path, iid, d.ID, &gitlab.AddMergeRequestDiscussionNoteOptions{Body: &body}, gitlab.WithContext(ctx))
			return g.fail(err)
		}
	}
	return &Error{NotFound, "GitLab has no such discussion"}
}

// ponytail: the first 100 members and labels.
func (g *gitLab) Choices(ctx context.Context) (*bandav1.PullRequestChoicesResponse, error) {
	out := &bandav1.PullRequestChoicesResponse{}
	members, _, err := g.c.ProjectMembers.ListAllProjectMembers(g.path, &gitlab.ListProjectMembersOptions{ListOptions: gitlab.ListOptions{PerPage: 100}}, gitlab.WithContext(ctx))
	if err != nil {
		return nil, g.fail(err)
	}
	for _, m := range members {
		out.Reviewers = append(out.Reviewers, m.Username)
	}
	labels, _, err := g.c.Labels.ListLabels(g.path, &gitlab.ListLabelsOptions{ListOptions: gitlab.ListOptions{PerPage: 100}}, gitlab.WithContext(ctx))
	if err != nil {
		return nil, g.fail(err)
	}
	for _, l := range labels {
		out.Labels = append(out.Labels, l.Name)
	}
	return out, nil
}

func (g *gitLab) Resolve(ctx context.Context, number int, thread string, resolved bool) error {
	_, _, err := g.c.Discussions.ResolveMergeRequestDiscussion(g.path, int64(number), thread,
		&gitlab.ResolveMergeRequestDiscussionOptions{Resolved: &resolved}, gitlab.WithContext(ctx))
	return g.fail(err)
}
