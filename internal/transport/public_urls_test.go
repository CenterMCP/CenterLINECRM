package transport

import (
	"path/filepath"
	"testing"

	"github.com/center/CenterLINECRM/internal/crm"
)

func TestMCPPublicURLs(t *testing.T) {
	s, err := crm.Open(filepath.Join(t.TempDir(), "crm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	call := mcpCaller(t, Handler(s))
	for _, tc := range []struct {
		name, base, want string
		template         bool
	}{
		{"default template", "", "https://centermcp.com/<mcp-id>/public/register", true},
		{"blank template", " \t", "https://centermcp.com/<mcp-id>/public/register", true},
		{"dedicated domain", "https://members.example.com", "https://members.example.com/public/register", false},
		{"instance prefix", "https://centermcp.com/instance-123", "https://centermcp.com/instance-123/public/register", false},
		{"trailing slashes", " https://centermcp.com/instance-123/// ", "https://centermcp.com/instance-123/public/register", false},
		{"local development", "http://localhost:8000", "http://localhost:8000/public/register", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("CRM_BASE_URL", tc.base)
			result := call("get_public_urls", map[string]any{})
			if result["isError"] == true {
				t.Fatalf("tool failed: %v", result)
			}
			data := result["structuredContent"].(map[string]any)
			if data["registration_url"] != tc.want || data["is_template"] != tc.template {
				t.Fatalf("unexpected URLs: %v", data)
			}
		})
	}
	for _, base := range []string{"members.example.com", "//example.com", "javascript:alert(1)", "https://user:secret@example.com", "https://example.com?key=secret", "https://example.com#fragment"} {
		t.Run(base, func(t *testing.T) {
			t.Setenv("CRM_BASE_URL", base)
			if result := call("get_public_urls", map[string]any{}); result["isError"] != true {
				t.Fatalf("invalid base accepted: %v", result)
			}
		})
	}
}
