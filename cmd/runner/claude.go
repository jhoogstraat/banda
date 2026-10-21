package main

import (
	"cmp"
	"encoding/json"
	"strings"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// line is the subset of Claude Code's stream-json output we use.
// See docs/research/claude-headless.md.
type line struct {
	Type            string  `json:"type"`
	Subtype         string  `json:"subtype"`
	ParentToolUseID *string `json:"parent_tool_use_id"`
	Message         struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
	} `json:"event"`
	// result
	IsError      bool    `json:"is_error"`
	Result       string  `json:"result"`
	DurationMS   int64   `json:"duration_ms"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	// system/api_retry
	Error string `json:"error"`
	// control_request (can_use_tool) and control_cancel_request
	RequestID string `json:"request_id"`
	Request   struct {
		Subtype        string          `json:"subtype"`
		ToolName       string          `json:"tool_name"`
		Input          json.RawMessage `json:"input"`
		ToolUseID      string          `json:"tool_use_id"`
		DecisionReason string          `json:"decision_reason"`
		Description    string          `json:"description"`
	} `json:"request"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// authErrors are api_retry categories that mean the user must log in again.
var authErrors = map[string]bool{"authentication_failed": true, "oauth_org_not_allowed": true, "account_on_hold": true, "billing_error": true}

// translate maps one stream-json line to Banda events. authFailed reports a
// retry loop caused by a bad login, which the caller should interrupt.
// A subagent's (Task tool's) lines carry parent_tool_use_id: only its tool
// calls and results are kept, tagged with the parent call, so the chat can nest
// them. Its prompt, text and deltas are dropped; the Task result has its answer.
func translate(raw []byte) (events []*bandav1.Event, turnEnded, authFailed bool) {
	var l line
	if json.Unmarshal(raw, &l) != nil {
		return nil, false, false
	}
	sub := l.ParentToolUseID != nil
	parent := ""
	if sub {
		parent = *l.ParentToolUseID
	}
	switch l.Type {
	case "stream_event":
		if !sub && l.Event.Type == "content_block_delta" && l.Event.Delta.Type == "text_delta" {
			events = append(events, &bandav1.Event{Kind: &bandav1.Event_AgentTextDelta{AgentTextDelta: &bandav1.AgentTextDelta{Text: l.Event.Delta.Text}}})
		}
	case "assistant", "user":
		var blocks []block
		if json.Unmarshal(l.Message.Content, &blocks) != nil {
			return nil, false, false // plain string content is our own prompt echoed back
		}
		for _, b := range blocks {
			if sub && b.Type != "tool_use" && b.Type != "tool_result" {
				continue
			}
			switch b.Type {
			case "text":
				events = append(events, &bandav1.Event{Kind: &bandav1.Event_AgentText{AgentText: &bandav1.AgentText{Text: b.Text}}})
			case "thinking":
				if b.Thinking != "" {
					events = append(events, &bandav1.Event{Kind: &bandav1.Event_AgentThinking{AgentThinking: &bandav1.AgentThinking{Text: b.Thinking}}})
				}
			case "tool_use":
				events = append(events, &bandav1.Event{Kind: &bandav1.Event_ToolCall{ToolCall: &bandav1.ToolCall{Id: b.ID, Name: b.Name, InputJson: string(b.Input), ParentToolCallId: parent}}})
			case "tool_result":
				events = append(events, &bandav1.Event{Kind: &bandav1.Event_ToolResult{ToolResult: &bandav1.ToolResult{ToolCallId: b.ToolUseID, Output: text(b.Content), IsError: b.IsError}}})
			}
		}
	case "result":
		if sub {
			return nil, false, false
		}
		te := &bandav1.TurnEnded{IsError: l.IsError, DurationMs: l.DurationMS, CostUsd: l.TotalCostUSD}
		if l.IsError {
			te.Error = l.Result
		}
		return []*bandav1.Event{{Kind: &bandav1.Event_TurnEnded{TurnEnded: te}}}, true, false
	case "system":
		return nil, false, l.Subtype == "api_retry" && authErrors[l.Error]
	case "control_request": // the CLI asks the host; other subtypes are answered by the caller
		if r := l.Request; r.Subtype == "can_use_tool" {
			events = append(events, &bandav1.Event{Kind: &bandav1.Event_PermissionRequested{PermissionRequested: &bandav1.PermissionRequested{
				Id: l.RequestID, ToolCallId: r.ToolUseID, ToolName: r.ToolName, InputJson: string(r.Input), Reason: cmp.Or(r.DecisionReason, r.Description)}}})
		}
	case "control_cancel_request":
		events = append(events, &bandav1.Event{Kind: &bandav1.Event_PermissionResolved{PermissionResolved: &bandav1.PermissionResolved{
			Id: l.RequestID, Decision: bandav1.PermissionDecision_PERMISSION_DECISION_CANCELED}}})
	}
	return events, false, false
}

// unsupportedRequest reports the id of a control request from the CLI that
// Banda doesn't answer (anything but can_use_tool), so the caller can refuse it.
func unsupportedRequest(raw []byte) (string, bool) {
	var l line
	if json.Unmarshal(raw, &l) != nil || l.Type != "control_request" || l.Request.Subtype == "can_use_tool" {
		return "", false
	}
	return l.RequestID, true
}

// text flattens tool_result content, which is a string or a list of blocks.
func text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []block
	json.Unmarshal(raw, &blocks)
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		} else {
			parts = append(parts, "["+b.Type+"]")
		}
	}
	return strings.Join(parts, "\n")
}
