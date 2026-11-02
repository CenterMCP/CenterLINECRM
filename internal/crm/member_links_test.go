package crm

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestMemberLinksSessionsPersistenceAndExpiry(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "crm.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	imported, err := s.BulkImportMembers(ctx, "cdp:hotel", []ImportMember{{Name: "Emily Wilson", Email: "emily@example.com", ExternalID: "cdp-emily"}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.CreateMemberCouponLink(ctx, MemberLookup{Source: "cdp:hotel", ExternalID: "cdp-emily"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateMemberCouponLink(ctx, MemberLookup{Email: "EMILY@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Token == second.Token || first.CustomerID != imported[0].Customer.ID {
		t.Fatal("links must be independent", first.CustomerID)
	}
	c, session, _, err := s.StartMemberSession(ctx, first.Token)
	if err != nil || c.ID != first.CustomerID {
		t.Fatal(c, err)
	}
	_, session2, _, err := s.StartMemberSession(ctx, second.Token)
	if err != nil {
		t.Fatal(err)
	}
	var plainCount int
	if err = s.DB.QueryRow(`SELECT (SELECT count(*) FROM member_links WHERE token_hash=?)+(SELECT count(*) FROM member_sessions WHERE token_hash=?)`, first.Token, session).Scan(&plainCount); err != nil || plainCount != 0 {
		t.Fatal("plaintext material stored", plainCount, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := s.AuthenticateMemberSession(ctx, session); err != nil || id != c.ID {
		t.Fatal("session did not persist", err)
	}
	if _, _, _, err = s.StartMemberSession(ctx, first.Token); err != nil {
		t.Fatal("link did not persist", err)
	}
	if _, err = s.DB.Exec(`UPDATE member_links SET expires_at='2000-01-01T00:00:00Z' WHERE token_hash=?`, hash(first.Token)); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.StartMemberSession(ctx, first.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired link accepted", err)
	}
	if _, err = s.AuthenticateMemberSession(ctx, session2); err != nil {
		t.Fatal("unrelated session invalidated", err)
	}
	if _, err = s.DB.Exec(`UPDATE member_sessions SET expires_at='2000-01-01T00:00:00Z' WHERE token_hash=?`, hash(session)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AuthenticateMemberSession(ctx, session); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("expired session accepted", err)
	}
	if err = s.UpdateCustomer(ctx, c.ID, c.Name, false); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = s.StartMemberSession(ctx, second.Token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("inactive member link accepted", err)
	}
	if _, err = s.AuthenticateMemberSession(ctx, session2); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("inactive session accepted", err)
	}
	if _, err = s.CreateMemberCouponLink(ctx, MemberLookup{CustomerID: c.ID}); !errors.Is(err, ErrConflict) {
		t.Fatal("inactive member got link", err)
	}
}

func TestRegistrationSessionRollback(t *testing.T) {
	s := testStore(t)
	if _, err := s.DB.Exec(`CREATE TRIGGER reject_member_session BEFORE INSERT ON member_sessions BEGIN SELECT RAISE(ABORT,'session failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := s.RegisterMember(context.Background(), "Emily", "emily@example.com"); err == nil {
		t.Fatal("expected failed session creation")
	}
	var count int
	if err := s.DB.QueryRow(`SELECT count(*) FROM customers`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("registration partially committed: %d %v", count, err)
	}
}
