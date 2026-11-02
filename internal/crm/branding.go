package crm

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// BusinessBranding controls the public registration and membership page.
type BusinessBranding struct {
	BusinessName string `json:"business_name"`
	LogoURL      string `json:"logo_url"`
	PrimaryColor string `json:"primary_color"`
}

var brandColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (s *Store) BusinessBranding(ctx context.Context) (BusinessBranding, error) {
	b := BusinessBranding{BusinessName: "CenterLINECRM", PrimaryColor: "#2563eb"}
	err := s.DB.QueryRowContext(ctx, `SELECT business_name,logo_url,primary_color FROM business_branding WHERE id=1`).Scan(&b.BusinessName, &b.LogoURL, &b.PrimaryColor)
	if errors.Is(err, sql.ErrNoRows) {
		return b, nil
	}
	return b, err
}

func (s *Store) UpdateBusinessBranding(ctx context.Context, b BusinessBranding) (BusinessBranding, error) {
	b.BusinessName = strings.TrimSpace(b.BusinessName)
	b.LogoURL = strings.TrimSpace(b.LogoURL)
	b.PrimaryColor = strings.ToLower(strings.TrimSpace(b.PrimaryColor))
	if !valid(b.BusinessName, 200) || len(b.LogoURL) > 4000 || !brandColor.MatchString(b.PrimaryColor) {
		return b, ErrInvalid
	}
	if b.LogoURL != "" {
		u, err := url.Parse(b.LogoURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return b, ErrInvalid
		}
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO business_branding(id,business_name,logo_url,primary_color) VALUES(1,?,?,?) ON CONFLICT(id) DO UPDATE SET business_name=excluded.business_name,logo_url=excluded.logo_url,primary_color=excluded.primary_color`, b.BusinessName, b.LogoURL, b.PrimaryColor)
	return b, err
}
