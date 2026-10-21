package api

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/gen/banda/v1/bandav1connect"
	"github.com/jhoogstraat/banda/internal/thread"
)

// The preview shows what listens on a port in a thread's environment, on its
// own origin so that apps keep their absolute paths (/assets/x.js) and can't
// script Banda. A cookie on that origin selects the thread and port; it is set
// by /__banda/open from a short-lived token that only PreviewOpen hands out,
// so viewers must be signed in to Banda. Requests travel to the pod through
// the runner's stream (internal/tunnel); the API never dials a pod.
// ponytail: one cookie per browser, so two windows previewing different
// threads share the last one opened (their reload button reopens). Per-thread
// subdomains would lift that, but need wildcard DNS.

const (
	previewCookie  = "banda_preview"
	previewOpenTTL = time.Minute
	previewTTL     = 12 * time.Hour
	maxPreviewBody = 64 << 20
)

var errNotRunning = errors.New("the environment is not running")

// PreviewConfig is where the preview origin listens (Addr, e.g. ":8788") and
// where browsers reach it: URL, or else the host they use for Banda on Addr's port.
type PreviewConfig struct {
	Addr, URL string
}

func (s *Service) PreviewOpen(ctx context.Context, req *bandav1.PreviewOpenRequest) (*bandav1.PreviewOpenResponse, error) {
	if err := s.previewable(ctx, req.ThreadId); err != nil {
		return nil, err
	}
	if req.Port < 1024 || req.Port > 65535 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "port must be between 1024 and 65535")
	}
	q := url.Values{
		"token": {s.previewKey.sign(req.ThreadId+"|"+strconv.Itoa(int(req.Port)), time.Now().Add(previewOpenTTL))},
		"path":  {localPath(req.Path)},
	}
	return &bandav1.PreviewOpenResponse{Url: s.previewOrigin(ctx) + "/__banda/open?" + q.Encode()}, nil
}

// PreviewWatch streams the thread's ports, like Files without waking a
// paused environment, unless its owner asks to.
func (s *Service) PreviewWatch(ctx context.Context, req *bandav1.PreviewWatchRequest, stream bandav1connect.ThreadServicePreviewWatchServerStream) error {
	id := req.ThreadId
	if err := s.previewable(ctx, id); err != nil {
		return err
	}
	if req.Wake {
		effect, err := s.apply(ctx, id, thread.Wake{By: user(ctx)})
		if err != nil {
			return err
		}
		s.do(ctx, id, effect)
	}
	var last *bandav1.PreviewWatchResponse
	for {
		res := &bandav1.PreviewWatchResponse{}
		s.mu.Lock()
		if r := s.runners[id]; r != nil {
			res.Running, res.Ports = true, r.ports
		}
		changed := s.previews[id]
		if changed == nil {
			changed = make(chan struct{})
			s.previews[id] = changed
		}
		s.mu.Unlock()
		if last == nil || !proto.Equal(res, last) {
			if err := stream.Send(res); err != nil {
				return err
			}
			last = res
		}
		select {
		case <-ctx.Done():
			return nil
		case <-changed:
		}
	}
}

// previewChanged wakes the thread's PreviewWatch streams. Call with s.mu held.
func (s *Service) previewChanged(id string) {
	if ch := s.previews[id]; ch != nil {
		close(ch)
		delete(s.previews, id)
	}
}

func (s *Service) previewable(ctx context.Context, id string) error {
	if strings.HasPrefix(id, "login:") { // a loginKey, not a thread
		return connect.NewError(connect.CodeNotFound, "thread not found")
	}
	_, err := s.store.Get(ctx, id)
	return cerr(err)
}

// previewOrigin is Preview.URL, or else the caller's host on the preview port.
func (s *Service) previewOrigin(ctx context.Context) string {
	if s.Preview.URL != "" {
		return strings.TrimSuffix(s.Preview.URL, "/")
	}
	scheme, host := "http", "localhost"
	if info, ok := connect.CallInfoForServerContext(ctx); ok {
		if u, err := url.Parse(info.RequestHeader().Get("Origin")); err == nil && u.Hostname() != "" {
			scheme, host = u.Scheme, u.Hostname()
		}
	}
	_, port, _ := net.SplitHostPort(s.Preview.Addr)
	return scheme + "://" + net.JoinHostPort(host, port)
}

