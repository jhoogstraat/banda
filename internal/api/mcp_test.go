package api

import (
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestTranscript(t *testing.T) {
	ev := func(actor string, kind any) *bandav1.Event {
		e := &bandav1.Event{Actor: actor}
		switch k := kind.(type) {
		case *bandav1.Message:
			e.Kind = &bandav1.Event_Message{Message: k}
		case *bandav1.AgentText:
			e.Kind = &bandav1.Event_AgentText{AgentText: k}
		case *bandav1.ToolCall:
			e.Kind = &bandav1.Event_ToolCall{ToolCall: k}
		case *bandav1.ToolResult:
			e.Kind = &bandav1.Event_ToolResult{ToolResult: k}
		case *bandav1.TurnEnded:
			e.Kind = &bandav1.Event_TurnEnded{TurnEnded: k}
		case *bandav1.Notice:
			e.Kind = &bandav1.Event_Notice{Notice: k}
		}
		return e
	}
	names := map[string]string{"u1": "Ann"}
	events := []*bandav1.Event{
		ev("u1", &bandav1.Message{Text: "fix it"}),
		ev("agent", &bandav1.ToolCall{Name: "Bash", InputJson: `{"command":"secret"}`}),
		ev("agent", &bandav1.ToolCall{Name: "Grep", ParentToolCallId: "t1"}),
		ev("agent", &bandav1.ToolResult{Output: "secret output"}),
		ev("agent", &bandav1.AgentText{Text: "done"}),
		ev("agent", &bandav1.TurnEnded{IsError: true, Error: "boom"}),
		ev("agent:abc", &bandav1.Message{Text: "fyi"}),
		ev("system", &bandav1.Notice{Text: "log in"}),
	}
	want := "Ann: fix it\nagent called tool Bash\nagent: done\nturn ended with error: boom\nagent in thread abc: fyi\nnotice: log in"
	if got := transcript(names, events, 50); got != want {
		t.Errorf("transcript:\n%q\nwant\n%q", got, want)
	}
	if got := transcript(names, events, 2); got != "agent in thread abc: fyi\nnotice: log in" {
		t.Errorf("limited transcript: %q", got)
	}
	if got := transcript(names, nil, 50); got != "" {
		t.Errorf("empty transcript: %q", got)
	}
}
