package store

import (
	"slices"
	"testing"
)

func TestDiffPaths(t *testing.T) {
	diff := "diff --git a/b.go b/b.go\n--- a/b.go\n+++ b/b.go\n@@ -1 +1 @@\n-x\n+diff --git a/no b/no\n" +
		"diff --git a/old name b/new name\nsimilarity index 90%\n" +
		"diff --git a/a.png b/a.png\nBinary files differ\n"
	if got, want := DiffPaths(diff), []string{"a.png", "b.go", "new name"}; !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
