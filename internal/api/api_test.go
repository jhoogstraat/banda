package api

import "testing"

// Every teammate sees a thread's repository URL, so it can't carry a token.
func TestCheckRepoURL(t *testing.T) {
	for repo, ok := range map[string]bool{
		"https://github.com/acme/app.git":                    true,
		"git@github.com:acme/app.git":                        true,
		"ssh://git@github.com/acme/app.git":                  true,
		"git://git-daemon/repo":                              true,
		"https://x-access-token:ghp_abc@github.com/acme/app": false,
		"https://ghp_abc@github.com/acme/app":                false,
		"http://user:pw@git.local/app":                       false,
	} {
		if err := checkRepoURL(repo); (err == nil) != ok {
			t.Errorf("checkRepoURL(%q) = %v", repo, err)
		}
	}
}
