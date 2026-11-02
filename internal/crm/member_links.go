package crm

import (
	"context"
	"database/sql"
	"time"
)

const MemberLinkLifetime = 90 * 24 * time.Hour
const MemberSessionLifetime = 30 * 24 * time.Hour

type MemberLookup struct {
	CustomerID string `json:"customer_id,omitempty"`
	Email      string `json:"email,omitempty"`
	ExternalID string `json:"external_id,omitempty"`
	Source     string `json:"source,omitempty"`
}
type MemberLink struct {
	CustomerID string    `json:"customer_id"`
	Token      string    `json:"-"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// CreateMemberCouponLink issues an independent opaque link without rotating
// existing links, sessions or API tokens. Only a hash is persisted.
func (s *Store) CreateMemberCouponLink(ctx context.Context, in MemberLookup) (MemberLink, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return MemberLink{}, err
	}
	defer tx.Rollback()
	id, err := resolveMemberTx(ctx, tx, in.Source, PointAdjustment{CustomerID: in.CustomerID, Email: in.Email, ExternalID: in.ExternalID})
	if err != nil {
		return MemberLink{}, err
	}
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT active FROM customers WHERE id=?`, id).Scan(&active); err != nil {
		return MemberLink{}, dbError(err)
	}
	if !active {
		return MemberLink{}, ErrConflict
	}
	link := MemberLink{CustomerID: id, Token: ID() + ID(), ExpiresAt: time.Now().UTC().Add(MemberLinkLifetime)}
	_, err = tx.ExecContext(ctx, `INSERT INTO member_links(token_hash,customer_id,expires_at) VALUES(?,?,?)`, hash(link.Token), id, link.ExpiresAt.Format(time.RFC3339Nano))
	if err != nil {
		return MemberLink{}, err
	}
	return link, tx.Commit()
}

// StartMemberSession exchanges a personalized link for an HttpOnly browser
// session. Opening a link repeatedly does not invalidate an earlier session.
func (s *Store) StartMemberSession(ctx context.Context, linkToken string) (Customer, string, time.Time, error) {
	if len(linkToken) != 96 {
		return Customer{}, "", time.Time{}, ErrUnauthorized
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Customer{}, "", time.Time{}, err
	}
	defer tx.Rollback()
	var c Customer
	err = tx.QueryRowContext(ctx, `SELECT c.id,c.name,c.email,c.active,c.points FROM member_links l JOIN customers c ON c.id=l.customer_id WHERE l.token_hash=? AND c.active=1 AND julianday(l.expires_at)>julianday(?)`, hash(linkToken), now()).Scan(&c.ID, &c.Name, &c.Email, &c.Active, &c.Points)
	if err != nil {
		if err == sql.ErrNoRows {
			err = ErrUnauthorized
		}
		return Customer{}, "", time.Time{}, err
	}
	token, expires, err := newMemberSessionTx(ctx, tx, c.ID)
	if err != nil {
		return Customer{}, "", time.Time{}, err
	}
	if err = tx.Commit(); err != nil {
		return Customer{}, "", time.Time{}, err
	}
	return c, token, expires, nil
}

func newMemberSessionTx(ctx context.Context, tx *sql.Tx, id string) (string, time.Time, error) {
	token := ID() + ID()
	expires := time.Now().UTC().Add(MemberSessionLifetime)
	_, err := tx.ExecContext(ctx, `INSERT INTO member_sessions(token_hash,customer_id,expires_at) VALUES(?,?,?)`, hash(token), id, expires.Format(time.RFC3339Nano))
	return token, expires, err
}

func (s *Store) AuthenticateMemberSession(ctx context.Context, token string) (string, error) {
	if len(token) != 96 {
		return "", ErrUnauthorized
	}
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT c.id FROM member_sessions s JOIN customers c ON c.id=s.customer_id WHERE s.token_hash=? AND c.active=1 AND julianday(s.expires_at)>julianday(?)`, hash(token), now()).Scan(&id)
	if err == sql.ErrNoRows {
		err = ErrUnauthorized
	}
	return id, err
}

// RegisterMember creates the member and browser session atomically. The public
// registration API retains its bearer response for existing integrations.
func (s *Store) RegisterMember(ctx context.Context, name, email string) (Customer, string, string, time.Time, error) {
	// Register's insertion and this session must commit together.
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Customer{}, "", "", time.Time{}, err
	}
	defer tx.Rollback()
	c, apiToken, err := registerMemberTx(ctx, tx, name, email)
	if err != nil {
		return Customer{}, "", "", time.Time{}, err
	}
	session, expires, err := newMemberSessionTx(ctx, tx, c.ID)
	if err != nil {
		return Customer{}, "", "", time.Time{}, err
	}
	if err = tx.Commit(); err != nil {
		return Customer{}, "", "", time.Time{}, err
	}
	return c, apiToken, session, expires, nil
}
