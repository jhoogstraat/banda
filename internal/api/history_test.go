package api

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestSlim(t *testing.T) {
	small := &bandav1.Event{Kind: &bandav1.Event_ToolResult{ToolResult: &bandav1.ToolResult{Output: "ok"}}}
	if slim(small) != small {
		t.Error("a small result should be sent as is")
	}

	long := strings.Repeat("é", cutAt) // 2 bytes each: the cut falls inside a rune
	big := &bandav1.Event{Seq: 7, Kind: &bandav1.Event_ToolResult{ToolResult: &bandav1.ToolResult{ToolCallId: "t", Output: long}}}
	got := slim(big)
	out := got.GetToolResult().Output
	if !got.Truncated || len(out) > cutAt || !utf8.ValidString(out) || !strings.HasPrefix(long, out) || got.Seq != 7 {
		t.Errorf("a large result should be cut to valid UTF-8 and marked: %d bytes, truncated=%v", len(out), got.Truncated)
	}
	if big.Truncated || big.GetToolResult().Output != long {
		t.Error("slim must not change the shared event")
	}

	in, _ := json.Marshal(map[string]any{"file_path": "a.go", "edits": []any{map[string]any{"new_string": long}}})
	call := &bandav1.Event{Kind: &bandav1.Event_ToolCall{ToolCall: &bandav1.ToolCall{Name: "MultiEdit", InputJson: string(in)}}}
	got = slim(call)
	var v struct {
		FilePath string `json:"file_path"`
		Edits    []struct {
			NewString string `json:"new_string"`
		} `json:"edits"`
	}
	if err := json.Unmarshal([]byte(got.GetToolCall().InputJson), &v); err != nil || !got.Truncated || v.FilePath != "a.go" || len(v.Edits[0].NewString) > cutAt {
		t.Errorf("a large input should stay JSON with its long strings cut: %v %+v", err, v)
	}
}
