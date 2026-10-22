package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

const (
	maxFiles = 5000
	maxFile  = 512 << 10
)

// files answers a request from the web UI to list or read the checkout. Paths
// go through os.Root, which refuses absolute paths, ".." and symlinks that
// leave the checkout: the owner's Claude login lives outside of it.
func files(ctx context.Context, r *bandav1.FileRequest, send func(*bandav1.ConnectRequest) error) {
	res := &bandav1.FileResponse{RequestId: r.RequestId}
	root, err := os.OpenRoot(work)
	if err == nil {
		defer root.Close()
		if r.List {
			err = list(ctx, root, res)
		} else {
			err = read(root, r.Path, res)
		}
	}
	if errors.Is(err, fs.ErrNotExist) {
		res.Error = "not found"
	} else if err != nil {
		res.Error = redact(err.Error())
	}
	send(&bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_FileResponse{FileResponse: res}})
}

// list returns the tracked and unignored files that still exist, sorted. A
// folder that isn't a repository (a thread without one) gets git's ignore
// rules all the same: its .gitignore files and the global excludes.
func list(ctx context.Context, root *os.Root, res *bandav1.FileResponse) error {
	args := []string{"ls-files", "-z", "--cached", "--others", "--exclude-standard"}
	if _, err := root.Lstat(".git"); err != nil {
		empty, err := emptyRepo(ctx)
		if err != nil {
			return err
		}
		args = append([]string{"--git-dir=" + empty, "--work-tree=" + work}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = work
	out, err := cmd.Output()
	if ee := new(exec.ExitError); errors.As(err, &ee) {
		return fmt.Errorf("git ls-files: %w: %s", err, ee.Stderr)
	} else if err != nil {
		return err
	}
	paths := strings.FieldsFunc(string(out), func(r rune) bool { return r == 0 })
	paths = slices.DeleteFunc(paths, func(p string) bool {
		_, err := root.Lstat(p) // tracked but deleted
		return err != nil || !utf8.ValidString(p)
	})
	slices.Sort(paths)
	paths = slices.Compact(paths)
	if res.Truncated = len(paths) > maxFiles; res.Truncated {
		paths = paths[:maxFiles]
	}
	res.Paths = paths
	return nil
}

// emptyRepo is a repository with nothing in it, outside the folder, that
// lists a folder without one.
func emptyRepo(ctx context.Context) (string, error) {
	dir := filepath.Join(os.TempDir(), "banda-empty.git")
	if _, err := os.Stat(filepath.Join(dir, "HEAD")); err == nil {
		return dir, nil
	}
	return dir, exec.CommandContext(ctx, "git", "init", "-q", "--bare", dir).Run()
}

func read(root *os.Root, path string, res *bandav1.FileResponse) error {
	if fi, err := root.Stat(path); err != nil {
		return err
	} else if !fi.Mode().IsRegular() { // opening a FIFO would block
		return errors.New("not a regular file")
	}
	f, err := root.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil {
		return err
	}
	if res.Truncated = len(b) > maxFile; res.Truncated {
		b = b[:maxFile]
	}
	if res.Binary = bytes.IndexByte(b[:min(len(b), 8<<10)], 0) >= 0; !res.Binary {
		res.Content = redact(string(b))
	}
	return nil
}
