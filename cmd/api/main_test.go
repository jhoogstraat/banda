package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Banda's pages refuse frames: a previewed app is on the same site and would
// get a signed-in Banda to clickjack.
func TestSPANoFrames(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<!doctype html>"), 0o644)
	for _, path := range []string{"/", "/t/abc"} {
		w := httptest.NewRecorder()
		spa(dir).ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if got := w.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
			t.Errorf("%s: Content-Security-Policy = %q", path, got)
		}
	}
}

// Test settings that would send credentials elsewhere refuse to start.
func TestTestHooks(t *testing.T) {
	for _, ok := range [][3]string{{"", "", ""}, {"http://127.0.0.1:4321", "banda-github-app-abc", "banda-gitlab-app-abc-"}, {"http://localhost:1", "", ""}, {"http://[::1]:1/", "", ""}} {
		if err := testHooks(ok[0], ok[1], ok[2]); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range [][3]string{{"https://forge.evil.example", "", ""}, {"http://127.0.0.1.evil.example", "", ""}, {"http://10.0.0.1:80", "", ""},
		{"file:///etc", "", ""}, {"", "banda-git", ""}, {"", "banda-env-team", ""}, {"", "", "banda-env-"}, {"", "", "banda-git"}} {
		if err := testHooks(bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
}
