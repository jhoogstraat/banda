package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestSetupOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	work = t.TempDir()
	var got []string
	s := setup{send: func(e *bandav1.Event) error {
		st := e.GetSetupStep()
		got = append(got, st.State.String()+" "+strings.TrimSpace(st.Output))
		return nil
	}}
	step := func(script, command string) error {
		got = nil
		return s.once(context.Background(), "setup", "Run .banda/setup.sh", []byte(script), command)
	}

	if err := step("v1", "echo ran"); err != nil || len(got) != 2 || got[1] != "SETUP_STATE_DONE ran" {
		t.Fatalf("first run: %v %q", err, got)
	}
	if err := step("v1", "echo ran"); err != nil || len(got) != 0 {
		t.Fatalf("unchanged script ran again: %q", got)
	}
	if err := step("v2", "echo boom; exit 3"); err == nil || len(got) != 2 || !strings.HasPrefix(got[1], "SETUP_STATE_FAILED boom") {
		t.Fatalf("failing script: %v %q", err, got)
	}
	if err := step("v2", "echo fixed"); err != nil || len(got) != 2 {
		t.Fatalf("a failed script runs again: %v %q", err, got)
	}
}

func TestSetupReportSkipsQuietly(t *testing.T) {
	n := 0
	s := setup{send: func(*bandav1.Event) error { n++; return nil }}
	s.report("setup", "x", bandav1.SetupState_SETUP_STATE_SKIPPED, "")
	if n != 0 {
		t.Error("a resume reported a skipped step")
	}
	s.fresh = true
	s.report("setup", "x", bandav1.SetupState_SETUP_STATE_SKIPPED, "")
	big := strings.Repeat("x", 2*maxOutput)
	var out string
	s.send = func(e *bandav1.Event) error { out = e.GetSetupStep().Output; return nil }
	s.report("setup", "x", bandav1.SetupState_SETUP_STATE_DONE, big)
	if n != 1 || len(out) > maxOutput+len("…") {
		t.Errorf("n=%d len=%d", n, len(out))
	}
}

func TestRunGroupBackgroundChild(t *testing.T) {
	work = t.TempDir()
	out, err := runGroup(context.Background(), "sleep 30 & echo started")
	if err != nil || strings.TrimSpace(out) != "started" {
		t.Errorf("got %q, %v", out, err)
	}
	if _, err := runGroup(context.Background(), "sleep 30 & exit 2"); err == nil {
		t.Error("a failing script with a background child succeeded")
	}
}

func TestSetupMarkerFailureFailsStep(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	work = t.TempDir()
	os.WriteFile(filepath.Join(home, ".banda"), nil, 0o644) // a file where the marker dir should be
	var last bandav1.SetupState
	s := setup{send: func(e *bandav1.Event) error { last = e.GetSetupStep().State; return nil }}
	if err := s.once(context.Background(), "setup", "x", []byte("v1"), "true"); err == nil || last != bandav1.SetupState_SETUP_STATE_FAILED {
		t.Errorf("got %v, last state %v", err, last)
	}
}

func TestSetupFork(t *testing.T) {
	work = t.TempDir()
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", work, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("one\n"), 0o644)
	git("add", "-A")
	git("commit", "-qm", "base")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@t")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@t")

	var states, notices []string
	s := setup{fresh: true, send: func(e *bandav1.Event) error { states = append(states, e.GetSetupStep().State.String()); return nil }}
	notice := func(text string) error { notices = append(notices, text); return nil }
	patch := func(p string) func(context.Context) (*bandav1.ForkPatchResponse, error) {
		return func(context.Context) (*bandav1.ForkPatchResponse, error) {
			return &bandav1.ForkPatchResponse{Patch: p, Message: "Changes of t1"}, nil
		}
	}

	s.fork(context.Background(), patch(""), notice)
	if len(states) != 0 {
		t.Fatalf("not a fork, but reported %v", states)
	}
	s.fork(context.Background(), patch("diff --git a/b.txt b/b.txt\nnew file mode 100644\n--- /dev/null\n+++ b/b.txt\n@@ -0,0 +1 @@\n+two\n"), notice)
	if strings.Join(states, ",") != "SETUP_STATE_RUNNING,SETUP_STATE_DONE" || !strings.Contains(git("log", "-1", "--format=%s", "--stat"), "Changes of t1") {
		t.Fatalf("patch not committed: %v", states)
	}
	states = nil
	s.fork(context.Background(), patch("diff --git a/a.txt b/a.txt\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-nope\n+three\n"), notice)
	if strings.Join(states, ",") != "SETUP_STATE_RUNNING,SETUP_STATE_SKIPPED" || len(notices) != 1 {
		t.Fatalf("a patch that doesn't apply: %v %q", states, notices)
	}
}

// Without a repository the agent gets an empty folder; only the login is checked.
func TestPrepareWithoutRepo(t *testing.T) {
	repoURL, apiKey, work = "", true, filepath.Join(t.TempDir(), "work")
	t.Cleanup(func() { apiKey = false })
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api-e2e")
	var got []string
	send := func(e *bandav1.Event) error { got = append(got, e.GetSetupStep().Id); return nil }
	for i, want := range []string{"login", ""} { // a resume reports nothing skipped
		got = nil
		if err := prepare(context.Background(), send, nil, nil, nil); err != nil || strings.Join(got, " ") != want {
			t.Fatalf("start %d: %v, steps %q", i, err, got)
		}
		if fi, err := os.Stat(work); err != nil || !fi.IsDir() {
			t.Fatalf("no working folder: %v", err)
		}
	}
}

// A folder that already holds files becomes the checkout without losing one.
func TestCheckoutKeepsFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	origin := t.TempDir()
	git := func(dir string, args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	files := func(dir string, m map[string]string) {
		for p, c := range m {
			os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
			os.WriteFile(filepath.Join(dir, p), []byte(c), 0o644)
		}
	}
	git(origin, "init", "-q", "-b", "main")
	files(origin, map[string]string{"a.txt": "a\n", "b.txt": "b\n", "d/x": "x\n", ".gitignore": "node_modules/\n"})
	git(origin, "add", "-A")
	git(origin, "commit", "-qm", "base")
	work = filepath.Join(t.TempDir(), "work")
	files(work, map[string]string{"a.txt": "mine\n", "new.txt": "new\n", "node_modules/j": "j\n", "d": "a file\n"})
	t.Setenv("BANDA_REPO_URL", origin)
	t.Setenv("BANDA_BASE_BRANCH", "main")
	t.Setenv("BANDA_BRANCH", "banda/t1")

	kept, err := checkout(context.Background())
	if err != nil || kept == "" {
		t.Fatalf("checkout: %q, %v", kept, err)
	}
	for p, want := range map[string]string{"a.txt": "mine\n", "new.txt": "new\n", "node_modules/j": "j\n", "d": "a file\n", "b.txt": "b\n"} {
		if b, _ := os.ReadFile(filepath.Join(work, p)); string(b) != want {
			t.Errorf("%s: %q, want %q", p, b, want)
		}
	}
	if got := git(work, "status", "--porcelain"); got != "M a.txt\n D d/x\n?? d\n?? new.txt" {
		t.Errorf("status:\n%s", got)
	}
	if git(work, "branch", "--show-current") != "banda/t1" || git(work, "rev-parse", "HEAD") != git(origin, "rev-parse", "HEAD") {
		t.Error("not on the thread branch at the base")
	}
	if kept, err := checkout(context.Background()); err != nil || kept != "" { // a resume
		t.Errorf("resume: %q, %v", kept, err)
	}
}
