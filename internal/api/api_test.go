package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
	"github.com/jhoogstraat/banda/internal/thread"
)

// Every teammate sees a thread's repository URL, so it can't carry a token.
// Only plain git URLs: no transports that run commands, no options for git.
func TestCheckRepoURL(t *testing.T) {
	for repo, ok := range map[string]bool{
		"https://github.com/acme/app.git":                    true,
		"git@github.com:acme/app.git":                        true,
		"ssh://git@github.com/acme/app.git":                  true,
		"git://git-daemon/repo":                              true,
		"https://x-access-token:ghp_abc@github.com/acme/app": false,
		"https://ghp_abc@github.com/acme/app":                false,
		"http://user:pw@git.local/app":                       false,
		"file:///opt/e2e/plain.git":                          true,
		"ext::sh -c touch% /tmp/pwned":                       false,
		"--upload-pack=touch /tmp/pwned":                     false,
		"-oProxyCommand=x@host:path":                         false,
		"git@-oProxyCommand=x:path":                          false,
		"git@-oProxyCommand:path":                            false,
		"fd::17":                                             false,
		"https::example.com/a.git":                           false,
		"https:///a.git":                                     false,
		"ftp://example.com/a.git":                            false,
		"https://example.com/a.git\n--upload-pack=x":         false,
		"git@github.com:acme/app.git\n-x":                    false,
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

// Only an environment suspended with all its work on origin, in a thread
// with a repository and no deletion scheduled, goes after the idle time.
func TestIdleDue(t *testing.T) {
	now := time.Now()
	due := store.Thread{Status: thread.Suspended, EnvPushed: true, RepoURL: "git://x/r.git", UpdatedAt: now.Add(-2 * time.Hour)}
	if !idleDue(due, now, time.Hour) {
		t.Error("an idle pushed environment is due")
	}
	scheduled := now.Add(time.Hour)
	for name, change := range map[string]func(*store.Thread){
		"recent":        func(t *store.Thread) { t.UpdatedAt = now.Add(-30 * time.Minute) },
		"unpushed":      func(t *store.Thread) { t.EnvPushed = false },
		"no repository": func(t *store.Thread) { t.RepoURL = "" },
		"running":       func(t *store.Thread) { t.Status = thread.AwaitingInput },
		"scheduled":     func(t *store.Thread) { t.EnvDeleteAt = &scheduled },
	} {
		th := due
		change(&th)
		if idleDue(th, now, time.Hour) {
			t.Errorf("%s: due", name)
		}
	}
	if idleDue(due, now, 0) {
		t.Error("DELETE_IDLE_AFTER=0 deletes nothing")
	}
}
