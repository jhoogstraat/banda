package main

import "testing"

// Lines are verbatim (trimmed) from docs/research/claude-headless.md.
func TestTranslate(t *testing.T) {
	tests := []struct {
		line        string
		want        string // GetKind() type of the single event, "" for none
		ended, auth bool
	}{
		{`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello "}},"session_id":"s","parent_tool_use_id":null}`, "agent_text_delta:Hello ", false, false},
		{`{"type":"assistant","message":{"id":"msg_1","content":[{"type":"text","text":"Done."}],"stop_reason":null},"parent_tool_use_id":null,"session_id":"s"}`, "agent_text:Done.", false, false},
		{`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"Let me think.","signature":"x"}]},"parent_tool_use_id":null}`, "agent_thinking:Let me think.", false, false},
		{`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"echo hi"}}]},"parent_tool_use_id":null}`, `tool_call:Bash {"command":"echo hi"}`, false, false},
		{`{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_1","type":"tool_result","content":"hi","is_error":false}]},"parent_tool_use_id":null,"tool_use_result":{"stdout":"hi"}}`, "tool_result:toolu_1 hi", false, false},
		{`{"type":"assistant","message":{"content":[{"type":"text","text":"from a subagent"}]},"parent_tool_use_id":"toolu_9"}`, "", false, false},
		{`{"type":"user","message":{"role":"user","content":"our own prompt"},"parent_tool_use_id":null}`, "", false, false},
		{`{"type":"result","subtype":"success","is_error":false,"result":"Hello.","terminal_reason":"completed","duration_ms":45,"total_cost_usd":0.000208}`, "turn_ended:false", true, false},
		{`{"type":"result","subtype":"success","is_error":true,"result":"Not logged in · Please run /login"}`, "turn_ended:true Not logged in · Please run /login", true, false},
		{`{"type":"system","subtype":"api_retry","attempt":1,"max_retries":10,"error_status":401,"error":"authentication_failed"}`, "", false, true},
		{`{"type":"system","subtype":"init","session_id":"s","model":"claude-opus-5-5"}`, "", false, false},
	}
	for _, tt := range tests {
		events, ended, auth := translate([]byte(tt.line))
		got := ""
		if len(events) > 1 {
			t.Errorf("%s: %d events", tt.line, len(events))
			continue
		}
		if len(events) == 1 {
			switch e := events[0]; {
			case e.GetAgentTextDelta() != nil:
				got = "agent_text_delta:" + e.GetAgentTextDelta().Text
			case e.GetAgentText() != nil:
				got = "agent_text:" + e.GetAgentText().Text
			case e.GetAgentThinking() != nil:
				got = "agent_thinking:" + e.GetAgentThinking().Text
			case e.GetToolCall() != nil:
				got = "tool_call:" + e.GetToolCall().Name + " " + e.GetToolCall().InputJson
			case e.GetToolResult() != nil:
				got = "tool_result:" + e.GetToolResult().ToolCallId + " " + e.GetToolResult().Output
			case e.GetTurnEnded() != nil:
				te := e.GetTurnEnded()
				got = "turn_ended:false"
				if te.IsError {
					got = "turn_ended:true " + te.Error
				}
			}
		}
		if got != tt.want || ended != tt.ended || auth != tt.auth {
			t.Errorf("%s:\n got %q ended=%v auth=%v\nwant %q ended=%v auth=%v", tt.line, got, ended, auth, tt.want, tt.ended, tt.auth)
		}
	}
}
