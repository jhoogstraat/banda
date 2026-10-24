package store

import (
	"encoding/json"
	"path"
	"strings"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// Activity summarizes an event for the team board, or returns nil for events
// that don't change what a passer-by should see (deltas, tool results, diffs).
func Activity(e *bandav1.Event) *bandav1.TeamActivity {
	var text string
	actor := e.Actor
	switch k := e.Kind.(type) {
	case *bandav1.Event_Message:
		text = k.Message.Text
	case *bandav1.Event_TurnRequest:
		text = "asks the agent (needs approval): " + snippet(k.TurnRequest.GetMessage().GetText())
	case *bandav1.Event_AgentText:
		text = k.AgentText.Text
	case *bandav1.Event_AgentThinking:
		text = "thinking"
	case *bandav1.Event_ToolCall:
		text = toolSummary(k.ToolCall)
	case *bandav1.Event_PermissionRequested:
		text = "asks permission: " + k.PermissionRequested.ToolName
		var q struct{ Questions []struct{ Question string } }
		if k.PermissionRequested.ToolName == "AskUserQuestion" && json.Unmarshal([]byte(k.PermissionRequested.InputJson), &q) == nil && len(q.Questions) > 0 {
			text = "asks: " + q.Questions[0].Question
		}
	case *bandav1.Event_TurnEnded:
		text = "finished the turn"
		if k.TurnEnded.IsError {
			text = "turn failed: " + k.TurnEnded.Error
		}
	case *bandav1.Event_StatusChanged:
		if k.StatusChanged.Status != bandav1.Status_STATUS_ERRORED {
			return nil
		}
		text, actor = strings.TrimSuffix("errored: "+k.StatusChanged.Reason, ": "), "system"
	case *bandav1.Event_SetupStep:
		actor = "system" // the runner reports setup, not the agent
		switch k.SetupStep.State {
		case bandav1.SetupState_SETUP_STATE_RUNNING:
			text = "setting up: " + k.SetupStep.Title
		case bandav1.SetupState_SETUP_STATE_FAILED:
			text = "setup failed: " + k.SetupStep.Title
		default:
			return nil
		}
	case *bandav1.Event_Notice:
		text = k.Notice.Text
	default:
		return nil
	}
	return &bandav1.TeamActivity{Actor: actor, Text: snippet(text), Time: e.Time}
}

// toolSummary is the tool name and its main argument: "Edit src/x.ts".
func toolSummary(c *bandav1.ToolCall) string {
	if c.Name == "TaskCreate" || c.Name == "TaskUpdate" {
		return "updated the plan"
	}
	var in map[string]any
	json.Unmarshal([]byte(c.InputJson), &in)
	for _, k := range []string{"file_path", "path", "command", "pattern", "url", "query", "description"} {
		if v, ok := in[k].(string); ok && v != "" {
			if strings.HasPrefix(v, "/") {
				v = strings.TrimPrefix(v, "/home/banda/work/") // the runner's checkout
				if strings.HasPrefix(v, "/") {
					v = path.Base(v)
				}
			}
			return c.Name + " " + v
		}
	}
	return c.Name
}

// snippet is the first non-empty line, at most 80 characters.
func snippet(s string) string {
	for l := range strings.Lines(s) {
		if l = strings.TrimSpace(l); l != "" {
			if r := []rune(l); len(r) > 80 {
				return string(r[:80]) + "…"
			}
			return l
		}
	}
	return ""
}
