package crm

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "crm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func seed(t *testing.T, s *Store) (context.Context, Customer) {
	t.Helper()
	ctx := context.Background()
	c, _, err := s.Register(ctx, "Alice", "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Adjust(ctx, c.ID, 100, "Purchase", "order-1"); err != nil {
		t.Fatal(err)
	}
	if err = s.SavePrivilege(ctx, Privilege{ID: "coffee", Name: "Coffee", Cost: 60, Stock: 1, Active: true}); err != nil {
		t.Fatal(err)
	}
	return ctx, c
}
func TestRedemptionRetryAndRollback(t *testing.T) {
	s := testStore(t)
	ctx, c := seed(t, s)
	first, err := s.Redeem(ctx, c.ID, "coffee", "request-1")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.Redeem(ctx, c.ID, "coffee", "request-1")
	if err != nil || first.ID != retry.ID {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	if _, err = s.Redeem(ctx, c.ID, "other", "request-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed retry accepted: %v", err)
	}
	if err = s.SavePrivilege(ctx, Privilege{ID: "coffee", Name: "Coffee", Cost: 60, Stock: 1, Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Redeem(ctx, c.ID, "coffee", "request-2"); !errors.Is(err, ErrConflict) {
		t.Fatalf("overspend accepted: %v", err)
	}
	got, _ := s.Customer(ctx, c.ID)
	p, _ := s.Privileges(ctx, false, 0)
	ledger, _ := s.History(ctx, c.ID, 0)
	if got.Points != 40 || p[0].Stock != 1 || len(ledger) != 2 {
		t.Fatalf("rollback failed: %+v %+v %+v", got, p, ledger)
	}
}
func TestConcurrentRedemptionAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, c := seed(t, s)
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i, store := range []*Store{s, second} {
		wg.Add(1)
		go func(i int, store *Store) {
			defer wg.Done()
			_, err := store.Redeem(ctx, c.ID, "coffee", string(rune('a'+i)))
			results <- err
		}(i, store)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrConflict) {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("%d redemptions", success)
	}
	got, _ := s.Customer(ctx, c.ID)
	if got.Points != 40 {
		t.Fatalf("balance %d", got.Points)
	}
}
func TestLedgerIdempotencyAndAuth(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	c, token, err := s.Register(ctx, "Bob", "bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Authenticate(ctx, token)
	if err != nil || id != c.ID {
		t.Fatal("token not valid")
	}
	var stored string
	s.DB.QueryRow(`SELECT token_hash FROM customers`).Scan(&stored)
	if stored == token {
		t.Fatal("plaintext token stored")
	}
	e, err := s.Adjust(ctx, id, 100, "Sale", "sale-1")
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.Adjust(ctx, id, 100, "Sale", "sale-1")
	if err != nil || retry.ID != e.ID {
		t.Fatal("duplicate adjustment")
	}
	if _, err = s.Adjust(ctx, id, 200, "Sale", "sale-1"); !errors.Is(err, ErrConflict) {
		t.Fatal("changed adjustment accepted")
	}
	if _, err = s.Adjust(ctx, id, -101, "Correction", "correction"); !errors.Is(err, ErrConflict) {
		t.Fatal("negative balance accepted")
	}
	if _, err = s.Adjust(ctx, id, 1, "Reserved", "redeem:fake"); !errors.Is(err, ErrInvalid) {
		t.Fatal("reserved reference accepted")
	}
	if err = s.UpdateCustomer(ctx, id, "Bob", false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Authenticate(ctx, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatal("inactive token accepted")
	}
	if _, _, err = s.Register(ctx, "Bob", "BOB@example.com"); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate email accepted")
	}
}
func TestPersistenceAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, c := seed(t, s)
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.Customer(ctx, c.ID)
	if err != nil || got.Points != 100 {
		t.Fatal("balance did not persist")
	}
	for _, p := range []Privilege{{ID: "p", Name: "P", Cost: -1}, {ID: "p", Name: "P", Cost: 1, Stock: -1}} {
		if !errors.Is(s.SavePrivilege(ctx, p), ErrInvalid) {
			t.Fatal("invalid privilege accepted")
		}
	}
}
