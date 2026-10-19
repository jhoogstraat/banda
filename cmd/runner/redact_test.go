package main

import "testing"

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
