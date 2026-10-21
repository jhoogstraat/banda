package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

var githubRepo = regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:)([^/]+/[^/]+?)(?:\.git)?/?$`)

// The remote turned the push down for good, not just this once.
var refused = regexp.MustCompile(`(?i)\b403\b|permission|denied|authentication failed|could not read username|read-only|not allowed`)

// canPush: https needs a token (see checkout); there are no SSH keys in the sandbox.
func canPush(repo string) bool {
	if strings.HasPrefix(repo, "git@") {
		return false
	}
	u, err := url.Parse(repo)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "https", "http":
		return os.Getenv("GIT_TOKEN") != ""
	case "ssh":
		return false
	}
	return true // file://, git://, a path
}

// pusher pushes the thread branch and reports where the work lives, when
// that changed.
type pusher struct {
	mu     sync.Mutex
	last   *bandav1.AutoPush
	report func(*bandav1.Event) error
}

// push pushes the branch, reports the result and returns it.
func (p *pusher) push(ctx context.Context) *bandav1.AutoPush {
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
	if !canPush(os.Getenv("BANDA_REPO_URL")) {
		return &bandav1.AutoPush{State: bandav1.AutoPushState_AUTO_PUSH_STATE_UNAVAILABLE,
			Detail: "origin can't be pushed to (an https remote needs a GIT_TOKEN secret), so this thread's work only lives in its environment."}
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
		return &bandav1.AutoPush{State: bandav1.AutoPushState_AUTO_PUSH_STATE_UNPUSHED,
			Detail: fmt.Sprintf("Pushed `%s` to origin. Uncommitted changes only live in this environment; ask the agent to commit them.", branch)}
	}
	return &bandav1.AutoPush{State: bandav1.AutoPushState_AUTO_PUSH_STATE_PUSHED, Detail: fmt.Sprintf("Pushed `%s` to origin.", branch)}
}

// manualPush is the Push button: it also reports the result as a notice,
// with a pull request link for GitHub.
func (p *pusher) manualPush(ctx context.Context, notice func(string) error) {
	r := p.push(ctx)
	text := r.Detail
	if !strings.HasPrefix(text, "Push") {
		text = "Push failed: " + text
	}
	if m := githubRepo.FindStringSubmatch(os.Getenv("BANDA_REPO_URL")); m != nil && strings.HasPrefix(text, "Pushed") {
		branch := os.Getenv("BANDA_BRANCH")
		text += fmt.Sprintf(" [Open a pull request](https://github.com/%s/compare/%s...%s?expand=1)", m[1], os.Getenv("BANDA_BASE_BRANCH"), branch)
	}
	notice(text)
}
