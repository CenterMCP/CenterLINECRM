package transport

import "net/http"

func registerCustomerUI(mux *http.ServeMux) {
	for pattern, target := range map[string]string{"GET /public": "public/register", "GET /public/{$}": "register"} {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", target)
			w.WriteHeader(http.StatusSeeOther)
		})
	}
	for path, asset := range map[string]struct{ contentType, body string }{
		"/public/register":     {"text/html; charset=utf-8", registerHTML},
		"/public/register.css": {"text/css; charset=utf-8", registerCSS},
		"/public/register.js":  {"text/javascript; charset=utf-8", registerJS},
	} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", asset.contentType)
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
			w.Header().Set("Referrer-Policy", "no-referrer")
			_, _ = w.Write([]byte(asset.body))
		})
	}
}
