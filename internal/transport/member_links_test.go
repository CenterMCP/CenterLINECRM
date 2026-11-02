package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/center/CenterLINECRM/internal/crm"
)

func TestPersonalizedMemberURLWorkflow(t *testing.T) {
	s, err := crm.Open(filepath.Join(t.TempDir(), "crm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	member, err := s.BulkImportMembers(ctx, "cdp:hotel", []crm.ImportMember{{Name: "Emily Wilson", Email: "emily@example.com", ExternalID: "cdp-emily"}})
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := s.Register(ctx, "Nattapong", "nattapong@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Adjust(ctx, member[0].Customer.ID, 100, "Welcome", "welcome"); err != nil {
		t.Fatal(err)
	}
	privilege, err := s.SavePrivilege(ctx, crm.Privilege{Name: "Coffee", Cost: 50, Stock: 2, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	h := Handler(s)
	call := mcpCaller(t, h)
	t.Setenv("CRM_BASE_URL", "https://centermcp.com/hotel-123/")
	result := call("get_member_coupon_url", map[string]any{"source": "cdp:hotel", "external_id": "cdp-emily"})
	if result["isError"] == true {
		t.Fatal(result)
	}
	data := result["structuredContent"].(map[string]any)
	u, err := url.Parse(data["coupon_url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/hotel-123/public/coupons" || u.RawQuery != "" || data["customer_id"] != member[0].Customer.ID {
		t.Fatal(data)
	}
	fragment, err := url.ParseQuery(u.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	link := fragment.Get("member")
	if len(link) != 96 {
		t.Fatal("missing personalized link value")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/public/coupons", nil))
	if w.Code != 200 || strings.Contains(strings.ToLower(w.Body.String()), "access key") || !strings.Contains(w.Body.String(), `id="registration" aria-labelledby="form-title" hidden`) {
		t.Fatal(w.Code, w.Body)
	}
	sessionRequest := func(token string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/v1/member-session", strings.NewReader(`{"member":"`+token+`"}`))
		r.Header.Set("X-Forwarded-Proto", "https")
		h.ServeHTTP(w, r)
		return w
	}
	w = sessionRequest(link)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing member cookie")
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "" || cookie.MaxAge <= 0 {
		t.Fatal("invalid session cookie", cookie)
	}
	// Browsers derive a cookie path from the external prefixed session URL.
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	external, _ := url.Parse("https://centermcp.com/hotel-123/api/v1/member-session")
	jar.SetCookies(external, cookies)
	ownAPI, _ := url.Parse("https://centermcp.com/hotel-123/api/v1/me")
	otherAPI, _ := url.Parse("https://centermcp.com/other-hotel/api/v1/me")
	if len(jar.Cookies(ownAPI)) != 1 || len(jar.Cookies(otherAPI)) != 0 {
		t.Fatal("member session cookie lost instance scope")
	}
	var account map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &account); err != nil {
		t.Fatal(err)
	}
	if _, ok := account["access_token"]; ok {
		t.Fatal("raw key exposed in session response")
	}
	request := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.AddCookie(cookie)
		r.Header.Set("Idempotency-Key", "redeem-one")
		h.ServeHTTP(w, r)
		return w
	}
	w = request("GET", "/api/v1/me", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), member[0].Customer.ID) || strings.Contains(w.Body.String(), other.ID) {
		t.Fatal("member isolation", w.Code, w.Body)
	}
	body := `{"privilege_id":"` + privilege.ID + `"}`
	for i := 0; i < 2; i++ {
		w = request("POST", "/api/v1/me/redemptions", body)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body)
		}
	}
	w = request("GET", "/api/v1/me/points", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"points":50`) {
		t.Fatal(w.Code, w.Body)
	}
	if got := sessionRequest(strings.Repeat("a", 96)); got.Code != 401 || len(got.Result().Cookies()) != 0 {
		t.Fatal("invalid link authenticated", got.Code)
	}
	if err = s.UpdateCustomer(ctx, member[0].Customer.ID, "Emily", false); err != nil {
		t.Fatal(err)
	}
	if got := request("GET", "/api/v1/me", ""); got.Code != 401 {
		t.Fatal("inactive session authorized", got.Code)
	}
}

func TestMemberLinkConfigurationAndSelectors(t *testing.T) {
	s, err := crm.Open(filepath.Join(t.TempDir(), "crm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	c, _, err := s.Register(context.Background(), "Emily", "emily@example.com")
	if err != nil {
		t.Fatal(err)
	}
	call := mcpCaller(t, Handler(s))
	for _, base := range []string{"", "not-a-url", "https://example.com?secret=x"} {
		t.Setenv("CRM_BASE_URL", base)
		if result := call("get_member_coupon_url", map[string]any{"customer_id": c.ID}); result["isError"] != true {
			t.Fatal("invalid base accepted", result)
		}
	}
	var count int
	if err = s.DB.QueryRow(`SELECT count(*) FROM member_links`).Scan(&count); err != nil || count != 0 {
		t.Fatal("configuration failure issued links", count, err)
	}
	t.Setenv("CRM_BASE_URL", "https://members.example.com")
	for _, args := range []map[string]any{{}, {"customer_id": c.ID, "email": c.Email}, {"external_id": "missing-source"}, {"email": "missing@example.com"}} {
		if result := call("get_member_coupon_url", args); result["isError"] != true {
			t.Fatal("invalid member selection accepted", result)
		}
	}
}

func TestRegistrationSetsMemberSessionCookie(t *testing.T) {
	s, err := crm.Open(filepath.Join(t.TempDir(), "crm.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := Handler(s)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/v1/register", strings.NewReader(`{"name":"Emily","email":"emily@example.com"}`)))
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].Secure {
		t.Fatal("local registration session cookie", cookies)
	}
	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	r.AddCookie(cookies[0])
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Emily") {
		t.Fatal(w.Code, w.Body)
	}
}
