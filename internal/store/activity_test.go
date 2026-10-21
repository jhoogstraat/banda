package store

import (
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestActivity(t *testing.T) {
	for _, c := range []struct {
		event *bandav1.Event
		want  string // "" for nil
	}{
		{&bandav1.Event{Kind: &bandav1.Event_ToolCall{ToolCall: &bandav1.ToolCall{Name: "Edit", InputJson: `{"file_path":"/home/banda/work/src/x.ts","old_string":"a"}`}}}, "Edit src/x.ts"},
		{&bandav1.Event{Kind: &bandav1.Event_ToolCall{ToolCall: &bandav1.ToolCall{Name: "Bash", InputJson: `{"command":"go test ./..."}`}}}, "Bash go test ./..."},
		{&bandav1.Event{Kind: &bandav1.Event_ToolCall{ToolCall: &bandav1.ToolCall{Name: "Read", InputJson: `{"file_path":"/etc/hosts"}`}}}, "Read hosts"},
		{&bandav1.Event{Kind: &bandav1.Event_ToolCall{ToolCall: &bandav1.ToolCall{Name: "TodoWrite", InputJson: `{}`}}}, "updated the plan"},
		{&bandav1.Event{Kind: &bandav1.Event_Message{Message: &bandav1.Message{Text: "\n  first line\nsecond"}}}, "first line"},
		{&bandav1.Event{Kind: &bandav1.Event_StatusChanged{StatusChanged: &bandav1.StatusChanged{Status: bandav1.Status_STATUS_ERRORED, Reason: "Setup failed: tools"}}}, "errored: Setup failed: tools"},
		{&bandav1.Event{Kind: &bandav1.Event_StatusChanged{StatusChanged: &bandav1.StatusChanged{Status: bandav1.Status_STATUS_ERRORED}}}, "errored"},
		{&bandav1.Event{Kind: &bandav1.Event_StatusChanged{StatusChanged: &bandav1.StatusChanged{Status: bandav1.Status_STATUS_WORKING}}}, ""},
		{&bandav1.Event{Kind: &bandav1.Event_TurnRequest{TurnRequest: &bandav1.TurnRequest{Message: &bandav1.Message{Text: "@agent fix it"}}}}, "asks the agent (needs approval): @agent fix it"},
		{&bandav1.Event{Kind: &bandav1.Event_ToolResult{ToolResult: &bandav1.ToolResult{}}}, ""},
		{&bandav1.Event{Kind: &bandav1.Event_SetupStep{SetupStep: &bandav1.SetupStep{Title: "tools", State: bandav1.SetupState_SETUP_STATE_DONE}}}, ""},
	} {
		got := ""
		if a := Activity(c.event); a != nil {
			got = a.Text
		}
		if got != c.want {
			t.Errorf("%v: got %q, want %q", c.event.Kind, got, c.want)
		}
	}
}
