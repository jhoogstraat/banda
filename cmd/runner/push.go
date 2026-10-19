package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

var githubRepo = regexp.MustCompile(`^(?:https://github\.com/|git@github\.com:)([^/]+/[^/]+?)(?:\.git)?/?$`)

// push publishes the thread branch and reports the result as a notice.
func push(ctx context.Context, notice func(string) error) {
	branch := os.Getenv("BANDA_BRANCH")
	cmd := exec.CommandContext(ctx, "git", "push", "-u", "origin", branch)
	cmd.Dir = work
	if out, err := cmd.CombinedOutput(); err != nil {
		notice("Push failed: " + strings.TrimSpace(string(out)))
		return
	}
	text := fmt.Sprintf("Pushed `%s` to origin.", branch)
	if m := githubRepo.FindStringSubmatch(os.Getenv("BANDA_REPO_URL")); m != nil {
		text += fmt.Sprintf(" [Open a pull request](https://github.com/%s/compare/%s...%s?expand=1)", m[1], os.Getenv("BANDA_BASE_BRANCH"), branch)
	}
	status := exec.CommandContext(ctx, "git", "status", "--porcelain")
	status.Dir = work
	if out, _ := status.Output(); len(out) > 0 {
		text += " Uncommitted changes were not pushed; ask the agent to commit them."
	}
	notice(text)
}