// PreviewHandler serves the preview origin.
func (s *Service) PreviewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /__banda/open", s.openPreview)
	mux.HandleFunc("/", s.proxyPreview)
	return mux
}

func (s *Service) openPreview(w http.ResponseWriter, r *http.Request) {
	target, ok := s.previewKey.verify(r.URL.Query().Get("token"), time.Now())
	if !ok {
		previewPage(w, http.StatusUnauthorized, "This preview link has expired. Reopen it from the thread in Banda.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: previewCookie, Value: s.previewKey.sign(target, time.Now().Add(previewTTL)), Path: "/",
		MaxAge: int(previewTTL.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: strings.HasPrefix(s.Preview.URL, "https://")})
	http.Redirect(w, r, localPath(r.URL.Query().Get("path")), http.StatusFound)
}

func (s *Service) proxyPreview(w http.ResponseWriter, r *http.Request) {
	var target string
	if c, err := r.Cookie(previewCookie); err == nil {
		target, _ = s.previewKey.verify(c.Value, time.Now())
	}
	id, port, ok := strings.Cut(target, "|")
	if !ok {
		previewPage(w, http.StatusUnauthorized, "Open this preview from its thread in Banda.")
		return
	}
	s.mu.Lock()
	running := s.runners[id] != nil
	s.mu.Unlock()
	if !running { // never wakes the environment
		previewPage(w, http.StatusServiceUnavailable, "The environment is not running. Open the thread in Banda to wake it.")
		return
	}
	local := "localhost:" + port // what dev servers expect, e.g. Vite's allowed hosts
	origin := "http://" + r.Host
	if r.TLS != nil || strings.HasPrefix(s.Preview.URL, "https://") {
		origin = "https://" + r.Host
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxPreviewBody)
	proxy := &httputil.ReverseProxy{
		Transport: s.previewProxy,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetXForwarded()
			pr.Out.URL.Scheme, pr.Out.URL.Host, pr.Out.Host = "http", net.JoinHostPort(id, port), local
			pr.Out.Header.Del("Cookie") // Banda's session cookie must not reach the sandbox
			for _, c := range pr.In.Cookies() {
				if !strings.HasPrefix(c.Name, "banda_") {
					pr.Out.Header.Add("Cookie", c.String())
				}
			}
			if pr.Out.Header.Get("Origin") == origin { // CSRF checks compare it with Host
				pr.Out.Header.Set("Origin", "http://"+local)
			}
		},
		ModifyResponse: func(res *http.Response) error {
			cookies := res.Header.Values("Set-Cookie")
			res.Header.Del("Set-Cookie")
			for _, c := range cookies {
				if !strings.HasPrefix(strings.TrimSpace(c), "banda_") {
					res.Header.Add("Set-Cookie", c)
				}
			}
			if loc, err := url.Parse(res.Header.Get("Location")); err == nil && loc.Host == local {
				res.Header.Set("Location", loc.RequestURI())
			}
			res.Header.Del("X-Frame-Options") // Banda frames it
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() == nil {
				slog.Debug("preview", "thread", id, "port", port, "err", err)
			}
			previewPage(w, http.StatusBadGateway, fmt.Sprintf("Port %s did not answer: %v", port, err))
		},
	}
	proxy.ServeHTTP(w, r)
}

// dialPreview opens a tunnel to "<thread>:<port>" through the thread's runner.
func (s *Service) dialPreview(ctx context.Context, _, addr string) (net.Conn, error) {
	id, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	p, _ := strconv.Atoi(port)
	s.mu.Lock()
	r := s.runners[id]
	s.mu.Unlock()
	if r == nil {
		return nil, errNotRunning
	}
	tid := rand.Text()
	c := r.tunnels.Open(tid)
	if !r.send(&bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_HttpTunnel{HttpTunnel: &bandav1.HttpTunnel{Id: tid, Port: uint32(p)}}}) {
		c.Close()
		return nil, errNotRunning
	}
	return c, nil
}

// previewPage is the preview origin's own answer, shown inside the frame.
func previewPage(w http.ResponseWriter, code int, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!doctype html><meta name="color-scheme" content="light dark"><title>Banda preview</title>`+
		`<p style="font:14px system-ui;margin:3rem auto;max-width:28rem;text-align:center;opacity:.7">%s</p>`, html.EscapeString(text))
}
