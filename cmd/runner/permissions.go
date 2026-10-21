package main

import (
	"cmp"
	"encoding/json"
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
// API answers them, and the tools the thread always allows, which it
// answers itself. See docs/research/claude-headless.md §4.
type permissions struct {
	mu      sync.Mutex
	pending map[string]json.RawMessage // request id -> the tool input, echoed back as updatedInput
	allowed map[string]bool
	mode    string // the CLI mode last asked for, set again when claude restarts
}

func newPermissions() *permissions {
	return &permissions{pending: map[string]json.RawMessage{}, allowed: map[string]bool{}}
}

// ask records a request. It reports an answer to send right away, without
// asking anyone, if the thread always allows the tool.
func (p *permissions) ask(r *bandav1.PermissionRequested) (auto map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	input := json.RawMessage(r.InputJson)
	if p.allowed[r.ToolName] {
		return controlResponse(r.Id, map[string]any{"behavior": "allow", "updatedInput": input})
	}
	p.pending[r.Id] = input
	return nil
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
	clear(p.allowed)
	for _, t := range c.AllowedTools {
		p.allowed[t] = true
	}
	if input, ok := p.pending[c.RequestId]; ok {
		delete(p.pending, c.RequestId)
		res := map[string]any{"behavior": "allow", "updatedInput": input}
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
