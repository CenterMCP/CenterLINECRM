package crm

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
)

func TestBulkImportIdentityCredentialsAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	ctx := context.Background()
	members := []ImportMember{{Name: " Emily Wilson ", Email: " EMILY.WILSON@example.com ", ExternalID: "cdp-1"}, {Name: "Nattapong Srisawat", Email: "nattapong.srisawat@example.com", ExternalID: "cdp-2"}}
	imported, err := s.BulkImportMembers(ctx, "cdp:serenity", members)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range imported {
		if !m.Created || len(m.Customer.ID) != 48 || m.Customer.Points != 0 || len(m.AccessToken) != 96 {
			t.Fatal(m)
		}
		id, err := s.Authenticate(ctx, m.AccessToken)
		if err != nil || id != m.Customer.ID {
			t.Fatalf("new member token: %s %v", id, err)
		}
	}
	if imported[0].Customer.Email != "emily.wilson@example.com" || imported[0].Customer.Name != "Emily Wilson" {
		t.Fatal(imported[0])
	}
	first := imported[0]
	if _, err = s.Adjust(ctx, first.Customer.ID, 300, "Opening points", "opening-1"); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateCustomer(ctx, first.Customer.ID, "Emily", false); err != nil {
		t.Fatal(err)
	}
	members[0].Name = "Emily Bennett"
	members[0].Email = "emily.bennett@example.com"
	retry, err := s.BulkImportMembers(ctx, "cdp:serenity", members)
	if err != nil {
		t.Fatal(err)
	}
	if retry[0].Customer.ID != first.Customer.ID || retry[0].Customer.Points != 300 || retry[0].Customer.Active || retry[0].Created || retry[0].AccessToken != "" {
		t.Fatal(retry[0])
	}
	if err = s.UpdateCustomer(ctx, first.Customer.ID, "Emily Bennett", true); err != nil {
		t.Fatal(err)
	}
	if id, err := s.Authenticate(ctx, first.AccessToken); err != nil || id != first.Customer.ID {
		t.Fatal("import rotated existing token", err)
	}
	// A different import source can link an already registered member by email.
	linked, err := s.BulkImportMembers(ctx, "spreadsheet", []ImportMember{{Name: "Emily Bennett", Email: "EMILY.BENNETT@example.com", ExternalID: "sheet-1"}})
	if err != nil || linked[0].Customer.ID != first.Customer.ID || linked[0].Created {
		t.Fatalf("email match: %v %v", linked, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := s.BulkAdjustPoints(ctx, "cdp:serenity", []PointAdjustment{{ExternalID: "cdp-1", Delta: 50, Reason: "Purchase", Reference: "purchase-1"}})
	if err != nil || entries[0].CustomerID != first.Customer.ID {
		t.Fatalf("mapping persistence: %v %v", entries, err)
	}
}

func TestBulkImportRollbackAndIdentityConflicts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	_, err := s.BulkImportMembers(ctx, "cdp", []ImportMember{{Name: "Alice", Email: "alice@example.com", ExternalID: "a"}, {Name: "Bad", Email: "not-an-email"}})
	var batch *BatchError
	if !errors.As(err, &batch) || batch.Row != 2 || !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	var count int
	if err = s.DB.QueryRow(`SELECT (SELECT count(*) FROM customers)+(SELECT count(*) FROM customer_import_identities)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial import committed", count, err)
	}
	original, err := s.BulkImportMembers(ctx, "cdp", []ImportMember{{Name: "Alice", Email: "alice@example.com", ExternalID: "a"}, {Name: "Bob", Email: "bob@example.com", ExternalID: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, rows := range [][]ImportMember{
		{{Name: "Changed", Email: "alice@example.com", ExternalID: "a"}, {Name: "Collision", Email: "alice@example.com", ExternalID: "b"}},
		{{Name: "New", Email: "new@example.com"}, {Name: "Duplicate", Email: "NEW@example.com"}},
		{{Name: "Changed", Email: "alice@example.com", ExternalID: "different-source-id"}},
	} {
		if _, err = s.BulkImportMembers(ctx, "cdp", rows); !errors.Is(err, ErrConflict) {
			t.Fatal("expected identity conflict", err)
		}
	}
	got, err := s.Customer(ctx, original[0].Customer.ID)
	if err != nil || got.Name != "Alice" {
		t.Fatalf("failed import changed name: %v %v", got, err)
	}
	if err = s.DB.QueryRow(`SELECT count(*) FROM customers`).Scan(&count); err != nil || count != 2 {
		t.Fatal("rollback failed", count, err)
	}
}

func TestBulkAdjustPointsAtomicRetryAndSelectors(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	members, err := s.BulkImportMembers(ctx, "cdp", []ImportMember{{Name: "Alice", Email: "alice@example.com", ExternalID: "a"}, {Name: "Bob", Email: "bob@example.com", ExternalID: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	adjustments := []PointAdjustment{{Email: " ALICE@example.com ", Delta: 100, Reason: "Opening points", Reference: "opening"}, {ExternalID: "b", Delta: 200, Reason: "Opening points", Reference: "opening"}}
	first, err := s.BulkAdjustPoints(ctx, "cdp", adjustments)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.BulkAdjustPoints(ctx, "cdp", adjustments)
	if err != nil || retry[0].ID != first[0].ID || retry[1].ID != first[1].ID {
		t.Fatalf("retry: %v %v", retry, err)
	}
	cases := [][]PointAdjustment{
		{{CustomerID: members[0].Customer.ID, Delta: 25, Reason: "New", Reference: "new"}, {Email: "bob@example.com", Delta: -201, Reason: "Too much", Reference: "overspend"}},
		{{Email: "alice@example.com", Delta: 25, Reason: "New", Reference: "new"}, {Email: "bob@example.com", Delta: 201, Reason: "Opening points", Reference: "opening"}},
		{{Email: "alice@example.com", Delta: 25, Reason: "New", Reference: "new"}, {Email: "missing@example.com", Delta: 1, Reason: "Missing", Reference: "missing"}},
		{{Email: "alice@example.com", CustomerID: members[0].Customer.ID, Delta: 1, Reason: "Ambiguous", Reference: "ambiguous"}},
		{{Email: "alice@example.com", Delta: 1, Reason: "Reserved", Reference: "redeem:fake"}},
		{{Email: "alice@example.com", Delta: 1, Reason: "Zero", Reference: "same"}, {Email: "alice@example.com", Delta: 1, Reason: "Zero", Reference: "same"}},
	}
	for _, rows := range cases {
		if _, err = s.BulkAdjustPoints(ctx, "cdp", rows); err == nil {
			t.Fatal("invalid batch succeeded", rows)
		}
	}
	for i, m := range members {
		c, err := s.Customer(ctx, m.Customer.ID)
		if err != nil || c.Points != int64((i+1)*100) {
			t.Fatalf("balance rollback: %v %v", c, err)
		}
		ledger, err := s.History(ctx, c.ID, 0)
		if err != nil || len(ledger) != 1 {
			t.Fatal("ledger rollback", ledger, err)
		}
	}
	if err = s.UpdateCustomer(ctx, members[1].Customer.ID, "Bob", false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.BulkAdjustPoints(ctx, "", []PointAdjustment{{Email: "bob@example.com", Delta: 1, Reason: "Inactive", Reference: "inactive"}}); !errors.Is(err, ErrConflict) {
		t.Fatal("inactive member updated", err)
	}
}

func TestBulkImportConcurrentRetryAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crm.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	results := make(chan []ImportedMember, 2)
	errs := make(chan error, 2)
	for _, store := range []*Store{s, other} {
		wg.Add(1)
		go func(store *Store) {
			defer wg.Done()
			result, err := store.BulkImportMembers(context.Background(), "cdp", []ImportMember{{Name: "Alice", Email: "alice@example.com", ExternalID: "cdp-a"}})
			results <- result
			errs <- err
		}(store)
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	id := ""
	created := 0
	for rows := range results {
		if id != "" && id != rows[0].Customer.ID {
			t.Fatal("duplicate customer")
		}
		id = rows[0].Customer.ID
		if rows[0].Created {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created %d members", created)
	}
}

func TestGeneratedRecordIDs(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	r, err := s.SaveRecord(ctx, Record{Kind: "tier", Name: "Gold", Active: true})
	if err != nil || len(r.ID) != 48 {
		t.Fatalf("record: %v %v", r, err)
	}
	r.Name = "Platinum"
	updated, err := s.SaveRecord(ctx, r)
	if err != nil || updated.ID != r.ID {
		t.Fatal(updated, err)
	}
	p, err := s.SavePrivilege(ctx, Privilege{Name: "Coffee", Cost: 100, Stock: 10, Active: true})
	if err != nil || len(p.ID) != 48 || p.ID == r.ID {
		t.Fatal(p, err)
	}
	p.Stock = 20
	if updated, err := s.SavePrivilege(ctx, p); err != nil || updated.ID != p.ID {
		t.Fatal(updated, err)
	}
	if _, err = s.SavePrivilege(ctx, Privilege{ID: "manual", Name: "Manual", Cost: 1, Active: true}); !errors.Is(err, ErrNotFound) {
		t.Fatal("manual privilege ID created", err)
	}
	if _, err = s.SaveRecord(ctx, Record{Kind: "tier", ID: "manual", Name: "Manual"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("manual record ID created", err)
	}
}
