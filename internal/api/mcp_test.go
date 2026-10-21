package api

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/store"
)

func TestTranscript(t *testing.T) {
	ev := func(actor string, kind any) *bandav1.Event {
		e := &bandav1.Event{Actor: actor}
		switch k := kind.(type) {
		case *bandav1.Message:
			e.Kind = &bandav1.Event_Message{Message: k}
		case *bandav1.AgentText:
			e.Kind = &bandav1.Event_AgentText{AgentText: k}
		case *bandav1.ToolCall:
			e.Kind = &bandav1.Event_ToolCall{ToolCall: k}
		case *bandav1.ToolResult:
			e.Kind = &bandav1.Event_ToolResult{ToolResult: k}
		case *bandav1.TurnEnded:
			e.Kind = &bandav1.Event_TurnEnded{TurnEnded: k}
		case *bandav1.Notice:
			e.Kind = &bandav1.Event_Notice{Notice: k}
		}
		return e
	}
	names := map[string]string{"u1": "Ann"}
	events := []*bandav1.Event{
		ev("u1", &bandav1.Message{Text: "fix it"}),
		ev("agent", &bandav1.ToolCall{Name: "Bash", InputJson: `{"command":"secret"}`}),
		ev("agent", &bandav1.ToolCall{Name: "Grep", ParentToolCallId: "t1"}),
		ev("agent", &bandav1.ToolResult{Output: "secret output"}),
		ev("agent", &bandav1.AgentText{Text: "done"}),
		ev("agent", &bandav1.TurnEnded{IsError: true, Error: "boom"}),
		ev("agent:abc", &bandav1.Message{Text: "fyi"}),
		ev("system", &bandav1.Notice{Text: "log in"}),
	}
	want := "Ann: fix it\nagent called tool Bash\nagent: done\nturn ended with error: boom\nagent in thread abc: fyi\nnotice: log in"
	if got := transcript(names, events, 50); got != want {
		t.Errorf("transcript:\n%q\nwant\n%q", got, want)
	}
	if got := transcript(names, events, 2); got != "agent in thread abc: fyi\nnotice: log in" {
		t.Errorf("limited transcript: %q", got)
	}
	if got := transcript(names, nil, 50); got != "" {
		t.Errorf("empty transcript: %q", got)
	}
}

// The server's instructions, which Claude Code puts into the agent's system
// prompt, name every tool.
func TestMCPInstructions(t *testing.T) {
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	if _, err := (&Service{}).mcpServer(store.Thread{}).Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) < 6 {
		t.Fatalf("tools: %v, %v", tools, err)
	}
	for _, tool := range tools.Tools {
		if !strings.Contains(cs.InitializeResult().Instructions, tool.Name) {
			t.Errorf("instructions %q lack %s", cs.InitializeResult().Instructions, tool.Name)
		}
	}
}
