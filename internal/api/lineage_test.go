package api

import (
	"strings"
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestLineageStatus(t *testing.T) {
	status := func(s bandav1.Status, reason string) *bandav1.Event {
		return &bandav1.Event{Kind: &bandav1.Event_StatusChanged{StatusChanged: &bandav1.StatusChanged{Status: s, Reason: reason}}}
	}
	ended := func(err string) *bandav1.Event {
		return &bandav1.Event{Kind: &bandav1.Event_TurnEnded{TurnEnded: &bandav1.TurnEnded{IsError: err != "", Error: err}}}
	}
	tests := []struct {
		name   string
		events []*bandav1.Event
		want   bandav1.Status
		text   string
	}{
		{"last turn ended", []*bandav1.Event{ended(""), status(bandav1.Status_STATUS_AWAITING_INPUT, "")}, bandav1.Status_STATUS_AWAITING_INPUT, ""},
		{"turn failed", []*bandav1.Event{ended("boom\nstack"), status(bandav1.Status_STATUS_AWAITING_INPUT, "")}, bandav1.Status_STATUS_AWAITING_INPUT, "Turn failed: boom"},
		{"a queued turn is still running", []*bandav1.Event{ended("")}, bandav1.Status_STATUS_UNSPECIFIED, ""},
		{"ready without a turn", []*bandav1.Event{status(bandav1.Status_STATUS_AWAITING_INPUT, "")}, bandav1.Status_STATUS_UNSPECIFIED, ""},
		{"setup failed", []*bandav1.Event{status(bandav1.Status_STATUS_ERRORED, "Setup failed: checkout")}, bandav1.Status_STATUS_ERRORED, "Setup failed: checkout"},
		{"environment lost", []*bandav1.Event{status(bandav1.Status_STATUS_ERRORED, "")}, bandav1.Status_STATUS_ERRORED, "The environment failed or was lost."},
		{"working is no news", []*bandav1.Event{status(bandav1.Status_STATUS_WORKING, "")}, bandav1.Status_STATUS_UNSPECIFIED, ""},
	}
	for _, tt := range tests {
		got, text := lineageStatus(tt.events)
		if got != tt.want || text != tt.text {
			t.Errorf("%s: got %v %q, want %v %q", tt.name, got, text, tt.want, tt.text)
		}
	}
}

func TestLineageInPrompt(t *testing.T) {
	u := &bandav1.Event{Actor: "system", Kind: &bandav1.Event_LineageUpdate{LineageUpdate: &bandav1.LineageUpdate{
		ThreadId: "c1", Title: "fix the parser", Status: bandav1.Status_STATUS_AWAITING_INPUT, Text: "Fixed it."}}}
	got := agentPrompt("u1", nil, []*bandav1.Event{u}, "u1", &bandav1.Message{Text: "next?"})
	if !strings.Contains(got, `[Sub-thread c1 "fix the parser" finished] Fixed it.`) || !strings.HasSuffix(got, "u1: next?") {
		t.Errorf("prompt: %q", got)
	}
	if got := firstLine(strings.Repeat("x", 300) + "\nmore"); len([]rune(got)) != 201 {
		t.Errorf("first line not truncated: %d", len([]rune(got)))
	}
}
