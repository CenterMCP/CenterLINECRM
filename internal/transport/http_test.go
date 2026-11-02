package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/center/CenterLINECRM/internal/crm"
)

func TestPublicWorkflow(t *testing.T) {
	s, err := crm.Open(filepath.Join(t.TempDir(), "crm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := Handler(s)
	request := func(method, path, body, token, key string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/v1/me", "/api/v1/me/points", "/api/v1/me/ledger", "/api/v1/me/redemptions"} {
		w := request("GET", path, "", "", "")
		if w.Code != 401 {
			t.Fatalf("unprotected %s: %d", path, w.Code)
		}
	}
	w := request("POST", "/api/v1/register", `{"name":"Alice","email":"alice@example.com"}`, "", "")
	if w.Code != 201 {
		t.Fatal(w.Body.String())
	}
	var registered struct {
		Customer crm.Customer `json:"customer"`
		Token    string       `json:"access_token"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &registered); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Adjust(context.Background(), registered.Customer.ID, 100, "Purchase", "order-1"); err != nil {
		t.Fatal(err)
	}
	privilege, err := s.SavePrivilege(context.Background(), crm.Privilege{Name: "Coffee", Cost: 50, Stock: 1, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	w = request("POST", "/api/v1/me/redemptions", `{"privilege_id":"`+privilege.ID+`"}`, registered.Token, "")
	if w.Code != 400 {
		t.Fatal("missing key accepted")
	}
	w = request("POST", "/api/v1/me/redemptions", `{"privilege_id":"`+privilege.ID+`"}`, registered.Token, "one")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	first := w.Body.String()
	w = request("POST", "/api/v1/me/redemptions", `{"privilege_id":"`+privilege.ID+`"}`, registered.Token, "one")
	if w.Body.String() != first {
		t.Fatal("retry changed")
	}
	w = request("GET", "/api/v1/me/points", "", registered.Token, "")
	if !strings.Contains(w.Body.String(), `"points":50`) {
		t.Fatal(w.Body.String())
	}
	for _, body := range []string{`{"name":"X","email":"x@example.com","points":999}`, `{"name":"X","email":"x@example.com"} {}`} {
		w = request("POST", "/api/v1/register", body, "", "")
		if w.Code != 400 {
			t.Fatal("invalid registration accepted")
		}
	}
	w = request("GET", "/api/v1/me/ledger?page=-1", "", registered.Token, "")
	if w.Code != 400 {
		t.Fatal("invalid page accepted")
	}
	w = request("GET", "/api/v1/me?customer_id=other", "", registered.Token, "")
	if !strings.Contains(w.Body.String(), registered.Customer.ID) {
		t.Fatal("customer scope changed")
	}
}
func TestMCPToolsAndResource(t *testing.T) {
	s, err := crm.Open(filepath.Join(t.TempDir(), "crm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := Handler(s)
	session := ""
	rpc := func(body string) map[string]any {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		if session != "" {
			r.Header.Set("Mcp-Session-Id", session)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if v := w.Header().Get("Mcp-Session-Id"); v != "" {
			session = v
		}
		var result map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatalf("MCP response %d: %s", w.Code, w.Body.String())
		}
		if result["error"] != nil {
			t.Fatalf("MCP error: %v", result)
		}
		return result
	}
	rpc(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`)
	tools := rpc(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	raw, _ := json.Marshal(tools)
	if !bytes.Contains(raw, []byte("save_privilege")) || !bytes.Contains(raw, []byte(ResourceURI)) {
		t.Fatal("tools or UI metadata missing")
	}
	rpc(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"save_privilege","arguments":{"name":"Tea","description":"Tea reward","cost":10,"stock":2,"active":true}}}`)
	p, err := s.Privileges(context.Background(), false, 0)
	if err != nil || len(p) != 1 {
		t.Fatal("MCP save failed")
	}
	resource := rpc(`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"ui://centerlinecrm/admin/v1.html"}}`)
	raw, _ = json.Marshal(resource)
	if !bytes.Contains(raw, []byte("ui/initialize")) {
		t.Fatal("UI resource missing")
	}
}
