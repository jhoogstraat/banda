package main

import "testing"

func TestGithubRepo(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/o/r":      "o/r",
		"https://github.com/o/r.git":  "o/r",
		"git@github.com:o/r.git":      "o/r",
		"https://github.com/o/r.git/": "o/r",
		"https://gitlab.com/o/r.git":  "",
		"/tmp/origin.git":             "",
	} {
		got := ""
		if m := githubRepo.FindStringSubmatch(url); m != nil {
			got = m[1]
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", url, got, want)
		}
	}
}
