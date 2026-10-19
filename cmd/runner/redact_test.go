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
