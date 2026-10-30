// Command api is the Banda control plane: the Connect API for the web client
// and the runners, plus the static web build when WEB_DIR is set.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/jhoogstraat/banda/gen/banda/v1/bandav1connect"
	"github.com/jhoogstraat/banda/internal/api"
	"github.com/jhoogstraat/banda/internal/forge"
	"github.com/jhoogstraat/banda/internal/identity"
	"github.com/jhoogstraat/banda/internal/sandbox"
	"github.com/jhoogstraat/banda/internal/store"
)

func main() {
	ctx := context.Background()
	st, err := store.Open(ctx, env("DATABASE_URL", "postgres://banda:banda@localhost:5432/banda"))
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	sizes, err := sandbox.ParseSizes(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	sb, err := sandbox.New(sandbox.Config{
		Context:   os.Getenv("KUBE_CONTEXT"),
		Namespace: env("SANDBOX_NAMESPACE", "banda"),
		Image:     env("SANDBOX_IMAGE", "localhost/banda-sandbox:dev"),
		APIURL:    env("RUNNER_API_URL", "http://host.containers.internal:8787"),
		Sizes:     sizes,
	})
	if err != nil {
		log.Fatalf("sandbox client: %v", err)
	}
	allow, err := sandbox.ParseCIDRs(os.Getenv("SANDBOX_EGRESS_ALLOW"))
	if err == nil {
		err = sb.AllowEgress(ctx, allow)
	}
	if err != nil {
		log.Fatalf("SANDBOX_EGRESS_ALLOW: %v", err)
	}
	// Before serving: with one replica (and Recreate) no other API creates
	// sandboxes now; the sweep's own age check covers the e2e's side by side.
	// ponytail: one-off for Secrets from before start set their owner; drop once no install has them.
	if n, err := sb.SweepTokens(ctx); err != nil {
		log.Printf("sweep token Secrets: %v", err) // e.g. a Role without list/delete: they just linger
	} else if n > 0 {
		log.Printf("deleted %d token Secrets of deleted sandboxes", n)
	}
	started := time.Now()
	publicURL := strings.TrimRight(env("PUBLIC_URL", "http://localhost:5173"), "/")
	keys, err := sb.IdentityKeys(ctx, identity.FirstKey)
	if err != nil {
		log.Fatalf("workload identity keys (Secret banda-identity): %v", err)
	}
	issuer, err := identity.New(publicURL, keys)
	if err != nil {
		log.Fatalf("workload identity keys (Secret banda-identity): %v", err)
	}

	auth, httpUser := api.DevAuth(), api.DevUser
	var sso *api.OIDC
	if issuer := os.Getenv("OIDC_ISSUER"); issuer != "" {
		sso, err = api.NewOIDC(ctx, st, api.OIDCConfig{
			Issuer:        issuer,
			ClientID:      os.Getenv("OIDC_CLIENT_ID"),
			ClientSecret:  os.Getenv("OIDC_CLIENT_SECRET"),
			PublicURL:     publicURL,
			SessionSecret: os.Getenv("SESSION_SECRET"),
			DexURL:        os.Getenv("DEX_URL"),
			Provider:      os.Getenv("OIDC_PROVIDER_NAME"),
		})
		if err != nil {
			log.Fatalf("oidc: %v", err)
		}
		auth, httpUser = sso.Interceptor(), sso.User
	} else if os.Getenv("DEV_AUTH") != "1" {
		log.Fatal("set OIDC_ISSUER to enable sign-in, or DEV_AUTH=1 to trust the X-Banda-User header (local development only)")
	}
	server := connect.NewServer(auth, api.Errors())
	svc := api.New(st, sb)
	svc.Identity = issuer
	bandav1connect.RegisterThreadServiceHandler(server, svc)
	bandav1connect.RegisterRunnerServiceHandler(server, svc)
	bandav1connect.RegisterUserServiceHandler(server, svc)
	bandav1connect.RegisterSettingsServiceHandler(server, svc)
	idle, err := time.ParseDuration(env("IDLE_SUSPEND_AFTER", "30m"))
	if err != nil {
		log.Fatalf("IDLE_SUSPEND_AFTER: %v", err)
	}
	if svc.DeleteSettledAfter, err = time.ParseDuration(env("DELETE_SETTLED_AFTER", "168h")); err != nil {
		log.Fatalf("DELETE_SETTLED_AFTER: %v", err)
	}
	if svc.DeleteIdleAfter, err = time.ParseDuration(env("DELETE_IDLE_AFTER", "720h")); err != nil {
		log.Fatalf("DELETE_IDLE_AFTER: %v", err)
	}
	if svc.LoginIdle, err = time.ParseDuration(env("LOGIN_IDLE_AFTER", "10m")); err != nil {
		log.Fatalf("LOGIN_IDLE_AFTER: %v", err)
	}
	if svc.MaxSubthreadDepth, err = strconv.Atoi(env("MAX_SUBTHREAD_DEPTH", "3")); err != nil {
		log.Fatalf("MAX_SUBTHREAD_DEPTH: %v", err)
	}
	if svc.MaxAgentThreads, err = strconv.Atoi(env("MAX_AGENT_THREADS", "8")); err != nil {
		log.Fatalf("MAX_AGENT_THREADS: %v", err)
	}
	if svc.OverlapWindow, err = time.ParseDuration(env("OVERLAP_WINDOW", "168h")); err != nil {
		log.Fatalf("OVERLAP_WINDOW: %v", err)
	}
	if svc.WatchBuffer, err = strconv.Atoi(env("WATCH_BUFFER", "256")); err != nil {
		log.Fatalf("WATCH_BUFFER: %v", err)
	}
	every, err := time.ParseDuration(env("IDLE_SWEEP_EVERY", "1m"))
	if err != nil {
		log.Fatalf("IDLE_SWEEP_EVERY: %v", err)
	}
	// Tests only: holds a suspend between the thread's status and its environment.
	if svc.SuspendPause, err = time.ParseDuration(env("TEST_SUSPEND_PAUSE", "0s")); err != nil {
		log.Fatalf("TEST_SUSPEND_PAUSE: %v", err)
	}
	// Tests only: how often server streams send a keepalive (default 25s).
	if v := os.Getenv("TEST_KEEPALIVE_EVERY"); v != "" {
		if svc.Keepalive, err = time.ParseDuration(v); err != nil || svc.Keepalive <= 0 {
			log.Fatalf("TEST_KEEPALIVE_EVERY: %q is not a positive duration", v)
		}
	}
	// Tests only: how often runners fetch identity tokens (default 5m).
	if v := os.Getenv("TEST_ID_TOKEN_REFRESH"); v != "" {
		if svc.IDTokenRefresh, err = time.ParseDuration(v); err != nil || svc.IDTokenRefresh < time.Second {
			log.Fatalf("TEST_ID_TOKEN_REFRESH: %q is not a duration of at least 1s", v)
		}
	}
	go svc.SuspendIdle(ctx, idle, every)
	hosts, err := forge.ParseHosts(os.Getenv("FORGE_HOSTS"))
	if err != nil {
		log.Fatalf("FORGE_HOSTS: %v", err)
	}
	if err := testHooks(os.Getenv("TEST_FORGE_URL"), os.Getenv("TEST_GITHUB_APP_SECRET"), os.Getenv("TEST_GITLAB_APP_SECRET")); err != nil {
		log.Fatal(err)
	}
	// TEST_FORGE_URL: tests only, a fake forge API in place of every forge's.
	api.Forges = forge.Config{Hosts: hosts, TestURL: os.Getenv("TEST_FORGE_URL"), Refused: svc.TokenRefused}
	svc.PublicURL = env("PUBLIC_URL", "http://localhost:5173")
	// Tests only: the GitHub App's Secret, for APIs side by side in one namespace.
	api.GitHubAppSecret = env("TEST_GITHUB_APP_SECRET", api.GitHubAppSecret)
	// Tests only: the prefix of the GitLab applications' Secrets, likewise.
	api.GitLabAppSecret = env("TEST_GITLAB_APP_SECRET", api.GitLabAppSecret)
	poll, err := time.ParseDuration(env("PULL_REQUEST_POLL", "30s"))
	if err != nil {
		log.Fatalf("PULL_REQUEST_POLL: %v", err)
	}
	go svc.PollPullRequests(ctx, poll)
	svc.Preview = api.PreviewConfig{Addr: env("PREVIEW_ADDR", ":8788"), URL: os.Getenv("PREVIEW_URL")}
	go func() {
		preview := &http.Server{Addr: svc.Preview.Addr, Handler: svc.PreviewHandler(), ReadHeaderTimeout: 10 * time.Second}
		log.Printf("preview on %s", preview.Addr)
		log.Printf("preview disabled: %v", preview.ListenAndServe()) // e.g. the port is taken: the rest still works
	}()
	if addr := os.Getenv("METRICS_ADDR"); addr != "" {
		go func() {
			mux := http.NewServeMux()
			mux.Handle("/metrics", svc.MetricsHandler(started))
			metrics := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
			log.Printf("metrics on %s", addr)
			log.Printf("metrics disabled: %v", metrics.ListenAndServe())
		}()
	}
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	if sso != nil {
		sso.Mount(mux)
	}
	mux.Handle("GET "+identity.DiscoveryPath, issuer.Handler()) // public: secret managers and clouds verify tokens with it
	mux.Handle("GET "+identity.JWKSPath, issuer.Handler())
	mux.Handle("/mcp", svc.MCPHandler())
	mux.Handle("/github/", svc.GitHubHandler(httpUser))
	mux.Handle("/gitlab/", svc.GitLabHandler(httpUser))
	mux.Handle("/files", svc.FilesHandler(httpUser))
	mux.Handle("/files/", svc.FilesHandler(httpUser))
	if dir := os.Getenv("WEB_DIR"); dir != "" {
		mux.Handle("/", spa(dir))
	}

	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true) // runners use bidi streaming over h2c
	srv := &http.Server{Addr: env("ADDR", ":8787"), Handler: mux, Protocols: p, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("listening on %s", srv.Addr)
	log.Fatal(srv.ListenAndServe())
}

// spa serves the static web build, falling back to index.html for client routes.
func spa(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", api.FrameAncestors)
		if _, err := os.Stat(filepath.Join(dir, filepath.Clean("/"+r.URL.Path))); err != nil {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		files.ServeHTTP(w, r)
	})
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// testHooks refuses test settings that would hand credentials elsewhere if
// set in a deployment: the fake forge gets users' tokens and the app's JWTs,
// so it must be on this machine, and the apps' Secrets must stay their own
// (no pod mounts banda-github-app* or banda-gitlab-app*, see the chart).
func testHooks(forgeURL, appSecret, gitlabSecret string) error {
	if forgeURL != "" {
		u, err := url.Parse(forgeURL)
		if err != nil || u.Scheme != "http" && u.Scheme != "https" || !slices.Contains([]string{"127.0.0.1", "::1", "localhost"}, u.Hostname()) {
			return fmt.Errorf("TEST_FORGE_URL: %q isn't on this machine (127.0.0.1, ::1 or localhost): it is for tests only, and forges get users' tokens", forgeURL)
		}
	}
	if appSecret != "" && !strings.HasPrefix(appSecret, "banda-github-app") {
		return fmt.Errorf("TEST_GITHUB_APP_SECRET: %q doesn't start with banda-github-app: it is for tests only", appSecret)
	}
	if gitlabSecret != "" && !strings.HasPrefix(gitlabSecret, "banda-gitlab-app") {
		return fmt.Errorf("TEST_GITLAB_APP_SECRET: %q doesn't start with banda-gitlab-app: it is for tests only", gitlabSecret)
	}
	return nil
}
