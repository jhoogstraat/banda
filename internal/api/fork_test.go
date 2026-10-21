package api

import (
	"strings"
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
)

func TestForkPrompt(t *testing.T) {
	msg := func(actor, text string) *bandav1.Event {
		return &bandav1.Event{Actor: actor, Kind: &bandav1.Event_Message{Message: &bandav1.Message{Text: text}}}
	}
	reply := &bandav1.Event{Actor: "agent", Kind: &bandav1.Event_AgentText{AgentText: &bandav1.AgentText{Text: "Added it."}}}
	tool := &bandav1.Event{Actor: "agent", Kind: &bandav1.Event_ToolCall{ToolCall: &bandav1.ToolCall{Name: "Bash"}}}
	src := store.Thread{ID: "t1", Title: "Add a file", BaseBranch: "main"}
	names := map[string]string{"u1": "Ann"}

	got := forkPrompt(names, src, 1, []*bandav1.Event{msg("u1", "add a file"), tool, reply}, "")
	for _, want := range []string{`thread t1 ("Add a file"), made after its turn 1`, "on top of main", "Ann: add a file\n\nYou: Added it.\n\n---\n\nSum up"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "Bash") {
		t.Error("tool calls should be left out")
	}
	if got := forkPrompt(names, src, 1, nil, "try Rust"); !strings.HasSuffix(got, "---\n\ntry Rust") {
		t.Errorf("instruction should end the prompt: %q", got)
	}

	var long []*bandav1.Event
	for range 100 {
		long = append(long, msg("u1", strings.Repeat("é", maxTranscript/20)))
	}
	long = append(long, msg("u1", "last"))
	got = forkPrompt(names, src, 0, long, "go")
	if len(got) > maxTranscript+1000 || !strings.Contains(got, "(earlier messages left out)") || !strings.Contains(got, "Ann: last") {
		t.Errorf("long transcript: %d bytes, cut marker or latest message missing", len(got))
	}
	got = forkPrompt(names, src, 0, []*bandav1.Event{msg("u1", strings.Repeat("é", maxTranscript))}, "")
	if !strings.Contains(got, "Ann: é") || !strings.Contains(got, "é …") || len(got) > maxTranscript/2 {
		t.Errorf("one huge message should be kept, shortened: %d bytes", len(got))
	}
}
