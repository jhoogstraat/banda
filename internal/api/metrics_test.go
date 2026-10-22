package api

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jhoogstraat/banda/internal/store"
)

// Prometheus' text format: every status and mode, zeros included, so a series never vanishes.
func TestWriteMetrics(t *testing.T) {
	var b strings.Builder
	writeMetrics(&b, metrics{threads: map[string]int{"working": 2, "suspended": 5}, running: 3, suspended: 4,
		runners: 3, internalErrors: 1, uptime: 90 * time.Second})
	want := `# HELP banda_threads Threads by status.
# TYPE banda_threads gauge
banda_threads{status="starting"} 0
banda_threads{status="working"} 2
banda_threads{status="awaiting_input"} 0
banda_threads{status="errored"} 0
banda_threads{status="suspended"} 5
# HELP banda_sandboxes Sandboxes (thread and login environments) by mode.
# TYPE banda_sandboxes gauge
banda_sandboxes{mode="running"} 3
banda_sandboxes{mode="suspended"} 4
# HELP banda_runners_connected Runners connected to this API, login environments' included.
# TYPE banda_runners_connected gauge
banda_runners_connected 3
# HELP banda_internal_errors_total Calls that failed with an internal error, each logged as "internal error".
# TYPE banda_internal_errors_total counter
banda_internal_errors_total 1
# HELP banda_uptime_seconds Seconds since this API started.
# TYPE banda_uptime_seconds gauge
banda_uptime_seconds 90
`
	if b.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", b.String(), want)
	}
}

// An internal error counts; a known one doesn't.
func TestInternalErrorsCount(t *testing.T) {
	before := internalErrors.Load()
	publicError(t.Context(), "op", errors.New("db down"))
	publicError(t.Context(), "op", store.ErrNotFound)
	if n := internalErrors.Load() - before; n != 1 {
		t.Fatalf("counted %d, want 1", n)
	}
}
