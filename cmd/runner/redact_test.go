package main

import (
	"strings"
	"testing"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

func TestRedact(t *testing.T) {
	t.Setenv("GIT_TOKEN", "ghp_secretsecret")
	in := `{"claudeAiOauth":{"accessToken":"sk-ant-oat01-AbC_d-123456789","refreshToken":"sk-ant-ort01-xyz987654321"}} token=ghp_secretsecret bad:` + "\xff"
	want := `{"claudeAiOauth":{"accessToken":"[redacted]","refreshToken":"[redacted]"}} token=[redacted] bad:` + "�"
	if got := redact(in); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestRedactUserSecrets(t *testing.T) {
	t.Setenv("BANDA_SECRET_NAMES", "DB_PASSWORD,DB_URL,SHORT,MULTILINE,UNSET")
	t.Setenv("DB_PASSWORD", "hunter2hunter2")
	t.Setenv("DB_URL", "postgres://u:hunter2hunter2@db")
	t.Setenv("SHORT", "abc")
	t.Setenv("MULTILINE", "line1\n\"quoted\"\tx")
	t.Setenv("ANTHROPIC_API_KEY", "proxy-key-123")

	in := `url=postgres://u:hunter2hunter2@db pw=hunter2hunter2 key=proxy-key-123 abc {"value":"line1\n\"quoted\"\tx"}`
	want := `url=[redacted] pw=[redacted] key=[redacted] abc {"value":"[redacted]"}`
	if got := redact(in); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// A permission card shows the command it asks for, so it is redacted like the tool call.
func TestScrubPermissionRequest(t *testing.T) {
	t.Setenv("GIT_TOKEN", "ghp_secretsecret")
	e := &bandav1.Event{Kind: &bandav1.Event_PermissionRequested{PermissionRequested: &bandav1.PermissionRequested{
		ToolName: "Bash", InputJson: `{"command":"git push https://x:ghp_secretsecret@host"}`, Reason: "uses ghp_secretsecret"}}}
	scrub(e)
	if r := e.GetPermissionRequested(); strings.Contains(r.InputJson+r.Reason, "ghp_") {
		t.Errorf("not redacted: %v", r)
	}
}
