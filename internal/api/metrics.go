package api

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jhoogstraat/banda/internal/thread"
)

// metrics is what /metrics reports, gathered on every scrape.
type metrics struct {
	threads                 map[string]int // by status
	running, suspended      int            // Sandboxes, threads' and login environments'
	runners, internalErrors int64
	uptime                  time.Duration
}

// MetricsHandler serves the metrics in Prometheus' text format, from the
// database and the cluster on every scrape. It serves on METRICS_ADDR, a port
// of its own that no ingress routes and sandboxes can't reach. PVC disk use
// is the kubelet's (kubelet_volume_stats_*), not here.
func (s *Service) MetricsHandler(started time.Time) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := metrics{internalErrors: internalErrors.Load(), uptime: time.Since(started)}
		var err error
		if m.threads, err = s.store.StatusCounts(r.Context()); err == nil {
			m.running, m.suspended, err = s.sandboxes.Count(r.Context())
		}
		if err != nil { // a failed scrape, not zeros
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.mu.Lock()
		m.runners = int64(len(s.runners))
		s.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		writeMetrics(w, m)
	})
}

func writeMetrics(w io.Writer, m metrics) {
	metric := func(name, typ, help string) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
	}
	metric("banda_threads", "gauge", "Threads by status.")
	for _, st := range []thread.Status{thread.Starting, thread.Working, thread.AwaitingInput, thread.Errored, thread.Suspended} {
		fmt.Fprintf(w, "banda_threads{status=%q} %d\n", st, m.threads[string(st)])
	}
	metric("banda_sandboxes", "gauge", "Sandboxes (thread and login environments) by mode.")
	fmt.Fprintf(w, "banda_sandboxes{mode=\"running\"} %d\nbanda_sandboxes{mode=\"suspended\"} %d\n", m.running, m.suspended)
	metric("banda_runners_connected", "gauge", "Runners connected to this API, login environments' included.")
	fmt.Fprintf(w, "banda_runners_connected %d\n", m.runners)
	metric("banda_internal_errors_total", "counter", "Calls that failed with an internal error, each logged as \"internal error\".")
	fmt.Fprintf(w, "banda_internal_errors_total %d\n", m.internalErrors)
	metric("banda_uptime_seconds", "gauge", "Seconds since this API started.")
	fmt.Fprintf(w, "banda_uptime_seconds %.0f\n", m.uptime.Seconds())
}
