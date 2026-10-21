package main

import (
	"encoding/json"
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// The can_use_tool line as recorded from the real CLI (docs/research/claude-headless.md §4).
const canUseTool = `{"type":"control_request","request_id":"e819","request":{"subtype":"can_use_tool","tool_name":"Bash","display_name":"Bash","input":{"command":"touch banda.txt","description":"Print hi"},"description":"Print hi","permission_suggestions":[],"blocked_path":"/w/banda.txt","tool_use_id":"toolu_fake1"}}`

func TestPermissionRequest(t *testing.T) {
	events, _, _ := translate([]byte(canUseTool))
	r := events[0].GetPermissionRequested()
	if len(events) != 1 || r.Id != "e819" || r.ToolCallId != "toolu_fake1" || r.ToolName != "Bash" || r.Reason != "Print hi" ||
		r.InputJson != `{"command":"touch banda.txt","description":"Print hi"}` {
		t.Fatalf("got %v", events)
	}
	if _, ok := unsupportedRequest([]byte(canUseTool)); ok {
		t.Error("can_use_tool refused")
	}
	if id, ok := unsupportedRequest([]byte(`{"type":"control_request","request_id":"h1","request":{"subtype":"hook_callback"}}`)); !ok || id != "h1" {
		t.Error("an unknown control request would leave the CLI waiting")
	}
	events, _, _ = translate([]byte(`{"type":"control_cancel_request","request_id":"e819"}`))
	if c := events[0].GetPermissionResolved(); c.Id != "e819" || c.Decision != bandav1.PermissionDecision_PERMISSION_DECISION_CANCELED {
		t.Fatalf("cancel: got %v", events)
	}
}

func TestPermissionAnswers(t *testing.T) {
	p := newPermissions()
	req := func(id, tool string) *bandav1.PermissionRequested {
		return &bandav1.PermissionRequested{Id: id, ToolName: tool, InputJson: `{"command":"ls"}`}
	}
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }

	if p.ask(req("a", "Bash")) != nil {
		t.Fatal("asked nobody")
	}
	lines := p.apply(&bandav1.PermissionCommand{RequestId: "a", Decision: bandav1.PermissionDecision_PERMISSION_DECISION_DENY, Message: "no"})
	if len(lines) != 1 || js(lines[0]) != `{"response":{"request_id":"a","response":{"behavior":"deny","message":"no"},"subtype":"success"},"type":"control_response"}` {
		t.Fatalf("deny: %s", js(lines))
	}
	if len(p.apply(&bandav1.PermissionCommand{RequestId: "a"})) != 0 {
		t.Error("answered a request twice")
	}

	p.ask(req("b", "Bash"))
	lines = p.apply(&bandav1.PermissionCommand{RequestId: "b", Decision: bandav1.PermissionDecision_PERMISSION_DECISION_ALLOW_ALWAYS,
		AllowedTools: []string{"Bash"}, Mode: bandav1.PermissionMode_PERMISSION_MODE_ACCEPT_EDITS})
	if len(lines) != 2 || js(lines[0]) != `{"response":{"request_id":"b","response":{"behavior":"allow","updatedInput":{"command":"ls"}},"subtype":"success"},"type":"control_response"}` ||
		lines[1]["request"].(map[string]any)["mode"] != "acceptEdits" {
		t.Fatalf("allow always and switch mode: %s", js(lines))
	}
	if auto := p.ask(req("c", "Bash")); js(auto) != `{"response":{"request_id":"c","response":{"behavior":"allow","updatedInput":{"command":"ls"}},"subtype":"success"},"type":"control_response"}` {
		t.Fatalf("an always-allowed tool asked: %s", js(auto))
	}
	if p.ask(req("d", "Write")) != nil {
		t.Error("other tools must still ask")
	}
	p.clear()
	if len(p.apply(&bandav1.PermissionCommand{RequestId: "d"})) != 0 {
		t.Error("answered a request of an ended turn")
	}
}
