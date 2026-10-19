package api

import "testing"

func TestCheckSecretName(t *testing.T) {
	for name, ok := range map[string]bool{
		"GIT_TOKEN": true, "_X": true, "NPM_TOKEN_2": true,
		"": false, "2FA": false, "lower": false, "A-B": false, "A B": false,
		"ANTHROPIC_API_KEY": false, "CLAUDE_CODE_OAUTH_TOKEN": false, "PATH": false, "HOME": false,
		"BANDA_THREAD_ID": false, "BANDA_": false, "ANTHROPIC_MODEL": false, "CLAUDE_X": false, "OPENAI_API_KEY": false,
	} {
		if err := checkSecretName(name); (err == nil) != ok {
			t.Errorf("%q: got %v, want ok=%v", name, err, ok)
		}
	}
}

func TestCheckNotOAuth(t *testing.T) {
	for v, ok := range map[string]bool{"sk-ant-oat01-abc": false, "x sk-ant-ort01-abc": false, "sk-ant-api03-abc": true, "hunter2": true, "": true} {
		if err := checkNotOAuth(v); (err == nil) != ok {
			t.Errorf("%q: got %v, want ok=%v", v, err, ok)
		}
	}
}
