package main

import (
	"os"
	"regexp"
	"strings"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// Anthropic keys and OAuth tokens (sk-ant-api…, sk-ant-oat…, sk-ant-ort…).
var anthropicToken = regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{8,}`)

// redact removes credentials the agent can reach in its sandbox (the owner's
// Claude login, the git and runner tokens) from text that leaves the pod, so
// they never end up in Banda's database or in front of teammates.
func redact(s string) string {
	s = anthropicToken.ReplaceAllString(s, "[redacted]")
	for _, v := range []string{os.Getenv("GIT_TOKEN"), os.Getenv("BANDA_RUNNER_TOKEN")} {
		if len(v) >= 8 {
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
	}
	return strings.ToValidUTF8(s, "�") // protobuf strings must be UTF-8
}

func scrub(e *bandav1.Event) {
	switch k := e.Kind.(type) {
	case *bandav1.Event_AgentText:
		k.AgentText.Text = redact(k.AgentText.Text)
	case *bandav1.Event_AgentTextDelta:
		k.AgentTextDelta.Text = redact(k.AgentTextDelta.Text)
	case *bandav1.Event_AgentThinking:
		k.AgentThinking.Text = redact(k.AgentThinking.Text)
	case *bandav1.Event_ToolCall:
		k.ToolCall.InputJson = redact(k.ToolCall.InputJson)
	case *bandav1.Event_ToolResult:
		k.ToolResult.Output = redact(k.ToolResult.Output)
	case *bandav1.Event_TurnEnded:
		k.TurnEnded.Error = redact(k.TurnEnded.Error)
	case *bandav1.Event_Notice:
		k.Notice.Text = redact(k.Notice.Text)
	}
}
