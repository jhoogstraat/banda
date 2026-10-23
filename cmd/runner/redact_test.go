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

// GitHub's and GitLab's tokens are redacted by their shape: an installation token from the
// API is in no variable.
func TestRedactGitHubTokens(t *testing.T) {
	for _, tok := range []string{"ghs_16C7e42F292c6912E7710c838347Ae178B4a", "ghu_abcdefghijklmnop1234", "ghr_1B4a2e77838347a7E420ce178F2E7c6912E1",
		"ghp_abcdefghijklmnopqrstuvwxyz0123456789", "gho_abcdefghijklmnop1234", "github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz",
		"glpat-xYz12_AbCdEfGhIjKlMnOp", "gloas-0123456789abcdef0123456789abcdef"} {
		if got := redact("password=" + tok + "\n"); got != "password=[redacted]\n" {
			t.Errorf("%s: got %q", tok, got)
		}
	}
	if got := redact("ghost_town ghs_short"); got != "ghost_town ghs_short" {
		t.Errorf("ordinary text: got %q", got)
	}
}
