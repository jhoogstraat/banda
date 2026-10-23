package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"connectrpc.com/connect/v2"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestPushBlocked(t *testing.T) {
	t.Setenv("GIT_TOKEN", "")
	refused := connect.NewError(connect.CodeFailedPrecondition, "connect GitHub in Settings, so Banda can push as you")
	ask := askCredential
	askCredential = func(context.Context) (*bandav1.GitCredentialResponse, error) { return nil, refused }
	t.Cleanup(func() { askCredential = ask })
	const noToken = "an https remote needs a GIT_TOKEN secret"
	for url, want := range map[string]string{
		"https://github.com/o/r.git":   refused.Message(),
		"git@github.com:o/r.git":       noToken,
		"ssh://git@host/o/r.git":       noToken,
		"file:///opt/e2e/setup-bg.git": "",
		"git://e2e-git/push.git":       "",
		"/tmp/origin.git":              "",
	} {
		if got := pushBlocked(context.Background(), url); got != want {
			t.Errorf("%s: got %q, want %q", url, got, want)
		}
	}
	askCredential = func(context.Context) (*bandav1.GitCredentialResponse, error) {
		return &bandav1.GitCredentialResponse{}, nil
	}
	if got := pushBlocked(context.Background(), "https://github.com/o/r.git"); got != "" {
		t.Errorf("https with a credential from the API: %q", got)
	}
	askCredential = func(context.Context) (*bandav1.GitCredentialResponse, error) { return nil, refused }
	t.Setenv("GIT_TOKEN", "x")
	if got := pushBlocked(context.Background(), "https://github.com/o/r.git"); got != "" {
		t.Errorf("https with GIT_TOKEN: %q", got)
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
