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
	for _, p := range []string{"b.txt", "notes/a.md", "notes/.hidden/x"} {
		os.WriteFile(filepath.Join(dir, p), []byte("x"), 0o600)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	repoURL = ""
	got := &bandav1.FileResponse{}
	if err := list(t.Context(), root, got); err != nil || strings.Join(got.Paths, " ") != "b.txt notes/.hidden/x notes/a.md" {
		t.Errorf("got %q, %v", got.Paths, err)
	}
}
