package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/center/CenterLINECRM/internal/crm"
)

func TestMCPBulkImportAndPoints(t *testing.T) {
	s, err := crm.Open(filepath.Join(t.TempDir(), "crm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	handler := Handler(s)
	call := mcpCaller(t, handler)
	rows := []any{map[string]any{"name": "Emily Wilson", "email": "emily.wilson@example.com", "external_id": "contact-emily"}, map[string]any{"name": "Nattapong Srisawat", "email": "nattapong@example.com", "external_id": "contact-nattapong"}}
	args := map[string]any{"source": "cdp:serenity-pearl", "members": rows}
	result := call("bulk_import_members", args)
	if result["isError"] == true {
		t.Fatal(result)
	}
	data := result["structuredContent"].(map[string]any)
	if data["created"] != float64(2) {
		t.Fatal(data)
	}
	imported := data["members"].([]any)[0].(map[string]any)
	if _, ok := imported["access_token"]; ok {
		t.Fatal("bulk result exposes a raw key")
	}
	link, err := s.CreateMemberCouponLink(context.Background(), crm.MemberLookup{Email: "emily.wilson@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/member-session", strings.NewReader(`{"member":"`+link.Token+`"}`))
	handler.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	req.AddCookie(cookies[0])
	handler.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Emily Wilson") {
		t.Fatal(w.Code, w.Body)
	}
	points := map[string]any{"source": "cdp:serenity-pearl", "adjustments": []any{
		map[string]any{"external_id": "contact-emily", "delta": 300, "reason": "CDP opening balance", "reference": "opening-2026-11"},
		map[string]any{"email": "nattapong@example.com", "delta": 200, "reason": "CDP opening balance", "reference": "opening-2026-11"},
	}}
	for i := 0; i < 2; i++ {
		if r := call("bulk_adjust_points", points); r["isError"] == true {
			t.Fatal(r)
		}
	}
	retry := call("bulk_import_members", args)["structuredContent"].(map[string]any)
	if retry["created"] != float64(0) || retry["updated"] != float64(2) {
		t.Fatal(retry)
	}
	for _, raw := range retry["members"].([]any) {
		if _, ok := raw.(map[string]any)["access_token"]; ok {
			t.Fatal("existing key returned")
		}
	}
	customers, err := s.Customers(context.Background(), 0)
	if err != nil || len(customers) != 2 {
		t.Fatal(customers, err)
	}
	total := int64(0)
	for _, c := range customers {
		total += c.Points
	}
	if total != 500 {
		t.Fatalf("retry awarded points twice: %d", total)
	}
}

func TestMCPBulkValidationAndAutoIDs(t *testing.T) {
	s, err := crm.Open(filepath.Join(t.TempDir(), "crm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	call := mcpCaller(t, Handler(s))
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"bulk_import_members", map[string]any{"source": "cdp", "members": []any{}}},
		{"bulk_import_members", map[string]any{"source": "cdp", "members": []any{map[string]any{"name": "Alice", "email": "alice@example.com", "id": "manual"}}}},
		{"bulk_import_members", map[string]any{"source": "cdp", "members": []any{map[string]any{"name": "Alice", "email": "alice@example.com", "points": 999}}}},
		{"bulk_import_members", map[string]any{"source": "cdp", "members": []any{map[string]any{"name": "Alice", "email": "alice@example.com"}, map[string]any{"name": "Bad", "email": "bad-email"}}}},
		{"bulk_adjust_points", map[string]any{"adjustments": []any{map[string]any{"email": "alice@example.com", "customer_id": "other", "delta": 1, "reason": "bad", "reference": "bad"}}}},
		{"bulk_adjust_points", map[string]any{"adjustments": []any{map[string]any{"email": "alice@example.com", "delta": 1.5, "reason": "bad", "reference": "bad"}}}},
	} {
		if r := call(tc.name, tc.args); r["isError"] != true {
			t.Fatalf("accepted %s: %v", tc.name, r)
		}
	}
	var total int
	if err = s.DB.QueryRow(`SELECT count(*) FROM customers`).Scan(&total); err != nil || total != 0 {
		t.Fatal("invalid bulk calls changed members", total, err)
	}
	args := map[string]any{"name": "Coffee", "description": "One coffee", "cost": 100, "stock": 10, "active": true}
	created := call("save_privilege", args)
	if created["isError"] == true {
		t.Fatal(created)
	}
	id := created["structuredContent"].(map[string]any)["data"].(map[string]any)["saved"].(string)
	if len(id) != 48 {
		t.Fatal("missing generated ID", id)
	}
	args["id"] = id
	args["stock"] = 20
	if r := call("save_privilege", args); r["isError"] == true {
		t.Fatal(r)
	}
	args["id"] = "manual-new-id"
	if r := call("save_privilege", args); r["isError"] != true {
		t.Fatal("manual new ID accepted", r)
	}
	master := call("save_master_record", map[string]any{"kind": "tier", "name": "Gold", "active": true, "data": map[string]any{}})
	if master["isError"] == true {
		t.Fatal(master)
	}
	records, err := s.Records(context.Background(), "tier", 0)
	if err != nil || len(records) != 1 || len(records[0].ID) != 48 {
		t.Fatal(records, err)
	}
}
