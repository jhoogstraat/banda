// Command api is the Banda control plane: the Connect API for the web client
// and the runners, plus the static web build when WEB_DIR is set.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/jhoogstraat/banda/gen/banda/v1/bandav1connect"
	"github.com/jhoogstraat/banda/internal/api"
	"github.com/jhoogstraat/banda/internal/sandbox"
	"github.com/jhoogstraat/banda/internal/store"
)

func main() {
	ctx := context.Background()
	st, err := store.Open(ctx, env("DATABASE_URL", "postgres://banda:banda@localhost:5432/banda"))
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	sb, err := sandbox.New(sandbox.Config{
		Namespace: env("SANDBOX_NAMESPACE", "banda"),
		Image:     env("SANDBOX_IMAGE", "localhost/banda-sandbox:dev"),
		APIURL:    env("RUNNER_API_URL", "http://host.containers.internal:8787"),
	})
	if err != nil {
		log.Fatalf("sandbox client: %v", err)
	}

	auth := api.DevAuth()
	var sso *api.OIDC
	if issuer := os.Getenv("OIDC_ISSUER"); issuer != "" {
		sso, err = api.NewOIDC(ctx, st, api.OIDCConfig{
			Issuer:        issuer,
			ClientID:      os.Getenv("OIDC_CLIENT_ID"),
			ClientSecret:  os.Getenv("OIDC_CLIENT_SECRET"),
			PublicURL:     env("PUBLIC_URL", "http://localhost:5173"),
			SessionSecret: os.Getenv("SESSION_SECRET"),
		})
		if err != nil {
			log.Fatalf("oidc: %v", err)
		}
		auth = sso.Interceptor()
	} else if os.Getenv("DEV_AUTH") != "1" {
		log.Fatal("set OIDC_ISSUER to enable sign-in, or DEV_AUTH=1 to trust the X-Banda-User header (local development only)")
	}
	server := connect.NewServer(auth)
	svc := api.New(st, sb)
	bandav1connect.RegisterThreadServiceHandler(server, svc)
	bandav1connect.RegisterRunnerServiceHandler(server, svc)
	bandav1connect.RegisterUserServiceHandler(server, svc)
	idle, err := time.ParseDuration(env("IDLE_SUSPEND_AFTER", "30m"))
	if err != nil {
		log.Fatalf("IDLE_SUSPEND_AFTER: %v", err)
	}
	go svc.SuspendIdle(ctx, idle)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	if sso != nil {
		sso.Mount(mux)
	}
	mux.Handle("/mcp", svc.MCPHandler())
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
