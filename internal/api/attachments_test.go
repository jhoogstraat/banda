package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// An upload is a CORS-simple request (a text/plain body), which a previewed
// app on the same site could send with the viewer's cookie.
func TestUploadCrossOrigin(t *testing.T) {
	h := (&Service{}).FilesHandler(func(*http.Request) string { return "alice" })
	for _, site := range []string{"same-site", "cross-site"} {
		r := httptest.NewRequest("POST", "/files?name=x.txt", strings.NewReader("hi"))
		r.Header.Set("Content-Type", "text/plain")
		r.Header.Set("Sec-Fetch-Site", site)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s upload: %d, want 403", site, w.Code)
		}
	}
}
