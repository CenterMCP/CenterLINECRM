package transport

import (
	"bytes"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/center/CenterLINECRM/internal/crm"
)

var customerTemplate = template.Must(template.New("registration").Parse(registerHTML))

func registerCustomerUI(mux *http.ServeMux, s *crm.Store) {
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
			body := asset.body
			if path == "/public/register" || path == "/public/register.css" {
				branding, err := s.BusinessBranding(r.Context())
				if err != nil {
					fail(w, err)
					return
				}
				if path == "/public/register" {
					var rendered bytes.Buffer
					if err := customerTemplate.Execute(&rendered, branding); err != nil {
						fail(w, err)
						return
					}
					body = rendered.String()
				} else {
					body = brandedCSS(branding)
				}
			}
			w.Header().Set("Content-Type", asset.contentType)
			w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' https:; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
			w.Header().Set("Referrer-Policy", "no-referrer")
			_, _ = w.Write([]byte(body))
		})
	}
}

// Select a contrasting foreground for arbitrary business colors.
func brandedCSS(b crm.BusinessBranding) string {
	color, _ := strconv.ParseUint(strings.TrimPrefix(b.PrimaryColor, "#"), 16, 32)
	luminance := 0.0
	for i, weight := range []float64{0.2126, 0.7152, 0.0722} {
		c := float64((color>>uint(16-8*i))&255) / 255
		if c <= 0.04045 {
			c /= 12.92
		} else {
			c = math.Pow((c+0.055)/1.055, 2.4)
		}
		luminance += c * weight
	}
	foreground := "#ffffff"
	if luminance > 0.179 {
		foreground = "#000000"
	}
	css := strings.NewReplacer("#2563eb", b.PrimaryColor, "#1d4ed8", b.PrimaryColor).Replace(registerCSS)
	return css + "\n.brand-logo{width:40px;height:40px;object-fit:contain;flex-shrink:0}.brand-name{overflow-wrap:anywhere;min-width:0}\n" +
		".primary,.redeem-button:not(:disabled),.brand-mark{color:" + foreground + "}\n" +
		".wallet{background:" + b.PrimaryColor + ";color:" + foreground + "}.wallet .points-label,.wallet .points>span:last-child,.wallet .wallet-bottom{color:inherit}\n"
}
