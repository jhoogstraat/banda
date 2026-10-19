package api

import (
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestAgentPrompt(t *testing.T) {
	msg := func(actor, text string) *bandav1.Event {
		return &bandav1.Event{Actor: actor, Kind: &bandav1.Event_Message{Message: &bandav1.Message{Text: text}}}
	}
	agent := &bandav1.Event{Actor: "agent", Kind: &bandav1.Event_AgentText{AgentText: &bandav1.AgentText{Text: "done"}}}
	names := map[string]string{"u1": "Ann", "u2": "Bob"}

	if got := agentPrompt("u1", names, nil, "u1", "fix it"); got != "fix it" {
		t.Errorf("owner prompt: %q", got)
	}
	if got := agentPrompt("u1", names, nil, "u2", "@agent fix it"); got != "Bob: @agent fix it" {
		t.Errorf("teammate prompt: %q", got)
	}
	want := "Messages posted in this thread since your last turn:\nBob: looks off\nAnn: agreed\n\nAnn: @agent fix it"
	if got := agentPrompt("u1", names, []*bandav1.Event{agent, msg("u2", "looks off"), msg("u1", "agreed")}, "u1", "@agent fix it"); got != want {
		t.Errorf("with chat:\n%q\nwant\n%q", got, want)
	}
}
