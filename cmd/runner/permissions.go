package main

import (
	"cmp"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// cliModes maps Banda's permission modes to Claude Code's --permission-mode.
var cliModes = map[bandav1.PermissionMode]string{
	bandav1.PermissionMode_PERMISSION_MODE_BYPASS:       "bypassPermissions",
	bandav1.PermissionMode_PERMISSION_MODE_ASK:          "default",
	bandav1.PermissionMode_PERMISSION_MODE_ACCEPT_EDITS: "acceptEdits",
	bandav1.PermissionMode_PERMISSION_MODE_PLAN:         "plan",
}

// permissions holds the agent's pending can_use_tool requests until the
// API answers them, and the tools and rules the thread always allows, which
// it answers itself. See docs/research/claude-headless.md §4.
type permissions struct {
	mu       sync.Mutex
	pending  map[string]json.RawMessage // request id -> the tool input, echoed back as updatedInput
	allowed  []string
	mode     string // the CLI mode last asked for or entered, set again when claude restarts
	entering string // the agent's EnterPlanMode call, until its result
}

func newPermissions() *permissions {
	return &permissions{pending: map[string]json.RawMessage{}}
}

// ask records a request. It reports an answer to send right away, without
// asking anyone, if the thread always allows the call.
func (p *permissions) ask(r *bandav1.PermissionRequested) (auto map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	input := json.RawMessage(r.InputJson)
	if slices.ContainsFunc(p.allowed, func(rule string) bool { return allows(rule, r.ToolName, input) }) {
		return controlResponse(r.Id, map[string]any{"behavior": "allow", "updatedInput": input})
	}
	p.pending[r.Id] = input
	return nil
}

var editTools = []string{"Edit", "Write", "MultiEdit", "NotebookEdit"}

// allows reports whether a rule allows a call: the whole tool ("Bash"), a
// command and its arguments ("Bash(npm test:*)"), or edits under a
// directory of the checkout ("Edit(src/**)", any edit tool). A command with
// shell operators never matches a prefix, so "npm test; rm -rf ~" still asks.
// ponytail: paths are compared as written; a symlink inside the directory leads out of it.
func allows(rule, tool string, input json.RawMessage) bool {
	name, arg, ok := strings.Cut(rule, "(")
	if !ok {
		return rule == tool
	}
	arg, _ = strings.CutSuffix(arg, ")")
	var in struct {
		Command      string `json:"command"`
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
	}
	json.Unmarshal(input, &in)
	if prefix, ok := strings.CutSuffix(arg, ":*"); ok && name == "Bash" && tool == "Bash" && prefix != "" {
		c := strings.TrimSpace(in.Command)
		return !strings.ContainsAny(c, ";&|<>$`()\n\\") && (c == prefix || strings.HasPrefix(c, prefix+" "))
	}
	if dir, ok := strings.CutSuffix(arg, "/**"); ok && name == "Edit" && slices.Contains(editTools, tool) {
		path := cmp.Or(in.FilePath, in.NotebookPath)
		if path == "" {
			return false
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(work, path)
		}
		rel, err := filepath.Rel(filepath.Join(work, dir), filepath.Clean(path))
		return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
	}
	return false
}

// agentMode reports the agent entering plan mode itself (EnterPlanMode
// succeeded), as an event for the API, which shows it as the agent's.
// The CLI switches without asking the host; see docs/research/claude-headless.md §4.
func (p *permissions) agentMode(e *bandav1.Event) *bandav1.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c := e.GetToolCall(); c != nil && c.Name == "EnterPlanMode" && c.ParentToolCallId == "" {
		p.entering = c.Id
	}
	r := e.GetToolResult()
	if r == nil || p.entering == "" || r.ToolCallId != p.entering {
		return nil
	}
	p.entering = ""
	if r.IsError || p.mode == "plan" {
		return nil
	}
	p.mode = "plan"
	return &bandav1.Event{Kind: &bandav1.Event_PermissionModeChanged{PermissionModeChanged: &bandav1.PermissionModeChanged{Mode: bandav1.PermissionMode_PERMISSION_MODE_PLAN}}}
}

func (p *permissions) drop(id string) {
	p.mu.Lock()
	delete(p.pending, id)
	p.mu.Unlock()
}

// clear forgets all pending requests, e.g. when the turn has ended.
func (p *permissions) clear() {
	p.mu.Lock()
	clear(p.pending)
	p.mu.Unlock()
}

// apply turns a command from the API into the lines for claude's stdin: the
// answer to a pending request, if it still is, then the mode switch.
func (p *permissions) apply(c *bandav1.PermissionCommand) (lines []map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.allowed = c.AllowedTools
	if input, ok := p.pending[c.RequestId]; ok {
		delete(p.pending, c.RequestId)
		res := map[string]any{"behavior": "allow", "updatedInput": answered(input, c)}
		if c.Decision == bandav1.PermissionDecision_PERMISSION_DECISION_DENY {
			res = map[string]any{"behavior": "deny", "message": cmp.Or(c.Message, "Denied in Banda.")}
		}
		lines = append(lines, controlResponse(c.RequestId, res))
	}
	if m := cliModes[c.Mode]; m != "" {
		p.mode = m
		lines = append(lines, controlRequest(map[string]any{"subtype": "set_permission_mode", "mode": m}))
	}
	return lines
}

// answered is the input to allow a request with: the one the API sent, or the
// agent's own with the answers to its questions (AskUserQuestion) added.
func answered(input json.RawMessage, c *bandav1.PermissionCommand) json.RawMessage {
	if c.UpdatedInputJson != "" && json.Valid([]byte(c.UpdatedInputJson)) {
		return json.RawMessage(c.UpdatedInputJson)
	}
	if len(c.Answers) == 0 {
		return input
	}
	var in map[string]any
	if json.Unmarshal(input, &in) != nil || in == nil {
		in = map[string]any{}
	}
	in["answers"] = c.Answers
	b, _ := json.Marshal(in)
	return b
}

// modeLine sets the mode again, for a new claude process, if one was asked for.
func (p *permissions) modeLine() map[string]any {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.mode == "" {
		return nil
	}
	return controlRequest(map[string]any{"subtype": "set_permission_mode", "mode": p.mode})
}

func controlResponse(id string, res map[string]any) map[string]any {
	return map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": id, "response": res}}
}
