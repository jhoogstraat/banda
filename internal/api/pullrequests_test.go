package api

import (
	"slices"
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestPullRequestChanges(t *testing.T) {
	type s = bandav1.PullRequestSummary
	open, draft, merged, closed := bandav1.PullState_PULL_STATE_OPEN, bandav1.PullState_PULL_STATE_DRAFT, bandav1.PullState_PULL_STATE_MERGED, bandav1.PullState_PULL_STATE_CLOSED
	pending, passing, failing := bandav1.ChecksState_CHECKS_STATE_PENDING, bandav1.ChecksState_CHECKS_STATE_PASSING, bandav1.ChecksState_CHECKS_STATE_FAILING
	for _, c := range []struct {
		old, cur *s
		want     []string
	}{
		{&s{State: open}, &s{State: open}, nil},
		{&s{State: draft}, &s{State: open}, []string{"ready"}},
		{&s{State: open}, &s{State: draft}, []string{"draft"}},
		{&s{State: open}, &s{State: merged}, []string{"merged"}},
		{&s{State: open}, &s{State: closed}, []string{"closed"}},
		{&s{State: closed}, &s{State: open}, []string{"reopened"}},
		{&s{State: open, Checks: pending}, &s{State: open, Checks: failing}, []string{"checks_failed"}},
		// Passing is news only after failing: every push runs the checks.
		{&s{State: open, Checks: pending}, &s{State: open, Checks: passing}, nil},
		{&s{State: open, Checks: failing}, &s{State: open, Checks: passing}, []string{"checks_passed"}},
		{&s{State: open, Review: bandav1.ReviewState_REVIEW_STATE_REQUESTED}, &s{State: open, Review: bandav1.ReviewState_REVIEW_STATE_APPROVED}, []string{"approved"}},
		{&s{State: open}, &s{State: open, Review: bandav1.ReviewState_REVIEW_STATE_CHANGES_REQUESTED, Checks: failing}, []string{"changes_requested", "checks_failed"}},
		{&s{State: open}, &s{State: open, Review: bandav1.ReviewState_REVIEW_STATE_COMMENTED}, nil},
	} {
		if got := changes(c.old, c.cur); !slices.Equal(got, c.want) {
			t.Errorf("changes(%v, %v) = %v, want %v", c.old, c.cur, got, c.want)
		}
	}
	line := pullRequestLine(&bandav1.PullRequestChanged{Change: "checks_failed", PullRequest: &s{Number: 3, Title: "Fix", Url: "https://x/3"}})
	if line != `[Pull request #3 "Fix" has failing checks: https://x/3]` {
		t.Errorf("line %q", line)
	}
}
