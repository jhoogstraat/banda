package main

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
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
	killGrace    = 30 * time.Second
)

// setup reports the steps that prepare the environment as SetupStep events.
type setup struct {
	send  func(*bandav1.Event) error
	fresh bool // first start: also report the steps with nothing to do
}

// prepare runs the setup steps. When it returns an error, the failed step
// has been reported. Without a repository there is only the login: the agent
// works in an empty folder on the thread's volume.
func prepare(ctx context.Context, send func(*bandav1.Event) error, notice func(string) error, loginStatus func(bool) error, forkPatch func(context.Context) (*bandav1.ForkPatchResponse, error)) error {
	if repoURL == "" {
		_, err := os.Stat(work)
		s := setup{send: send, fresh: err != nil}
		if err := os.MkdirAll(work, 0o755); err != nil {
			s.report("folder", "Create the working folder", bandav1.SetupState_SETUP_STATE_FAILED, err.Error())
			return err
		}
		return s.login(ctx, notice, loginStatus)
	}
	_, err := os.Stat(filepath.Join(work, ".git"))
	s := setup{send: send, fresh: err != nil}
	title := "Check out " + os.Getenv("BANDA_BRANCH")
	if s.fresh {
		var kept string
		err = s.run("checkout", title, func() (string, error) { kept, err = checkout(ctx); return kept, err })
		if err == nil && kept != "" {
			notice(kept)
		}
	} else if _, err = checkout(ctx); err != nil { // quick on a resume, so only reported if it fails
		s.report("checkout", title, bandav1.SetupState_SETUP_STATE_FAILED, err.Error())
	}
	if err != nil {
		return err
	}
	// A recreated environment continues from the pushed branch, which has the fork's changes.
	if s.fresh && exec.CommandContext(ctx, "git", "-C", work, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+os.Getenv("BANDA_BRANCH")).Run() != nil {
		s.fork(ctx, forkPatch, notice)
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
	return s.login(ctx, notice, loginStatus)
}

// login waits for the subscription login, or checks for the API key or the
// team's identity token, which comes with the runner's first fetch.
func (s setup) login(ctx context.Context, notice func(string) error, loginStatus func(bool) error) error {
	if teamToken != "" {
		for range 60 {
			if b, err := os.ReadFile(teamToken); err == nil && len(b) > 0 {
				s.report("login", "Use the team's Console identity", bandav1.SetupState_SETUP_STATE_SKIPPED, "Using the team's Console identity instead of a Claude login.")
				return nil
			}
			select {
			case <-time.After(500 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		s.report("login", "Use the team's Console identity", bandav1.SetupState_SETUP_STATE_FAILED, "No identity token arrived for the team's Console identity. The chat says why if Banda refused one.")
		return errors.New("no team identity token")
	}
	if !apiKey {
		return s.waitForLogin(ctx, notice, loginStatus)
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		s.report("login", "Use the API key", bandav1.SetupState_SETUP_STATE_FAILED, "API key mode is on but no API key is stored. Set one in Settings, then retry.")
		return errors.New("no API key is stored")
	}
	s.report("login", "Use the API key", bandav1.SetupState_SETUP_STATE_SKIPPED, "Using the API key instead of a subscription login.")
	return nil
}

// fork commits the changes a fork starts from, right after its first
// checkout. If they don't apply (the base branch moved on, a binary file, a
// diff cut at 1 MiB) the fork starts from the base branch and says so: a
// failed step would stop the thread.
// ponytail: dying between the checkout and this commit loses the changes; mark it done on the home volume if that bites.
func (s setup) fork(ctx context.Context, forkPatch func(context.Context) (*bandav1.ForkPatchResponse, error), notice func(string) error) {
	const title = "Apply the forked thread's changes"
	p, err := forkPatch(ctx)
	if err == nil && p.Patch == "" {
		return // not a fork
	}
	s.report("fork", title, bandav1.SetupState_SETUP_STATE_RUNNING, "")
	var out []byte
	if err == nil {
		apply := exec.CommandContext(ctx, "git", "apply", "--index", "-")
		apply.Dir, apply.Stdin = work, strings.NewReader(p.Patch)
		if out, err = apply.CombinedOutput(); err == nil {
			commit := exec.CommandContext(ctx, "git", "commit", "-q", "-m", p.Message)
			commit.Dir = work
			out, err = commit.CombinedOutput()
		}
	}
	if err != nil {
		s.report("fork", title, bandav1.SetupState_SETUP_STATE_SKIPPED, strings.TrimSpace(string(out)+"\n"+err.Error()))
		notice("The forked thread's changes could not be applied, so this fork starts from " + os.Getenv("BANDA_BASE_BRANCH") + ". See the setup step for why.")
		return
	}
	s.report("fork", title, bandav1.SetupState_SETUP_STATE_DONE, p.Message)
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
	return s.run(id, title, func() (string, error) {
		out, err := runGroup(ctx, command)
		if err == nil { // part of the step: a failure to save it fails the step
			if err = os.MkdirAll(filepath.Dir(marker), 0o755); err == nil {
				err = os.WriteFile(marker, []byte(sum), 0o644)
			}
		}
		return out, err
	})
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

// runGroup runs a bash command in the checkout in its own process group. On
// timeout or shutdown only that group is stopped: SIGTERM, then SIGKILL after
// a grace period (a claude the script started may be refreshing a token).
// The runner's own claude is never in it.
func runGroup(ctx context.Context, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = work
	cmd.Stdout, cmd.Stderr = &out, &out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		pgid := -cmd.Process.Pid
		time.AfterFunc(killGrace, func() { syscall.Kill(pgid, syscall.SIGKILL) })
		return syscall.Kill(pgid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second // background processes may keep the output open
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState.Success() {
		err = nil // the script succeeded and left a background process running
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("timed out after %s", setupTimeout)
	}
	return out.String(), err
}

// checkout clones the repo onto the thread branch the first time; the home
// volume keeps it across restarts. A folder that already holds files (the
// thread had no repository until now) becomes the checkout instead, and the
// result says so.
func checkout(ctx context.Context) (string, error) {
	git := func(args ...string) error {
		if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("git %s: %w: %s", args[0], err, out)
		}
		return nil
	}
	// Banda manages git's credentials: what an agent wrote into the git config
	// on the thread's volume (perhaps the previous owner's, before a handoff)
	// doesn't get to see them. Hooks stay, and run with the owner's credentials.
	for _, f := range []string{filepath.Join(work, ".git", "config"), filepath.Join(os.Getenv("HOME"), ".gitconfig"),
		filepath.Join(cmp.Or(os.Getenv("XDG_CONFIG_HOME"), filepath.Join(os.Getenv("HOME"), ".config")), "git", "config")} {
		if err := dropCredentialConfig(ctx, f); err != nil {
			return "", err
		}
	}
	// The owner's identity (Settings), set on every start so a change applies.
	steps := [][]string{
		{"config", "--global", "user.name", cmp.Or(os.Getenv("BANDA_GIT_NAME"), "Banda")},
		{"config", "--global", "user.email", cmp.Or(os.Getenv("BANDA_GIT_EMAIL"), "banda@localhost")},
	}
	if u, err := url.Parse(os.Getenv("BANDA_REPO_URL")); err == nil && u.Scheme == "https" {
		// Only for the repo's host, in order: a token from the API for the
		// thread's repository (the GitHub App's), then GIT_TOKEN, read from the
		// environment at use time. Neither is ever written to disk.
		key := "credential.https://" + u.Host + ".helper"
		exec.CommandContext(ctx, "git", "config", "--global", "--unset-all", key).Run() // none yet is fine
		self, err := os.Executable()
		if err != nil {
			return "", err
		}
		steps = append(steps, []string{"config", "--global", "--add", key, self + " git-credential"})
		if os.Getenv("GIT_TOKEN") != "" {
			steps = append(steps, []string{"config", "--global", "--add", key, `!f() { echo username=x-access-token; echo "password=$GIT_TOKEN"; }; f`})
		}
	}
	for _, args := range steps {
		if err := git(args...); err != nil {
			return "", err
		}
	}
	kept := ""
	if _, err := os.Stat(filepath.Join(work, ".git")); err != nil {
		if entries, _ := os.ReadDir(work); len(entries) > 0 {
			if err := adopt(git); err != nil {
				return "", err
			}
			kept = fmt.Sprintf("The files that were already in the folder are kept: they are uncommitted changes on %s, on top of %s.",
				os.Getenv("BANDA_BRANCH"), os.Getenv("BANDA_BASE_BRANCH"))
		} else if err := git("clone", "--branch="+os.Getenv("BANDA_BASE_BRANCH"), "--", os.Getenv("BANDA_REPO_URL"), work); err != nil {
			return "", err
		}
	}
	// Also covers a previous start that died right after cloning. A fresh
	// checkout of a thread whose environment was deleted continues from the
	// pushed branch.
	branch := os.Getenv("BANDA_BRANCH")
	switch {
	case git("-C", work, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch) == nil:
		return kept, nil
	case git("-C", work, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/"+branch) == nil:
		return kept, git("-C", work, "switch", branch)
	}
	return kept, git("-C", work, "switch", "-c", branch)
}

// dropCredentialConfig removes the credential.*, include.* and includeIf.*
// keys from the git config file f, if there is one: a helper there would be
// handed the token git asks for, and an include could add one.
func dropCredentialConfig(ctx context.Context, f string) error {
	if _, err := os.Stat(f); err != nil {
		return nil
	}
	out, err := exec.CommandContext(ctx, "git", "config", "--file", f, "--null", "--name-only", "--get-regexp", `^(credential|include|includeif)\.`).Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 { // none
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", f, err)
	}
	for _, key := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		// Exit 5: a key listed twice is gone already.
		if err := exec.CommandContext(ctx, "git", "config", "--file", f, "--unset-all", key).Run(); err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 5) {
			return fmt.Errorf("remove %s from %s: %w", key, f, err)
		}
	}
	return nil
}

// adopt makes the folder a checkout of the thread branch, based on the base
// branch, without touching a file in it (never lose work): what differs from
// the base shows as uncommitted changes, and only the base's files missing
// from the folder are written. The repository is built beside the folder and
// moved in last, so a start that dies halfway begins again, and one that
// fails (a wrong URL or base branch) leaves nothing behind: the owner may
// then change the repository or remove it.
func adopt(git func(...string) error) (err error) {
	dir, base := filepath.Join(os.Getenv("HOME"), ".banda", "adopt.git"), os.Getenv("BANDA_BASE_BRANCH")
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dir)
		}
	}()
	if err := git("init", "-q", "--bare", "--initial-branch="+os.Getenv("BANDA_BRANCH"), dir); err != nil {
		return err
	}
	in := func(args ...string) error {
		return git(append([]string{"-C", work, "--git-dir=" + dir, "--work-tree=" + work}, args...)...)
	}
	for _, args := range [][]string{
		{"remote", "add", "origin", os.Getenv("BANDA_REPO_URL")},
		{"fetch", "-q", "origin", "+refs/heads/" + base + ":refs/remotes/origin/" + base},
		{"reset", "-q", "refs/remotes/origin/" + base}, // the branch and the index, not the files
	} {
		if err := in(args...); err != nil {
			return err
		}
	}
	// Without --force it never overwrites a file, and fails for those it
	// skipped (one is there already, or the base has a folder where the
	// folder has a file): those are the folder's changes.
	in("checkout-index", "--all", "--quiet")
	if err := in("config", "core.bare", "false"); err != nil {
		return err
	}
	return os.Rename(dir, filepath.Join(work, ".git"))
}

// waitForLogin waits for a subscription login, reported as the login step
// only if there is something to wait for.
func (s setup) waitForLogin(ctx context.Context, notice func(string) error, loginStatus func(bool) error) error {
	const title = "Log in to Claude Code"
	told := false
	for {
		if loggedIn(ctx) {
			if told {
				s.report("login", title, bandav1.SetupState_SETUP_STATE_DONE, "")
			}
			return loginStatus(true)
		}
		if !told {
			told = true
			loginStatus(false)
			s.report("login", title, bandav1.SetupState_SETUP_STATE_RUNNING, "")
			if err := notice(loginHint("Claude Code is not logged in.")); err != nil {
				return err
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func loggedIn(ctx context.Context) bool {
	return claudeCmd(ctx, "auth", "status", "--json").Run() == nil
}

func loginHint(problem string) string {
	return fmt.Sprintf("%s Open the Terminal tab and run `claude auth login` (or use the Log in button). Fallback: `kubectl exec -it -n banda t-%s -- claude auth login`. The thread continues once you are logged in.", problem, threadID)
}
