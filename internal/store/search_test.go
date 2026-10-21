package store

import "testing"

func TestTsquery(t *testing.T) {
	for in, want := range map[string]string{
		"":                           "",
		"  !&|() ":                   "",
		"auth setup":                 "auth:* & setup:*",
		"hello1.txt, web/src/lib.":   "hello1.txt:* & web/src/lib:*",
		"don't 'quote' a:b !x <-> y": "don:* & t:* & quote:* & a:* & b:* & x:* & y:*",
		"Größe café":                 "Größe:* & café:*",
	} {
		if got := tsquery(in); got != want {
			t.Errorf("tsquery(%q) = %q, want %q", in, got, want)
		}
	}
}
