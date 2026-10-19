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

// list returns the tracked and unignored files that still exist, sorted.
func list(ctx context.Context, root *os.Root, res *bandav1.FileResponse) error {
	cmd := exec.CommandContext(ctx, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
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
