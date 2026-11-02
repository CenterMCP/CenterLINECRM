package transport

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/center/CenterLINECRM/internal/crm"
)

func TestBusinessBrandingWorkflow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.db")
	s, err := crm.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	h := Handler(s)
	call := mcpCaller(t, h)
	get := func(path string) string {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 {
			t.Fatalf("GET %s: %d %s", path, w.Code, w.Body)
		}
		return w.Body.String()
	}
	if !strings.Contains(get("/public/register"), "CenterLINECRM") {
		t.Fatal("missing default name")
	}
	args := map[string]any{"business_name": `Cafe <script>alert(1)</script>`, "logo_url": "https://example.com/logo.png?size=80&v=1", "primary_color": "#FFCC00"}
	result := call("update_business_branding", args)
	if result["isError"] == true {
		t.Fatal(result)
	}
	html := get("/public/register")
	if strings.Contains(html, "<script>alert(1)</script>") || !strings.Contains(html, "Cafe &lt;script&gt;") || !strings.Contains(html, `class="brand-logo"`) || !strings.Contains(html, "https://example.com/logo.png?size=80&amp;v=1") {
		t.Fatal(html)
	}
	css := get("/public/register.css")
	if !strings.Contains(css, "background: #ffcc00") || !strings.Contains(css, ".wallet{background:#ffcc00;color:#000000}") {
		t.Fatal("brand color or contrast not applied")
	}
	for _, bad := range []struct {
		key   string
		value any
	}{
		{"business_name", " "}, {"business_name", strings.Repeat("x", 201)},
		{"logo_url", "javascript:alert(1)"}, {"logo_url", "data:image/svg+xml,bad"}, {"logo_url", "https://user:secret@example.com/logo.png"}, {"logo_url", "/logo.png"},
		{"primary_color", "red; background:url(https://example.com)"}, {"primary_color", "#fff"},
	} {
		original := args[bad.key]
		args[bad.key] = bad.value
		if result := call("update_business_branding", args); result["isError"] != true {
			t.Fatalf("accepted invalid %s: %v", bad.key, result)
		}
		args[bad.key] = original
	}
	if get("/public/register") != html {
		t.Fatal("invalid updates changed branding")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = crm.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	h = Handler(s)
	if get("/public/register") != html {
		t.Fatal("branding did not persist")
	}
	call = mcpCaller(t, h)
	args["logo_url"] = ""
	if result := call("update_business_branding", args); result["isError"] == true {
		t.Fatal(result)
	}
	if html := get("/public/register"); strings.Contains(html, `class="brand-logo"`) || !strings.Contains(html, `class="brand-mark"`) {
		t.Fatal("logo removal failed")
	}
}
