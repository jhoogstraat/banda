package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestSetupOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	work = t.TempDir()
	var got []string
	s := setup{send: func(e *bandav1.Event) error {
		st := e.GetSetupStep()
		got = append(got, st.State.String()+" "+strings.TrimSpace(st.Output))
		return nil
	}}
	step := func(script, command string) error {
		got = nil
		return s.once(context.Background(), "setup", "Run .banda/setup.sh", []byte(script), command)
	}

	if err := step("v1", "echo ran"); err != nil || len(got) != 2 || got[1] != "SETUP_STATE_DONE ran" {
		t.Fatalf("first run: %v %q", err, got)
	}
	if err := step("v1", "echo ran"); err != nil || len(got) != 0 {
		t.Fatalf("unchanged script ran again: %q", got)
	}
	if err := step("v2", "echo boom; exit 3"); err == nil || len(got) != 2 || !strings.HasPrefix(got[1], "SETUP_STATE_FAILED boom") {
		t.Fatalf("failing script: %v %q", err, got)
	}
	if err := step("v2", "echo fixed"); err != nil || len(got) != 2 {
		t.Fatalf("a failed script runs again: %v %q", err, got)
	}
}

func TestSetupReportSkipsQuietly(t *testing.T) {
	n := 0
	s := setup{send: func(*bandav1.Event) error { n++; return nil }}
	s.report("setup", "x", bandav1.SetupState_SETUP_STATE_SKIPPED, "")
	if n != 0 {
		t.Error("a resume reported a skipped step")
	}
	s.fresh = true
	s.report("setup", "x", bandav1.SetupState_SETUP_STATE_SKIPPED, "")
	big := strings.Repeat("x", 2*maxOutput)
	var out string
	s.send = func(e *bandav1.Event) error { out = e.GetSetupStep().Output; return nil }
	s.report("setup", "x", bandav1.SetupState_SETUP_STATE_DONE, big)
	if n != 1 || len(out) > maxOutput+len("…") {
		t.Errorf("n=%d len=%d", n, len(out))
	}
}

func TestRunGroupBackgroundChild(t *testing.T) {
	work = t.TempDir()
	out, err := runGroup(context.Background(), "sleep 30 & echo started")
	if err != nil || strings.TrimSpace(out) != "started" {
		t.Errorf("got %q, %v", out, err)
	}
	if _, err := runGroup(context.Background(), "sleep 30 & exit 2"); err == nil {
		t.Error("a failing script with a background child succeeded")
	}
}

func TestSetupMarkerFailureFailsStep(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	work = t.TempDir()
	os.WriteFile(filepath.Join(home, ".banda"), nil, 0o644) // a file where the marker dir should be
	var last bandav1.SetupState
	s := setup{send: func(e *bandav1.Event) error { last = e.GetSetupStep().State; return nil }}
	if err := s.once(context.Background(), "setup", "x", []byte("v1"), "true"); err == nil || last != bandav1.SetupState_SETUP_STATE_FAILED {
		t.Errorf("got %v, last state %v", err, last)
	}
}
