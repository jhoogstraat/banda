package api

import (
	"strings"
	"testing"
	"time"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// A git credential handed to a thread's runner is redacted from what the
// runner sends back: a turn's tool output, its text, terminal output. Other
// threads, expired credentials and preview traffic are left alone.
func TestRedactRunner(t *testing.T) {
	s := &Service{}
	const tok, old = "0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210"
	s.handedOut.remember("t1", old, time.Now().Add(time.Hour))
	s.handedOut.remember("t1", tok, time.Now().Add(time.Hour)) // after a rotation: both
	s.handedOut.remember("t1", "expired-0123456789", time.Now().Add(-time.Second))

	tool := &bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_Event{Event: &bandav1.Event{Kind: &bandav1.Event_ToolResult{
		ToolResult: &bandav1.ToolResult{Output: "password=" + tok + "\nold=" + old + " expired-0123456789"}}}}}
	s.redactRunner("t1", tool)
	if got := tool.GetEvent().GetToolResult().Output; got != "password=[redacted]\nold=[redacted] expired-0123456789" {
		t.Errorf("tool output: %q", got)
	}
	term := &bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_TerminalOutput{TerminalOutput: &bandav1.TerminalOutput{Data: []byte("x" + tok)}}}
	s.redactRunner("t1", term)
	if got := string(term.GetTerminalOutput().Data); got != "x[redacted]" {
		t.Errorf("terminal: %q", got)
	}
	text := &bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_Event{Event: &bandav1.Event{Kind: &bandav1.Event_AgentText{AgentText: &bandav1.AgentText{Text: tok}}}}}
	s.redactRunner("t2", text)
	if text.GetEvent().GetAgentText().Text != tok {
		t.Error("another thread's text was redacted")
	}
	tunnel := &bandav1.ConnectRequest{Kind: &bandav1.ConnectRequest_HttpTunnel{HttpTunnel: &bandav1.HttpTunnel{}}}
	s.redactRunner("t1", tunnel) // nothing to do, and no panic
	if v := s.handedOut.live("t1"); len(v) != 2 || !strings.Contains(strings.Join(v, " "), tok) {
		t.Errorf("live: %v", v)
	}
}
