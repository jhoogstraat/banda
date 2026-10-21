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
