package api

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	bandav1 "github.com/jhoogstraat/banda/gen/banda/v1"
	"github.com/jhoogstraat/banda/internal/tunnel"
)

// TestPreviewProxy runs the preview origin against an app behind a fake
// runner that relays tunnels the way cmd/runner does.
func TestPreviewProxy(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws": // echo one line after the upgrade, like an HMR socket
			w.Header().Set("Upgrade", "websocket")
			w.Header().Set("Connection", "Upgrade")
			w.WriteHeader(http.StatusSwitchingProtocols)
			c, rw, _ := http.NewResponseController(w).Hijack()
			defer c.Close()
			line, _ := rw.ReadString('\n')
			rw.WriteString("echo " + line)
			rw.Flush()
		case "/login":
			http.Redirect(w, r, "http://"+r.Host+"/home", http.StatusFound)
		default:
			http.SetCookie(w, &http.Cookie{Name: "banda_session", Value: "evil"})
			http.SetCookie(w, &http.Cookie{Name: "app", Value: "1"})
			io.WriteString(w, r.Host+" "+r.Header.Get("Cookie")+" "+r.Header.Get("Origin"))
		}
	}))
	defer app.Close()
	_, port, _ := net.SplitHostPort(app.Listener.Addr().String())

	s := New(nil, nil)
	r := &runner{ch: make(chan *bandav1.ConnectResponse, 16), done: make(chan struct{})}
	pod := tunnel.NewMux(func(m *bandav1.HttpTunnel) error { r.tunnels.Deliver(m); return nil })
	r.tunnels = tunnel.NewMux(func(m *bandav1.HttpTunnel) error {
		r.ch <- &bandav1.ConnectResponse{Kind: &bandav1.ConnectResponse_HttpTunnel{HttpTunnel: m}}
		return nil
	})
	go func() {
		for msg := range r.ch {
			m := msg.GetHttpTunnel()
			if m.Port == 0 {
				pod.Deliver(m)
				continue
			}
			c := pod.Open(m.Id)
			go func() {
				tcp, err := net.Dial("tcp", app.Listener.Addr().String())
				if err != nil {
					c.CloseWithError(err)
					return
				}
				go func() { io.Copy(tcp, c); tcp.Close() }()
				io.Copy(c, tcp)
				c.Close()
			}()
		}
	}()
	s.runners["th"] = r
	preview := httptest.NewServer(s.PreviewHandler())
	defer preview.Close()

	get := func(path string, cookie string, header ...string) (*http.Response, string) {
		req, _ := http.NewRequest("GET", preview.URL+path, nil)
		if cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		res, err := http.DefaultTransport.RoundTrip(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, string(b)
	}

	if res, _ := get("/", ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without the cookie: %d", res.StatusCode)
	}
	open := "/__banda/open?" + url.Values{"token": {s.previewKey.sign("th|"+port, time.Now().Add(time.Minute))}, "path": {"/x"}}.Encode()
	res, _ := get(open, "")
	cookie := res.Header.Get("Set-Cookie")
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/x" || !strings.HasPrefix(cookie, previewCookie+"=") {
		t.Fatalf("open: %d %q %q", res.StatusCode, res.Header.Get("Location"), cookie)
	}
	cookie, _, _ = strings.Cut(cookie, ";")

	res, body := get("/assets/app.js", cookie+"; banda_session=secret; app=1", "Origin", preview.URL)
	if want := "localhost:" + port + " app=1 http://localhost:" + port; body != want {
		t.Errorf("app saw %q, want %q: Banda's cookies stripped, Host and Origin local", body, want)
	}
	if c := res.Header.Values("Set-Cookie"); len(c) != 1 || !strings.HasPrefix(c[0], "app=") {
		t.Errorf("Set-Cookie %q: the app must not set banda_ cookies", c)
	}
	if res, _ := get("/login", cookie); res.Header.Get("Location") != "/home" {
		t.Errorf("redirect to %q, want it relative", res.Header.Get("Location"))
	}

	c, err := net.Dial("tcp", preview.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, "GET /ws HTTP/1.1\r\nHost: x\r\nCookie: "+cookie+"\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	br := bufio.NewReader(c)
	ws, err := http.ReadResponse(br, nil)
	if err != nil || ws.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade: %v %v", ws, err)
	}
	io.WriteString(c, "hi\n")
	if line, _ := br.ReadString('\n'); line != "echo hi\n" {
		t.Errorf("upgraded connection got %q", line)
	}

	delete(s.runners, "th")
	if res, _ := get("/", cookie); res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("without a runner: %d", res.StatusCode)
	}
}
