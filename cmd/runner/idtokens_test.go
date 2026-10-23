package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// jwt is an unsigned token expiring at exp: the runner only reads exp.
func jwt(exp time.Time, tag string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc(fmt.Appendf(nil, `{"exp":%d,"jti":%q}`, exp.Unix(), tag)) + ".c2ln"
}

func tokenFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		out[e.Name()] = string(b)
	}
	return out
}

// Fetched tokens replace the files, removed audiences' files go, expired
// ones go when the API can't be reached, and current values are redacted.
func TestIdTokens(t *testing.T) {
	dir := t.TempDir()
	it := &idTokens{dir: dir, known: map[string]time.Time{}}
	soon := time.Now().Add(15 * time.Minute)
	tok := func(aud, file string, exp time.Time) *bandav1.IdToken {
		return &bandav1.IdToken{Audience: aud, File: file, Token: jwt(exp, aud), ExpiresAt: timestamppb.New(exp)}
	}
	a, b := tok("vault", "vault", soon), tok("sts.amazonaws.com", "sts.amazonaws.com", soon)
	it.write(&bandav1.IdTokensResponse{Tokens: []*bandav1.IdToken{a, b, tok("x", "../escape", soon), tok("y", ".hidden", soon)}})
	if got := tokenFiles(t, dir); len(got) != 2 || got["vault"] != a.Token || got["sts.amazonaws.com"] != b.Token {
		t.Fatalf("files %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "escape")); err == nil {
		t.Fatal("wrote outside the directory")
	}
	if got := redactWith(it, "vault says "+a.Token); strings.Contains(got, a.Token) {
		t.Errorf("not redacted: %s", got)
	}

	a2 := tok("vault", "vault", soon.Add(time.Minute))
	it.write(&bandav1.IdTokensResponse{Tokens: []*bandav1.IdToken{a2}})
	if got := tokenFiles(t, dir); len(got) != 1 || got["vault"] != a2.Token {
		t.Fatalf("after a refresh without sts: %v", got)
	}
	if v := it.values(); !slices.Contains(v, a.Token) || !slices.Contains(v, a2.Token) {
		t.Error("an earlier token still valid is no longer redacted")
	}

	// The API is away and the token runs out.
	it.scan(soon.Add(2 * time.Minute))
	if got := tokenFiles(t, dir); len(got) != 0 {
		t.Fatalf("expired token kept: %v", got)
	}
	// A restarted container redacts what it finds.
	os.WriteFile(filepath.Join(dir, "vault"), []byte(a2.Token), 0o600)
	fresh := &idTokens{dir: dir, known: map[string]time.Time{}}
	fresh.scan(time.Now())
	if !slices.Contains(fresh.values(), a2.Token) {
		t.Error("a token found at start isn't redacted")
	}
	it.write(&bandav1.IdTokensResponse{})
	if got := tokenFiles(t, dir); len(got) != 0 {
		t.Fatalf("no audiences left files: %v", got)
	}
}

func redactWith(it *idTokens, s string) string {
	old := ids
	ids = it
	defer func() { ids = old }()
	return redact(s)
}
