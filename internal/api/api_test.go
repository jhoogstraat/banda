package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

// Every teammate sees a thread's repository URL, so it can't carry a token.
func TestCheckRepoURL(t *testing.T) {
	for repo, ok := range map[string]bool{
		"https://github.com/acme/app.git":                    true,
		"git@github.com:acme/app.git":                        true,
		"ssh://git@github.com/acme/app.git":                  true,
		"git://git-daemon/repo":                              true,
		"https://x-access-token:ghp_abc@github.com/acme/app": false,
		"https://ghp_abc@github.com/acme/app":                false,
		"http://user:pw@git.local/app":                       false,
	} {
		if err := checkRepoURL(repo); (err == nil) != ok {
			t.Errorf("checkRepoURL(%q) = %v", repo, err)
		}
	}
}

// Clients see known errors with their code and nothing of anything else.
func TestPublicError(t *testing.T) {
	ctx := context.Background()
	for err, want := range map[error]connect.Code{
		fmt.Errorf("get: %w", store.ErrNotFound):               connect.CodeNotFound,
		thread.ErrForbidden:                                    connect.CodePermissionDenied,
		connect.NewError(connect.CodeInvalidArgument, "bad"):   connect.CodeInvalidArgument,
		errors.New(`ERROR: relation "threads" does not exist`): connect.CodeInternal,
		fmt.Errorf("query: %w", context.Canceled):              connect.CodeCanceled,
	} {
		got := publicError(ctx, "/test", err)
		if connect.CodeOf(got) != want || want == connect.CodeInternal && strings.Contains(got.Error(), "relation") {
			t.Errorf("publicError(%v) = %v, want %v", err, got, want)
		}
	}
}

func TestPullRequestURL(t *testing.T) {
	pushed := bandav1.AutoPushState_AUTO_PUSH_STATE_PUSHED.String()
	for repo, want := range map[string]string{
		"https://github.com/o/r":      "https://github.com/o/r/compare/main...banda/x?expand=1",
		"https://github.com/o/r.git":  "https://github.com/o/r/compare/main...banda/x?expand=1",
		"git@github.com:o/r.git":      "https://github.com/o/r/compare/main...banda/x?expand=1",
		"https://github.com/o/r.git/": "https://github.com/o/r/compare/main...banda/x?expand=1",
		"https://gitlab.com/o/r.git":  "",
		"/tmp/origin.git":             "",
	} {
		if got := pullRequestURL(store.Thread{RepoURL: repo, BaseBranch: "main", Branch: "banda/x", AutoPush: pushed}); got != want {
			t.Errorf("%s: got %q, want %q", repo, got, want)
		}
	}
	if got := pullRequestURL(store.Thread{RepoURL: "https://github.com/o/r"}); got != "" {
		t.Errorf("not pushed: got %q", got)
	}
}
