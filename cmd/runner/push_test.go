package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestCanPush(t *testing.T) {
	t.Setenv("GIT_TOKEN", "")
	for url, want := range map[string]bool{
		"https://github.com/o/r.git":   false,
		"git@github.com:o/r.git":       false,
		"ssh://git@host/o/r.git":       false,
		"file:///opt/e2e/setup-bg.git": true,
		"git://e2e-git/push.git":       true,
		"/tmp/origin.git":              true,
	} {
		if got := canPush(url); got != want {
			t.Errorf("%s: got %v, want %v", url, got, want)
		}
	}
	t.Setenv("GIT_TOKEN", "x")
	if !canPush("https://github.com/o/r.git") {
		t.Error("https with a token")
	}
}

func TestPushBranch(t *testing.T) {
	dir := t.TempDir()
	origin, w := filepath.Join(dir, "origin.git"), filepath.Join(dir, "work")
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q", "--bare", "-b", "main", origin)
	run("clone", "-q", origin, w)
	run("-C", w, "switch", "-q", "-c", "banda/x")
	os.WriteFile(filepath.Join(w, "a"), []byte("a"), 0o644)
	run("-C", w, "add", "a")
	run("-C", w, "commit", "-qm", "a")
	old := work
	t.Cleanup(func() { work = old })
	work = w
	t.Setenv("BANDA_REPO_URL", origin)
	t.Setenv("BANDA_BRANCH", "banda/x")

	if r := pushBranch(context.Background()); r.State != bandav1.AutoPushState_AUTO_PUSH_STATE_PUSHED {
		t.Fatalf("committed work: %v %s", r.State, r.Detail)
	}
	run("-C", origin, "rev-parse", "--verify", "refs/heads/banda/x")
	os.WriteFile(filepath.Join(w, "b"), []byte("b"), 0o644)
	if r := pushBranch(context.Background()); r.State != bandav1.AutoPushState_AUTO_PUSH_STATE_UNPUSHED {
		t.Fatalf("uncommitted file: %v %s", r.State, r.Detail)
	}
	t.Setenv("BANDA_REPO_URL", "https://example.com/o/r.git")
	t.Setenv("GIT_TOKEN", "")
	if r := pushBranch(context.Background()); r.State != bandav1.AutoPushState_AUTO_PUSH_STATE_UNAVAILABLE {
		t.Fatalf("https without a token: %v %s", r.State, r.Detail)
	}
}
