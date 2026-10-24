package main

import (
	"cmp"
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// Anthropic keys and OAuth tokens (sk-ant-api…, sk-ant-oat…, sk-ant-ort…),
// GitHub's tokens: installation (ghs_, which git gets from the API),
// user (ghu_, ghr_, gho_), personal (ghp_, github_pat_), and GitLab's
// personal (glpat-) and application secrets (gloas-). GitLab's OAuth tokens
// have no shape to match: git's comes from the API at use time.
var (
	anthropicToken = regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{8,}`)
	gitHubToken    = regexp.MustCompile(`\b(?:gh[opsur]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{16,}|gl(?:pat|oas)-[A-Za-z0-9_-]{16,})`)
)

// redact removes credentials the agent can reach in its sandbox (the owner's
// Claude login, API key and secrets, the team's secrets, the git and runner
// tokens, identity tokens) from text that leaves the pod, so they never end up in Banda's
// database or in front of teammates. The secrets are the variables named in
// BANDA_SECRET_NAMES; the pod's environment is fixed at start.
func redact(s string) string {
	s = anthropicToken.ReplaceAllString(s, "[redacted]")
	s = gitHubToken.ReplaceAllString(s, "[redacted]")
	var values []string
	for _, name := range append([]string{"GIT_TOKEN", "BANDA_RUNNER_TOKEN", "ANTHROPIC_API_KEY"}, strings.Split(os.Getenv("BANDA_SECRET_NAMES"), ",")...) {
		if v := os.Getenv(name); len(v) >= 4 { // shorter ones would redact ordinary text
			values = append(values, v, jsonEscaped(v)) // tool inputs are raw JSON
		}
	}
	values = append(values, ids.values()...)                                              // JWTs need no JSON escaping
	slices.SortFunc(values, func(a, b string) int { return cmp.Compare(len(b), len(a)) }) // a value may contain another
	for _, v := range values {
		s = strings.ReplaceAll(s, v, "[redacted]")
	}
	return strings.ToValidUTF8(s, "�") // protobuf strings must be UTF-8
}

// jsonEscaped is v as it appears inside a JSON string.
func jsonEscaped(v string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	out := strings.TrimSpace(b.String())
	return out[1 : len(out)-1]
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
	case *bandav1.Event_AutoPush:
		k.AutoPush.Detail = redact(k.AutoPush.Detail)
	case *bandav1.Event_PlanChanged:
		for _, i := range k.PlanChanged.Items {
			i.Content, i.ActiveForm = redact(i.Content), redact(i.ActiveForm)
		}
	case *bandav1.Event_PermissionRequested:
		k.PermissionRequested.InputJson = redact(k.PermissionRequested.InputJson)
		k.PermissionRequested.Reason = redact(k.PermissionRequested.Reason)
	}
}
