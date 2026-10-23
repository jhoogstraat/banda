package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/gen/banda/v1/bandav1connect"
)

// newAPI is a client for the API's RunnerService, over h2c.
func newAPI() bandav1connect.RunnerServiceClient {
	tr := &http.Transport{Protocols: new(http.Protocols)}
	tr.Protocols.SetUnencryptedHTTP2(true)
	return bandav1connect.NewRunnerServiceClient(connect.NewClient(connecthttp.NewTransport(&http.Client{Transport: tr}, os.Getenv("BANDA_API_URL"))))
}

// askCredential asks the API for a short-lived token for the thread's
// repository (the GitHub App's, see internal/api/github.go; GitLab's git-only
// OAuth token, internal/api/gitlab.go). A variable for tests.
var askCredential = func(ctx context.Context) (*bandav1.GitCredentialResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return newAPI().GitCredential(ctx, &bandav1.GitCredentialRequest{ThreadId: threadID, Token: os.Getenv("BANDA_RUNNER_TOKEN")})
}

// refusal is why the API gave no credential, for a person to act on.
func refusal(err error) string {
	var ce *connect.Error
	if errors.As(err, &ce) && ce.Code() == connect.CodeFailedPrecondition {
		return ce.Message()
	}
	return "Banda couldn't give git a credential: " + err.Error()
}

// gitCredential is git's credential helper for the repository's host (the
// runner binary with "git-credential", see checkout). It answers get with the
// API's token, never written anywhere else; a refusal goes to stderr, which
// git shows (a failed push's detail), and git asks the next helper:
// GIT_TOKEN's, if the owner has one.
func gitCredential(action string) {
	io.Copy(io.Discard, os.Stdin)
	if action != "get" {
		return // store, erase: nothing is kept
	}
	c, err := askCredential(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "Banda: "+refusal(err))
		return
	}
	fmt.Printf("username=%s\npassword=%s\n", c.Username, c.Password)
	if c.ExpiresAt != nil {
		fmt.Printf("password_expiry_utc=%d\n", c.ExpiresAt.AsTime().Unix())
	}
}

// pushBlocked is why origin can't be pushed to, "" if it can (or may):
// https needs GIT_TOKEN or a credential from the API; there are no SSH keys
// in the sandbox.
func pushBlocked(ctx context.Context, repo string) string {
	const noToken = "an https remote needs a GIT_TOKEN secret"
	if strings.HasPrefix(repo, "git@") {
		return noToken
	}
	u, err := url.Parse(repo)
	if err != nil {
		return noToken
	}
	switch u.Scheme {
	case "https", "http":
		if os.Getenv("GIT_TOKEN") != "" {
			return ""
		}
		if _, err := askCredential(ctx); err != nil {
			return refusal(err)
		}
		return ""
	case "ssh":
		return noToken
	}
	return "" // file://, git://, a path
}
