package transport

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/center/CenterLINECRM/internal/crm"
)

func TestCustomerRegistrationRoutes(t *testing.T) {
	s, err := crm.Open(filepath.Join(t.TempDir(), "crm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := Handler(s)
	for _, tc := range []struct{ path, contentType, content string }{
		{"/public/register", "text/html", `action="../api/v1/register"`},
		{"/public/register.css", "text/css", "@media"},
		{"/public/register.js", "text/javascript", "fetch(form.action"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != 200 || !strings.Contains(w.Header().Get("Content-Type"), tc.contentType) || !strings.Contains(w.Body.String(), tc.content) {
				t.Fatalf("unexpected response: %d %v %s", w.Code, w.Header(), w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "connect-src 'self'") {
				t.Fatal("missing privacy or origin restrictions")
			}
		})
	}
	for _, prefix := range []string{"", "/mcp-instance"} {
		for _, path := range []string{"/public", "/public/"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
			base, _ := url.Parse("https://public.example" + prefix + path)
			target, err := base.Parse(w.Header().Get("Location"))
			if err != nil || w.Code != http.StatusSeeOther || target.Path != prefix+"/public/register" {
				t.Fatalf("redirect loses service prefix: %d %v %v", w.Code, target, err)
			}
		}
	}
	for _, path := range []string{"/public/unknown", "/public/register/unknown"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("unknown page %s: %d", path, w.Code)
		}
	}
}
