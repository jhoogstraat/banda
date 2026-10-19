package main

import (
	"cmp"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// Anthropic keys and OAuth tokens (sk-ant-api…, sk-ant-oat…, sk-ant-ort…).
var anthropicToken = regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{8,}`)

// secretsDir mirrors the owner's secrets as files (internal/sandbox): the
// runner can't tell them apart from other variables in its environment.
const secretsDir = "/etc/banda/secrets"

// userSecrets are the values of the owner's secrets, read at start like the
// environment.
var userSecrets []string

func loadSecrets(dir string) []string {
	var out []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") { // the Secret volume's own ..data links
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
			out = append(out, strings.TrimSpace(string(b)))
		}
	}
	return out
}

// redact removes credentials the agent can reach in its sandbox (the owner's
// Claude login, API key and secrets, the git and runner tokens) from text that
// leaves the pod, so they never end up in Banda's database or in front of
// teammates.
func redact(s string) string {
	s = anthropicToken.ReplaceAllString(s, "[redacted]")
	values := append([]string{os.Getenv("GIT_TOKEN"), os.Getenv("BANDA_RUNNER_TOKEN"), os.Getenv("ANTHROPIC_API_KEY")}, userSecrets...)
	slices.SortFunc(values, func(a, b string) int { return cmp.Compare(len(b), len(a)) }) // a value may contain another
	for _, v := range values {
		if len(v) >= 4 { // shorter ones would redact ordinary text
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
	case *bandav1.Event_SetupStep:
		k.SetupStep.Output = redact(k.SetupStep.Output)
	}
}
