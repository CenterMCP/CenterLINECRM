package transport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/center/CenterLINECRM/internal/crm"
)

// Exercise the actual HTTP MCP transport, including sessions and schema validation.
func mcpCaller(t *testing.T, h http.Handler) func(string, map[string]any) map[string]any {
	t.Helper()
	session := ""
	id := 0
	rpc := func(method string, params any) map[string]any {
		t.Helper()
		id++
		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json, text/event-stream")
		if session != "" {
			r.Header.Set("Mcp-Session-Id", session)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if v := w.Header().Get("Mcp-Session-Id"); v != "" {
			session = v
		}
		var envelope map[string]any
		if err = json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("MCP HTTP %d: %s", w.Code, w.Body)
		}
		if envelope["error"] != nil {
			t.Fatalf("RPC error: %v", envelope["error"])
		}
		return envelope["result"].(map[string]any)
	}
	rpc("initialize", map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "workflow-test", "version": "1"}})
	return func(name string, args map[string]any) map[string]any {
		t.Helper()
		return rpc("tools/call", map[string]any{"name": name, "arguments": args})
	}
}
func TestMCPToCustomerRedemptionWorkflow(t *testing.T) {
	store, err := crm.Open(filepath.Join(t.TempDir(), "crm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	handler := Handler(store)
	call := mcpCaller(t, handler)
	success := func(name string, args map[string]any) map[string]any {
		t.Helper()
		result := call(name, args)
		if result["isError"] == true {
			t.Fatalf("%s failed: %v", name, result)
		}
		return result["structuredContent"].(map[string]any)
	}
	master := map[string]any{"kind": "tier", "name": "Gold", "active": true, "data": map[string]any{"minimum_points": 1000}}
	success("save_master_record", master)
	listed := success("list_master_records", map[string]any{"kind": "tier"})
	if len(listed["data"].([]any)) != 1 || listed["arguments"].(map[string]any)["kind"] != "tier" {
		t.Fatal("master data or UI filter context missing")
	}
	privilege := map[string]any{"name": "Coffee", "description": "One coffee", "cost": 60, "stock": 1, "active": true}
	savedPrivilege := success("save_privilege", privilege)
	privilegeID := savedPrivilege["data"].(map[string]any)["saved"].(string)
	request := func(method, path, body, token, key string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	register := request("POST", "/api/v1/register", `{"name":"Alice","email":"alice@example.com"}`, "", "")
	if register.Code != 201 {
		t.Fatal(register.Body)
	}
	var account struct {
		Customer crm.Customer `json:"customer"`
		Token    string       `json:"access_token"`
	}
	if err = json.Unmarshal(register.Body.Bytes(), &account); err != nil {
		t.Fatal(err)
	}
	adjustment := map[string]any{"customer_id": account.Customer.ID, "delta": 100, "reason": "Purchase", "reference": "sale-1"}
	success("adjust_points", adjustment)
	success("adjust_points", adjustment)
	points := request("GET", "/api/v1/me/points", "", account.Token, "")
	if !strings.Contains(points.Body.String(), `"points":100`) {
		t.Fatal(points.Body)
	}
	redeemed := request("POST", "/api/v1/me/redemptions", `{"privilege_id":"`+privilegeID+`"}`, account.Token, "checkout-1")
	if redeemed.Code != 200 {
		t.Fatal(redeemed.Body)
	}
	retry := request("POST", "/api/v1/me/redemptions", `{"privilege_id":"`+privilegeID+`"}`, account.Token, "checkout-1")
	if retry.Body.String() != redeemed.Body.String() {
		t.Fatal("redemption retry differed")
	}
	points = request("GET", "/api/v1/me/points", "", account.Token, "")
	if !strings.Contains(points.Body.String(), `"points":40`) {
		t.Fatal(points.Body)
	}
	ledger := success("list_point_ledger", map[string]any{"customer_id": account.Customer.ID})
	if len(ledger["data"].([]any)) != 2 {
		t.Fatal("ledger must contain exactly one award and one debit")
	}
	redemptions := success("list_redemptions", map[string]any{"customer_id": account.Customer.ID})
	if len(redemptions["data"].([]any)) != 1 {
		t.Fatal("duplicate redemption recorded")
	}
	ui := success("open_crm_admin", map[string]any{"section": "points", "customer_id": account.Customer.ID})
	if ui["arguments"].(map[string]any)["section"] != "points" {
		t.Fatal("UI target missing")
	}
	// Another registrant cannot choose Alice's identity to see her balance.
	second := request("POST", "/api/v1/register", `{"name":"Bob","email":"bob@example.com"}`, "", "")
	var other struct {
		Token string `json:"access_token"`
	}
	if err = json.Unmarshal(second.Body.Bytes(), &other); err != nil {
		t.Fatal(err)
	}
	otherPoints := request("GET", "/api/v1/me/points?customer_id="+account.Customer.ID, "", other.Token, "")
	if !strings.Contains(otherPoints.Body.String(), `"points":0`) || strings.Contains(otherPoints.Body.String(), account.Customer.ID) {
		t.Fatal("customer isolation failed")
	}
	success("update_customer", map[string]any{"id": account.Customer.ID, "name": "Alice", "active": false})
	if got := request("GET", "/api/v1/me", "", account.Token, ""); got.Code != 401 {
		t.Fatalf("deactivated token returned %d", got.Code)
	}
}
func TestMCPRejectsInvalidArgumentsWithoutMutating(t *testing.T) {
	store, err := crm.Open(filepath.Join(t.TempDir(), "crm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	call := mcpCaller(t, Handler(store))
	valid := map[string]any{"name": "Tea", "description": "Tea reward", "cost": 10, "stock": 2, "active": true}
	cases := []struct {
		name string
		args map[string]any
	}{
		{"save_privilege", map[string]any{"id": "tea", "name": "Tea", "description": "Tea reward", "cost": 10, "stock": 2}},
		{"save_privilege", map[string]any{"id": "tea", "name": "Tea", "description": "Tea reward", "cost": 10, "stock": 2, "active": true, "extra": "unexpected"}},
		{"save_privilege", map[string]any{"id": "tea", "name": "Tea", "description": "Tea reward", "cost": 0, "stock": 2, "active": true}},
		{"save_privilege", map[string]any{"id": "tea", "name": "Tea", "description": "Tea reward", "cost": 1.5, "stock": 2, "active": true}},
		{"save_master_record", map[string]any{"kind": "tier", "name": "Gold", "active": true, "data": []any{}}},
		{"open_crm_admin", map[string]any{"section": "unknown"}},
		{"list_privileges", map[string]any{"page": 0}},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if result := call(tc.name, tc.args); result["isError"] != true {
				t.Fatalf("invalid args accepted: %v", result)
			}
		})
	}
	var count int
	if err = store.DB.QueryRow(`SELECT count(*) FROM privileges`).Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid calls changed privileges")
	}
	result := call("save_privilege", valid)
	if result["isError"] == true {
		t.Fatal(result)
	}
	valid["id"] = result["structuredContent"].(map[string]any)["data"].(map[string]any)["saved"]
	// Missing active must not silently archive an existing privilege.
	delete(valid, "active")
	if result := call("save_privilege", valid); result["isError"] != true {
		t.Fatal("incomplete edit accepted")
	}
	var active bool
	if err = store.DB.QueryRow(`SELECT active FROM privileges WHERE id=?`, valid["id"]).Scan(&active); err != nil || !active {
		t.Fatal("invalid edit archived privilege")
	}
}
