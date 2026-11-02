package transport

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/center/CenterLINECRM/internal/crm"
	"github.com/mark3labs/mcp-go/server"
)

func Handler(s *crm.Store) http.Handler {
	mux := http.NewServeMux()
	mcpHTTP := server.NewStreamableHTTPServer(MCP(s))
	mux.Handle("/mcp", mcpHTTP)
	registerCustomerUI(mux, s)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := s.DB.PingContext(r.Context()); err != nil {
			write(w, 503, map[string]string{"status": "unavailable"})
			return
		}
		write(w, 200, map[string]string{"status": "ok", "version": Version})
	})
	mux.HandleFunc("POST /api/v1/register", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name  string `json:"name"`
			Email string `json:"email"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		c, token, err := s.Register(r.Context(), in.Name, in.Email)
		if err != nil {
			fail(w, err)
			return
		}
		write(w, 201, map[string]any{"customer": c, "access_token": token, "token_type": "Bearer"})
	})
	mux.HandleFunc("GET /api/v1/privileges", func(w http.ResponseWriter, r *http.Request) {
		offset, err := pageOffset(r)
		if err != nil {
			fail(w, err)
			return
		}
		v, err := s.Privileges(r.Context(), true, offset)
		respond(w, v, err)
	})
	customer := func(fn func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			id, err := s.Authenticate(r.Context(), bearer(r))
			if err != nil {
				fail(w, err)
				return
			}
			fn(w, r, id)
		}
	}
	mux.HandleFunc("GET /api/v1/me", customer(func(w http.ResponseWriter, r *http.Request, id string) {
		v, err := s.Customer(r.Context(), id)
		respond(w, v, err)
	}))
	mux.HandleFunc("GET /api/v1/me/points", customer(func(w http.ResponseWriter, r *http.Request, id string) {
		v, err := s.Customer(r.Context(), id)
		respond(w, map[string]any{"customer_id": id, "points": v.Points}, err)
	}))
	mux.HandleFunc("GET /api/v1/me/ledger", customer(func(w http.ResponseWriter, r *http.Request, id string) {
		o, err := pageOffset(r)
		if err != nil {
			fail(w, err)
			return
		}
		v, err := s.History(r.Context(), id, o)
		respond(w, v, err)
	}))
	mux.HandleFunc("GET /api/v1/me/redemptions", customer(func(w http.ResponseWriter, r *http.Request, id string) {
		o, err := pageOffset(r)
		if err != nil {
			fail(w, err)
			return
		}
		v, err := s.Redemptions(r.Context(), id, o)
		respond(w, v, err)
	}))
	mux.HandleFunc("POST /api/v1/me/redemptions", customer(func(w http.ResponseWriter, r *http.Request, id string) {
		var in struct {
			PrivilegeID string `json:"privilege_id"`
		}
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		v, err := s.Redeem(r.Context(), id, in.PrivilegeID, r.Header.Get("Idempotency-Key"))
		respond(w, v, err)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		mux.ServeHTTP(w, r)
	})
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 65536)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return crm.ErrInvalid
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return crm.ErrInvalid
	}
	return nil
}
func pageOffset(r *http.Request) (int, error) {
	p := r.URL.Query().Get("page")
	if p == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 1000000 {
		return 0, crm.ErrInvalid
	}
	return (n - 1) * 100, nil
}
func publicError(err error) string {
	switch {
	case errors.Is(err, crm.ErrInvalid):
		return "invalid input"
	case errors.Is(err, crm.ErrNotFound):
		return "not found or unavailable"
	case errors.Is(err, crm.ErrConflict):
		return "conflict: duplicate identity, changed retry, inactive account or insufficient points"
	case errors.Is(err, crm.ErrUnauthorized):
		return "unauthorized"
	default:
		log.Printf("CRM operation failed: %v", err)
		return "internal server error"
	}
}
func fail(w http.ResponseWriter, err error) {
	status := 500
	switch {
	case errors.Is(err, crm.ErrInvalid):
		status = 400
	case errors.Is(err, crm.ErrNotFound):
		status = 404
	case errors.Is(err, crm.ErrConflict):
		status = 409
	case errors.Is(err, crm.ErrUnauthorized):
		status = 401
	}
	write(w, status, map[string]string{"error": publicError(err)})
}
func respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	write(w, 200, v)
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func bearer(r *http.Request) string {
	value := r.Header.Get("Authorization")
	if !strings.HasPrefix(value, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(value, "Bearer ")
}
