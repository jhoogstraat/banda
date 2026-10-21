package store

import (
	"strings"
	"testing"
)

func TestColsMatchFields(t *testing.T) {
	if n, m := len(strings.Split(cols, ",")), len(fields(&Thread{})); n != m {
		t.Fatalf("%d columns, %d fields", n, m)
	}
	if strings.Contains(listCols, "t.diff") || !strings.HasPrefix(listCols, "t.id, t.owner_id") {
		t.Fatalf("list columns: %s", listCols)
	}
}
