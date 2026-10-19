package main

import (
	"os"
	"path/filepath"
	"testing"
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
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "DB_PASSWORD"), []byte("hunter2hunter2\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "DB_URL"), []byte("postgres://u:hunter2hunter2@db"), 0o644)
	os.WriteFile(filepath.Join(dir, "SHORT"), []byte("abc"), 0o644)
	os.Mkdir(filepath.Join(dir, "..data"), 0o755)
	t.Cleanup(func() { userSecrets = nil })
	userSecrets = loadSecrets(dir)
	t.Setenv("ANTHROPIC_API_KEY", "proxy-key-123")

	in := "url=postgres://u:hunter2hunter2@db pw=hunter2hunter2 key=proxy-key-123 abc"
	want := "url=[redacted] pw=[redacted] key=[redacted] abc"
	if got := redact(in); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}
