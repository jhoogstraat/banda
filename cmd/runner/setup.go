package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

const (
	maxOutput    = 64 << 10
	setupTimeout = 15 * time.Minute
)

// setup reports the steps that prepare the environment as SetupStep events.
type setup struct {
	send  func(*bandav1.Event) error
	fresh bool // first start: also report the steps with nothing to do
}

// prepare runs the setup steps. When it returns an error, the failed step
// has been reported.
func prepare(ctx context.Context, send func(*bandav1.Event) error, notice func(string) error, loginStatus func(bool) error) error {
	_, err := os.Stat(filepath.Join(work, ".git"))
	s := setup{send: send, fresh: err != nil}
	title := "Check out " + os.Getenv("BANDA_BRANCH")
	if s.fresh {
		err = s.run("checkout", title, func() (string, error) { return "", checkout(ctx) })
	} else if err = checkout(ctx); err != nil { // quick on a resume, so only reported if it fails
		s.report("checkout", title, bandav1.SetupState_SETUP_STATE_FAILED, err.Error())
	}
	if err != nil {
		return err
	}

	const tools = "Install tools (mise)"
	var config []byte
	for _, f := range []string{"mise.toml", ".mise.toml", ".tool-versions"} {
		if b, err := os.ReadFile(filepath.Join(work, f)); err == nil {
			config = append(append(config, f+"\n"...), b...)
		}
	}
	if config == nil {
		s.report("tools", tools, bandav1.SetupState_SETUP_STATE_SKIPPED, "No mise.toml, .mise.toml or .tool-versions.")
	} else if err := s.once(ctx, "tools", tools, config, "mise trust --all --yes && mise install --yes"); err != nil {
		return err
	}

	const script = "Run .banda/setup.sh"
	b, err := os.ReadFile(filepath.Join(work, ".banda", "setup.sh"))
	if errors.Is(err, fs.ErrNotExist) {
		s.report("setup", script, bandav1.SetupState_SETUP_STATE_SKIPPED, "No .banda/setup.sh.")
	} else if err != nil {
		s.report("setup", script, bandav1.SetupState_SETUP_STATE_FAILED, err.Error())
		return err
	} else if err := s.once(ctx, "setup", script, b, "bash .banda/setup.sh"); err != nil {
		return err
	}

	if !apiKey {
		return s.waitForLogin(ctx, notice, loginStatus)
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		err := errors.New("API key mode is on but no API key is stored. Set one in Settings, then retry.")
		s.report("login", "Use the API key", bandav1.SetupState_SETUP_STATE_FAILED, err.Error())
		return err
	}
	s.report("login", "Use the API key", bandav1.SetupState_SETUP_STATE_SKIPPED, "Using the API key instead of a subscription login.")
	return nil
}

// once runs a setup command unless it already succeeded for this content
// (a config file, a script). The marker is on the home volume, so this is
// once per thread.
func (s setup) once(ctx context.Context, id, title string, content []byte, command string) error {
	marker := filepath.Join(os.Getenv("HOME"), ".banda", id+".sha256")
	sum := fmt.Sprintf("%x", sha256.Sum256(content))
	if b, _ := os.ReadFile(marker); string(b) == sum {
		return nil
	}
	if err := s.run(id, title, func() (string, error) { return runGroup(ctx, command) }); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(sum), 0o644)
}

// run reports a step as running, runs fn and reports the result.
func (s setup) run(id, title string, fn func() (string, error)) error {
	s.report(id, title, bandav1.SetupState_SETUP_STATE_RUNNING, "")
	out, err := fn()
	if err != nil {
		s.report(id, title, bandav1.SetupState_SETUP_STATE_FAILED, strings.TrimSpace(out+"\n"+err.Error()))
		return fmt.Errorf("%s: %w", title, err)
	}
	s.report(id, title, bandav1.SetupState_SETUP_STATE_DONE, out)
	return nil
}

// report sends a step, its output redacted and cut to the last 64 KiB.
func (s setup) report(id, title string, state bandav1.SetupState, out string) {
	if state == bandav1.SetupState_SETUP_STATE_SKIPPED && !s.fresh {
		return
	}
	if out = redact(out); len(out) > maxOutput { // redact first: a cut could split a secret
		out = "…" + out[len(out)-maxOutput:]
	}
	s.send(&bandav1.Event{Kind: &bandav1.Event_SetupStep{SetupStep: &bandav1.SetupStep{Id: id, Title: title, State: state, Output: out}}})
}

// runGroup runs a bash command in the checkout in its own process group, and
// kills only that group on timeout or shutdown. Claude is never in it.
func runGroup(ctx context.Context, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = work
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second // background processes may keep the output open
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("timed out after %s", setupTimeout)
	}
	return out.String(), err
}
