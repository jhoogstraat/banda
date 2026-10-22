package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

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
		// TodoWrite and a Task with a nested call (the "Plans and subagents" section).
		{`{"type":"assistant","message":{"id":"msg_2","content":[{"type":"tool_use","id":"toolu_todo1","name":"TodoWrite","input":{"todos":[{"content":"Fix the parser","status":"in_progress","activeForm":"Fixing the parser"}]}}]},"parent_tool_use_id":null,"session_id":"s"}`, `tool_call:TodoWrite {"todos":[{"content":"Fix the parser","status":"in_progress","activeForm":"Fixing the parser"}]}`, false, false},
		{`{"type":"assistant","message":{"id":"msg_3","content":[{"type":"tool_use","id":"toolu_task1","name":"Task","input":{"description":"Find the parser","prompt":"Where is the config parser?","subagent_type":"Explore"}}]},"parent_tool_use_id":null,"session_id":"s"}`, `tool_call:Task {"description":"Find the parser","prompt":"Where is the config parser?","subagent_type":"Explore"}`, false, false},
		{`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Where is the config parser?"}]},"parent_tool_use_id":"toolu_task1","session_id":"s"}`, "", false, false},
		{`{"type":"assistant","message":{"id":"msg_4","content":[{"type":"tool_use","id":"toolu_sub1","name":"Grep","input":{"pattern":"func parse","path":"internal"}}]},"parent_tool_use_id":"toolu_task1","session_id":"s"}`, `tool_call:Grep {"pattern":"func parse","path":"internal"} parent=toolu_task1`, false, false},
		{`{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_sub1","type":"tool_result","content":"Found 1 file\ninternal/config/parse.go"}]},"parent_tool_use_id":"toolu_task1","session_id":"s"}`, "tool_result:toolu_sub1 Found 1 file\ninternal/config/parse.go", false, false},
		{`{"type":"assistant","message":{"id":"msg_5","content":[{"type":"text","text":"It is in internal/config/parse.go."}]},"parent_tool_use_id":"toolu_task1","session_id":"s"}`, "", false, false},
		{`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"It is"}},"parent_tool_use_id":"toolu_task1"}`, "", false, false},
		{`{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_task1","type":"tool_result","content":[{"type":"text","text":"The parser is internal/config/parse.go."}]}]},"parent_tool_use_id":null,"session_id":"s","tool_use_result":{"status":"completed","totalToolUseCount":1}}`, "tool_result:toolu_task1 The parser is internal/config/parse.go.", false, false},
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
				if p := e.GetToolCall().ParentToolCallId; p != "" {
					got += " parent=" + p
				}
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

func TestUserMessage(t *testing.T) {
	plain, _ := json.Marshal(userMessage(&bandav1.Prompt{Text: "hi"}))
	if string(plain) != `{"message":{"content":"hi","role":"user"},"type":"user"}` {
		t.Errorf("plain: %s", plain)
	}
	img, _ := json.Marshal(userMessage(&bandav1.Prompt{Text: "look", Images: []*bandav1.AttachmentImage{{MediaType: "image/png", Data: []byte("png")}}}))
	want := `{"message":{"content":[{"text":"look","type":"text"},{"source":{"data":"cG5n","media_type":"image/png","type":"base64"},"type":"image"}],"role":"user"},"type":"user"}`
	if string(img) != want {
		t.Errorf("with an image:\n%s\nwant\n%s", img, want)
	}
}

func TestProfileText(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	defer func(w string) { work = w }(work)
	work = dir
	builtin := func(name string) string {
		text, _ := profiles.ReadFile("profiles/" + name + ".md")
		return strings.TrimSpace(string(text))
	}
	if got := profileText("spec"); got != builtin("spec") || got == "" {
		t.Errorf("spec without a checkout: %q", got)
	}

	os.MkdirAll(filepath.Join(dir, ".banda", "profiles"), 0o755)
	os.WriteFile(filepath.Join(dir, ".banda", "profiles", "spec.md"), []byte("Repo spec\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".banda", "profiles", "other.md"), []byte("Other"), 0o644)
	os.WriteFile(filepath.Join(outside, "login"), []byte("sk-ant-oat01-secret"), 0o600)
	os.Symlink(filepath.Join(outside, "login"), filepath.Join(dir, ".banda", "profiles", "business.md"))
	for name, want := range map[string]string{
		"spec":     "Repo spec",         // the repo's replaces the built-in
		"devops":   builtin("devops"),   // no override
		"engineer": "",                  // adds nothing
		"business": builtin("business"), // a symlink out of the checkout is ignored
		"other":    "",                  // not a built-in
		"../spec":  "",                  // nor a path
	} {
		if got := profileText(name); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}
