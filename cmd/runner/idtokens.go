package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
)

// idTokens keeps the thread's workload identity tokens in dir, a file per
// audience, in memory (the pod's id-tokens volume): fetched when the API says
// they changed (also on connect) and before they run out. Their values are
// redacted like secrets.
type idTokens struct {
	dir   string
	mu    sync.Mutex
	known map[string]time.Time // token -> expiry, for redact
}

var ids = &idTokens{dir: os.Getenv("BANDA_ID_TOKEN_DIR"), known: map[string]time.Time{}}

// run fetches on every nudge and every refresh until ctx ends. A failed
// fetch keeps the tokens until they expire, and retries sooner.
func (it *idTokens) run(ctx context.Context, fetch func(context.Context) (*bandav1.IdTokensResponse, error), nudge <-chan struct{}) {
	timer := time.NewTimer(time.Hour)
	for {
		select {
		case <-ctx.Done():
			return
		case <-nudge:
		case <-timer.C:
		}
		next := time.Minute
		res, err := fetch(ctx)
		if err != nil {
			log.Print("identity tokens: ", err)
			it.scan(time.Now())
		} else {
			it.write(res)
			next = max(time.Duration(res.RefreshSeconds)*time.Second, time.Second)
		}
		timer.Reset(next)
	}
}

// write replaces the files with res's tokens, each atomically, and removes
// the rest.
func (it *idTokens) write(res *bandav1.IdTokensResponse) {
	keep := map[string]bool{}
	for _, t := range res.Tokens {
		name := filepath.Base(t.File)
		if name != t.File || strings.HasPrefix(name, ".") {
			continue // the API names plain files
		}
		it.remember(t.Token, t.ExpiresAt.AsTime())
		tmp, err := os.CreateTemp(it.dir, ".tmp-")
		if err == nil {
			_, err = tmp.WriteString(t.Token)
			if cerr := tmp.Close(); err == nil {
				err = cerr
			}
			if err == nil {
				err = os.Rename(tmp.Name(), filepath.Join(it.dir, name))
			}
			if err != nil {
				os.Remove(tmp.Name())
			}
		}
		if err != nil {
			log.Print("identity token for ", t.Audience, ": ", err)
			continue
		}
		keep[name] = true
	}
	entries, _ := os.ReadDir(it.dir)
	for _, e := range entries {
		if !keep[e.Name()] {
			os.Remove(filepath.Join(it.dir, e.Name()))
		}
	}
}

// scan remembers the tokens in dir and removes those expired at now: a
// restarted container finds its last ones there, also while the API is away.
func (it *idTokens) scan(now time.Time) {
	entries, _ := os.ReadDir(it.dir)
	for _, e := range entries {
		p := filepath.Join(it.dir, e.Name())
		b, err := os.ReadFile(p)
		exp := jwtExpiry(string(b))
		if err != nil || !exp.After(now) {
			os.Remove(p)
			continue
		}
		it.remember(string(b), exp)
	}
}

func (it *idTokens) remember(token string, exp time.Time) {
	it.mu.Lock()
	defer it.mu.Unlock()
	it.known[token] = exp
}

// values are the tokens not yet expired; expired ones are forgotten.
func (it *idTokens) values() []string {
	it.mu.Lock()
	defer it.mu.Unlock()
	var out []string
	for t, exp := range it.known {
		if time.Now().After(exp) {
			delete(it.known, t)
		} else {
			out = append(out, t)
		}
	}
	return out
}

// jwtExpiry reads a JWT's exp claim; zero if it has none.
func jwtExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	var c struct{ Exp int64 }
	if err != nil || json.Unmarshal(payload, &c) != nil || c.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(c.Exp, 0)
}
