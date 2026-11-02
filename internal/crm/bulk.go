package crm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

const MaxBulkRows = 500

type BatchError struct {
	Row int
	Err error
}

func (e *BatchError) Error() string { return fmt.Sprintf("row %d: %v", e.Row, e.Err) }
func (e *BatchError) Unwrap() error { return e.Err }

type ImportMember struct {
	Name       string `json:"name"`
	Email      string `json:"email"`
	ExternalID string `json:"external_id,omitempty"`
	Active     *bool  `json:"active,omitempty"`
}
type ImportedMember struct {
	Customer    Customer `json:"customer"`
	Source      string   `json:"source"`
	ExternalID  string   `json:"external_id,omitempty"`
	Created     bool     `json:"created"`
	AccessToken string   `json:"-"`
}

func normalizedEmail(value string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email || !valid(email, 254) {
		return "", ErrInvalid
	}
	return email, nil
}

// BulkImportMembers upserts by source/external ID or normalized email.
// Existing member access and balances survive imports.
func (s *Store) BulkImportMembers(ctx context.Context, source string, members []ImportMember) ([]ImportedMember, error) {
	source = strings.TrimSpace(source)
	if !valid(source, 80) || len(members) < 1 || len(members) > MaxBulkRows {
		return nil, ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := make([]ImportedMember, 0, len(members))
	seen := map[string]bool{}
	for i, member := range members {
		result, err := importMemberTx(ctx, tx, source, member)
		if err == nil && seen[result.Customer.ID] {
			err = ErrConflict
		}
		if err != nil {
			return nil, &BatchError{i + 1, err}
		}
		seen[result.Customer.ID] = true
		out = append(out, result)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func importMemberTx(ctx context.Context, tx *sql.Tx, source string, in ImportMember) (ImportedMember, error) {
	result := ImportedMember{Source: source, ExternalID: strings.TrimSpace(in.ExternalID)}
	name := strings.TrimSpace(in.Name)
	email, err := normalizedEmail(in.Email)
	if err != nil || !valid(name, 200) || (in.ExternalID != "" && !valid(result.ExternalID, 120)) {
		return result, ErrInvalid
	}
	var byExternal, byEmail string
	if result.ExternalID != "" {
		err = tx.QueryRowContext(ctx, `SELECT customer_id FROM customer_import_identities WHERE source=? AND external_id=?`, source, result.ExternalID).Scan(&byExternal)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
	}
	err = tx.QueryRowContext(ctx, `SELECT id FROM customers WHERE email=?`, email).Scan(&byEmail)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return result, err
	}
	if byExternal != "" && byEmail != "" && byExternal != byEmail {
		return result, ErrConflict
	}
	id := byExternal
	if id == "" {
		id = byEmail
	}
	if id == "" {
		id = ID()
		result.Created = true
		result.AccessToken = ID() + ID()
		active := true
		if in.Active != nil {
			active = *in.Active
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO customers(id,name,email,active,token_hash,created_at) VALUES(?,?,?,?,?,?)`, id, name, email, active, hash(result.AccessToken), now())
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE customers SET name=?,email=?,active=COALESCE(?,active) WHERE id=?`, name, email, in.Active, id)
	}
	if err != nil {
		return result, err
	}
	if result.ExternalID != "" {
		// A source identity cannot silently replace a different mapping on retry.
		var mapped string
		err = tx.QueryRowContext(ctx, `SELECT external_id FROM customer_import_identities WHERE source=? AND customer_id=?`, source, id).Scan(&mapped)
		if err == nil && mapped != result.ExternalID {
			return result, ErrConflict
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return result, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO customer_import_identities(source,external_id,customer_id) VALUES(?,?,?) ON CONFLICT(source,external_id) DO NOTHING`, source, result.ExternalID, id)
		if err != nil {
			return result, err
		}
	}
	err = tx.QueryRowContext(ctx, `SELECT id,name,email,active,points FROM customers WHERE id=?`, id).Scan(&result.Customer.ID, &result.Customer.Name, &result.Customer.Email, &result.Customer.Active, &result.Customer.Points)
	return result, err
}

type PointAdjustment struct {
	CustomerID string `json:"customer_id,omitempty"`
	Email      string `json:"email,omitempty"`
	ExternalID string `json:"external_id,omitempty"`
	Delta      int64  `json:"delta"`
	Reason     string `json:"reason"`
	Reference  string `json:"reference"`
}

// BulkAdjustPoints applies all deltas in one transaction with the same ledger
// validation and retry semantics as Adjust. Exactly one selector per row.
func (s *Store) BulkAdjustPoints(ctx context.Context, source string, adjustments []PointAdjustment) ([]Entry, error) {
	source = strings.TrimSpace(source)
	if len(adjustments) < 1 || len(adjustments) > MaxBulkRows || len(source) > 80 {
		return nil, ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := make([]Entry, 0, len(adjustments))
	seen := map[string]bool{}
	for i, a := range adjustments {
		id, err := resolveMemberTx(ctx, tx, source, a)
		key := id + "\x00" + a.Reference
		if err == nil && seen[key] {
			err = ErrConflict
		}
		var entry Entry
		if err == nil {
			entry, err = adjustPointsTx(ctx, tx, id, a.Delta, a.Reason, a.Reference)
		}
		if err != nil {
			return nil, &BatchError{i + 1, err}
		}
		seen[key] = true
		out = append(out, entry)
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

func resolveMemberTx(ctx context.Context, tx *sql.Tx, source string, a PointAdjustment) (string, error) {
	selectors := 0
	for _, s := range []string{a.CustomerID, a.Email, a.ExternalID} {
		if s != "" {
			selectors++
		}
	}
	if selectors != 1 {
		return "", ErrInvalid
	}
	if a.CustomerID != "" {
		if !valid(a.CustomerID, 120) {
			return "", ErrInvalid
		}
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM customers WHERE id=?`, a.CustomerID).Scan(&id)
		return id, dbError(err)
	}
	if a.Email != "" {
		email, err := normalizedEmail(a.Email)
		if err != nil {
			return "", err
		}
		var id string
		err = tx.QueryRowContext(ctx, `SELECT id FROM customers WHERE email=?`, email).Scan(&id)
		return id, dbError(err)
	}
	if !valid(source, 80) || !valid(a.ExternalID, 120) {
		return "", ErrInvalid
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT customer_id FROM customer_import_identities WHERE source=? AND external_id=?`, source, a.ExternalID).Scan(&id)
	return id, dbError(err)
}
