package main

import (
	"encoding/json"
	"strings"
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

func TestPermissionRules(t *testing.T) {
	old := work
	t.Cleanup(func() { work = old })
	work = "/w"
	for _, c := range []struct {
		rule, tool, input string
		want              bool
	}{
		{"Bash", "Bash", `{"command":"anything; rm -rf ~"}`, true},
		{"Bash", "Write", `{}`, false},
		{"Bash(npm test:*)", "Bash", `{"command":"npm test"}`, true},
		{"Bash(npm test:*)", "Bash", `{"command":"npm test -- --watch"}`, true},
		{"Bash(npm test:*)", "Bash", `{"command":"npm testing"}`, false},
		{"Bash(npm test:*)", "Bash", `{"command":"npm test && rm -rf ~"}`, false},
		{"Bash(npm test:*)", "Bash", `{"command":"npm test $(rm -rf ~)"}`, false},
		{"Bash(npm test:*)", "Bash", "{\"command\":\"npm test\\nrm -rf ~\"}", false},
		{"Bash(npm test:*)", "Write", `{"command":"npm test"}`, false},
		{"Edit(src/**)", "Write", `{"file_path":"src/a/b.go"}`, true},
		{"Edit(src/**)", "Edit", `{"file_path":"/w/src/b.go"}`, true},
		{"Edit(src/**)", "NotebookEdit", `{"notebook_path":"/w/src/n.ipynb"}`, true},
		{"Edit(src/**)", "Edit", `{"file_path":"/w/src/../secrets"}`, false},
		{"Edit(src/**)", "Edit", `{"file_path":"/w/srcs/x"}`, false},
		{"Edit(src/**)", "Edit", `{"file_path":"/etc/passwd"}`, false},
		{"Edit(src/**)", "Bash", `{"file_path":"src/x"}`, false},
		{"Edit(./**)", "Edit", `{"file_path":"/w/x"}`, true},
	} {
		if got := allows(c.rule, c.tool, json.RawMessage(c.input)); got != c.want {
			t.Errorf("%s allows %s %s: got %v", c.rule, c.tool, c.input, got)
		}
	}
	p := newPermissions()
	p.apply(&bandav1.PermissionCommand{AllowedTools: []string{"Bash(npm test:*)"}})
	if p.ask(&bandav1.PermissionRequested{Id: "a", ToolName: "Bash", InputJson: `{"command":"npm test"}`}) == nil {
		t.Error("a matching rule asked")
	}
	if p.ask(&bandav1.PermissionRequested{Id: "b", ToolName: "Bash", InputJson: `{"command":"npm run build"}`}) != nil {
		t.Error("a command outside the rule ran")
	}
}

func TestPermissionInputs(t *testing.T) {
	p := newPermissions()
	js := func(v any) string { b, _ := json.Marshal(v); return string(b) }
	p.ask(&bandav1.PermissionRequested{Id: "q", ToolName: "AskUserQuestion", InputJson: `{"questions":[{"question":"Which?"}]}`})
	lines := p.apply(&bandav1.PermissionCommand{RequestId: "q", Decision: bandav1.PermissionDecision_PERMISSION_DECISION_ALLOW, Answers: map[string]string{"Which?": "A, B"}})
	if js(lines) != `[{"response":{"request_id":"q","response":{"behavior":"allow","updatedInput":{"answers":{"Which?":"A, B"},"questions":[{"question":"Which?"}]}},"subtype":"success"},"type":"control_response"}]` {
		t.Fatalf("answers: %s", js(lines))
	}
	p.ask(&bandav1.PermissionRequested{Id: "e", ToolName: "Bash", InputJson: `{"command":"rm -rf /"}`})
	lines = p.apply(&bandav1.PermissionCommand{RequestId: "e", Decision: bandav1.PermissionDecision_PERMISSION_DECISION_ALLOW, UpdatedInputJson: `{"command":"ls"}`})
	if js(lines) != `[{"response":{"request_id":"e","response":{"behavior":"allow","updatedInput":{"command":"ls"}},"subtype":"success"},"type":"control_response"}]` {
		t.Fatalf("edited: %s", js(lines))
	}
}

func TestAgentEntersPlanMode(t *testing.T) {
	p := newPermissions()
	call := &bandav1.Event{Kind: &bandav1.Event_ToolCall{ToolCall: &bandav1.ToolCall{Id: "t1", Name: "EnterPlanMode"}}}
	result := func(isErr bool) *bandav1.Event {
		return &bandav1.Event{Kind: &bandav1.Event_ToolResult{ToolResult: &bandav1.ToolResult{ToolCallId: "t1", IsError: isErr}}}
	}
	if p.agentMode(call) != nil || p.agentMode(result(true)) != nil {
		t.Fatal("a refused EnterPlanMode changed the mode")
	}
	p.agentMode(call)
	if m := p.agentMode(result(false)); m.GetPermissionModeChanged().GetMode() != bandav1.PermissionMode_PERMISSION_MODE_PLAN {
		t.Fatalf("got %v", m)
	}
	if js, _ := json.Marshal(p.modeLine()); !strings.Contains(string(js), `"mode":"plan"`) {
		t.Errorf("a restarted claude would leave plan mode: %s", js)
	}
	p.agentMode(call)
	if p.agentMode(result(false)) != nil {
		t.Error("already in plan mode, reported again")
	}
}
