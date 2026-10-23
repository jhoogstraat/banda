package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// The remote turned the push down for good, not just this once.
var refused = regexp.MustCompile(`(?i)\b403\b|permission|denied|authentication failed|could not read username|read-only|not allowed`)

// pusher pushes the thread branch and reports where the work lives, when
// that changed.
type pusher struct {
	mu     sync.Mutex
	last   *bandav1.AutoPush
	report func(*bandav1.Event) error
}

// push pushes the branch, reports the result and returns it. Without a
// repository it only answers that the work lives here: the chat needn't say
// so on every suspend.
func (p *pusher) push(ctx context.Context) *bandav1.AutoPush {
	if repoURL == "" {
		return &bandav1.AutoPush{State: bandav1.AutoPushState_AUTO_PUSH_STATE_UNAVAILABLE}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	r := pushBranch(ctx)
	if p.last == nil || p.last.State != r.State || p.last.Detail != r.Detail {
		p.last = r
		p.report(&bandav1.Event{Kind: &bandav1.Event_AutoPush{AutoPush: r}})
	}
	return r
}

func pushBranch(ctx context.Context) *bandav1.AutoPush {
	branch := os.Getenv("BANDA_BRANCH")
	if why := pushBlocked(ctx, os.Getenv("BANDA_REPO_URL")); why != "" {
		return &bandav1.AutoPush{State: bandav1.AutoPushState_AUTO_PUSH_STATE_UNAVAILABLE,
			Detail: "origin can't be pushed to, so this thread's work only lives in its environment: " + why + "."}
	}
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := git("push", "-u", "origin", branch); err != nil {
		state := bandav1.AutoPushState_AUTO_PUSH_STATE_UNPUSHED
		if refused.MatchString(out) {
			state = bandav1.AutoPushState_AUTO_PUSH_STATE_UNAVAILABLE
		}
		return &bandav1.AutoPush{State: state, Detail: "Push failed: " + out}
	}
	if out, _ := git("status", "--porcelain"); out != "" {
		return &bandav1.AutoPush{State: bandav1.AutoPushState_AUTO_PUSH_STATE_UNPUSHED, BranchPushed: true,
			Detail: fmt.Sprintf("Pushed `%s` to origin. Uncommitted changes only live in this environment; ask the agent to commit them.", branch)}
	}
	return &bandav1.AutoPush{State: bandav1.AutoPushState_AUTO_PUSH_STATE_PUSHED, BranchPushed: true, Detail: fmt.Sprintf("Pushed `%s` to origin.", branch)}
}

// manualPush is the Push button: it also reports the result as a notice.
func (p *pusher) manualPush(ctx context.Context, notice func(string) error) {
	r := p.push(ctx)
	text := r.Detail
	if !strings.HasPrefix(text, "Push") {
		text = "Push failed: " + text
	}
	notice(text)
}

// diff returns the branch's changes against its base, untracked files
// included, using a throwaway index so the agent's own index is untouched.
func diff(ctx context.Context) string {
	const max = 1 << 20
	idx := filepath.Join(os.TempDir(), "banda-index")
	var out []byte
	for _, args := range [][]string{{"read-tree", "HEAD"}, {"add", "-A"}, {"diff", "--cached", base}} {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = work
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+idx)
		var err error
		if out, err = cmd.Output(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				return fmt.Sprintf("git %s failed: %s", args[0], ee.Stderr)
			}
			return err.Error()
		}
	}
	if len(out) > max {
		out = append(out[:max], "\n… diff truncated at 1 MiB"...)
	}
	return string(out) // redact makes it valid UTF-8
}
