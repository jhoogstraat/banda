package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestReadFile(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret")
	for p, c := range map[string]string{
		filepath.Join(dir, "a.txt"): "hello",
		filepath.Join(dir, "bin"):   "a\x00b",
		filepath.Join(dir, "big"):   strings.Repeat("x", maxFile+1),
		secret:                      "sk-ant-oat01-AbC_d-123456789",
	} {
		if err := os.WriteFile(p, []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	os.Symlink(secret, filepath.Join(dir, "link"))
	os.Symlink("a.txt", filepath.Join(dir, "inner"))
	os.Symlink(outside, filepath.Join(dir, "outdir"))
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	for path, want := range map[string]*bandav1.FileResponse{
		"a.txt": {Content: "hello"},
		"inner": {Content: "hello"}, // symlinks inside the checkout are fine
		"bin":   {Binary: true},
		"big":   {Content: strings.Repeat("x", maxFile), Truncated: true},
	} {
		got := &bandav1.FileResponse{}
		if err := read(root, path, got); err != nil || got.Content != want.Content || got.Binary != want.Binary || got.Truncated != want.Truncated {
			t.Errorf("%s: got %v, %v; want %v", path, got, err, want)
		}
	}
	for _, path := range []string{"../x", "../" + filepath.Base(outside) + "/secret", secret, "/etc/passwd", "link", "outdir/secret", "nope", "."} {
		got := &bandav1.FileResponse{}
		if err := read(root, path, got); err == nil || got.Content != "" {
			t.Errorf("%s: read succeeded: %v", path, got)
		}
	}
}

func TestListWithoutRepo(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "notes", ".hidden"), 0o755)
	os.MkdirAll(filepath.Join(dir, "node_modules", "x"), 0o755)
	for p, c := range map[string]string{"b.txt": "x", "notes/a.md": "x", "notes/.hidden/x": "x", "notes/.gitignore": "*.log\n", "notes/run.log": "x",
		".gitignore": "node_modules/\n", "node_modules/x/index.js": "x", "run.log": "x"} {
		os.WriteFile(filepath.Join(dir, p), []byte(c), 0o600)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	work = dir
	got := &bandav1.FileResponse{}
	// git's ignore rules, without a repository: .gitignore files apply to their folder
	if err := list(t.Context(), root, got); err != nil || strings.Join(got.Paths, " ") != ".gitignore b.txt notes/.gitignore notes/.hidden/x notes/a.md run.log" {
		t.Errorf("got %q, %v", got.Paths, err)
	}
}
