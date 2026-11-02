package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/center/CenterLINECRM/internal/crm"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const memberCookieName = "crm_member_session"

func registerMemberLinkTool(ms *server.MCPServer, s *crm.Store) {
	tool := mcp.NewToolWithRawSchema("get_member_coupon_url", "Return a personalized URL opening a member's coupon page directly, without registration or asking the consumer to manage an access key. Select exactly one customer_id, email or external_id; external_id requires source from bulk_import_members. CRM_BASE_URL must be configured. Returns coupon_url, customer_id and expires_at. Each link lasts 90 days; existing links remain valid. Share the returned link with that member.", json.RawMessage(`{"type":"object","properties":{"customer_id":{"type":"string","minLength":1,"maxLength":120},"email":{"type":"string","minLength":3,"maxLength":254},"source":{"type":"string","minLength":1,"maxLength":80},"external_id":{"type":"string","minLength":1,"maxLength":120}},"oneOf":[{"required":["customer_id"]},{"required":["email"]},{"required":["external_id"]}],"additionalProperties":false}`))
	mcp.WithReadOnlyHintAnnotation(false)(&tool)
	mcp.WithDestructiveHintAnnotation(false)(&tool)
	ms.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		base, _, err := publicServiceBase(true)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		var in crm.MemberLookup
		raw, err := json.Marshal(req.Params.Arguments)
		if err == nil {
			err = json.Unmarshal(raw, &in)
		}
		if err != nil {
			return mcp.NewToolResultError("invalid arguments"), nil
		}
		link, err := s.CreateMemberCouponLink(ctx, in)
		if err != nil {
			return mcp.NewToolResultError(publicError(err)), nil
		}
		result := map[string]any{"customer_id": link.CustomerID, "coupon_url": base + "/public/coupons#member=" + url.QueryEscape(link.Token), "expires_at": link.ExpiresAt}
		body, _ := json.Marshal(result)
		return mcp.NewToolResultStructured(result, string(body)), nil
	})
}

func setMemberCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	// Omit Path: the browser derives /<instance>/api/v1 from the public request,
	// preserving proxy prefixes even when the proxy strips them before forwarding.
	http.SetCookie(w, &http.Cookie{Name: memberCookieName, Value: token, HttpOnly: true, Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"), SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: int(crm.MemberSessionLifetime.Seconds())})
}

func registerMemberSessionRoute(mux *http.ServeMux, s *crm.Store) {
	mux.HandleFunc("POST /api/v1/member-session", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Member string `json:"member"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		c, session, expires, err := s.StartMemberSession(r.Context(), in.Member)
		if err != nil {
			fail(w, err)
			return
		}
		setMemberCookie(w, r, session, expires)
		write(w, http.StatusOK, map[string]any{"customer": c})
	})
}
