package crm

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

var ErrInvalid = errors.New("invalid input")
var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")
var ErrUnauthorized = errors.New("unauthorized")

type Store struct{ DB *sql.DB }
type Record struct {
	Kind   string          `json:"kind"`
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Active bool            `json:"active"`
	Data   json.RawMessage `json:"data"`
}
type Privilege struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Cost        int64  `json:"cost"`
	Stock       int64  `json:"stock"`
	Active      bool   `json:"active"`
}
type Customer struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Active bool   `json:"active"`
	Points int64  `json:"points"`
}
type Entry struct {
	ID         string `json:"id"`
	CustomerID string `json:"customer_id"`
	Delta      int64  `json:"delta"`
	Reason     string `json:"reason"`
	Reference  string `json:"reference"`
	CreatedAt  string `json:"created_at"`
}
type Redemption struct {
	ID          string `json:"id"`
	CustomerID  string `json:"customer_id"`
	PrivilegeID string `json:"privilege_id"`
	Name        string `json:"name"`
	Cost        int64  `json:"cost"`
	Key         string `json:"idempotency_key"`
	CreatedAt   string `json:"created_at"`
}

func Open(path string) (*Store, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("_foreign_keys", "on")
	q.Set("_journal_mode", "WAL")
	q.Set("_busy_timeout", "5000")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db}
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS business_branding(id INTEGER PRIMARY KEY CHECK(id=1),business_name TEXT NOT NULL,logo_url TEXT NOT NULL,primary_color TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS schema_version(version INTEGER PRIMARY KEY);
INSERT OR IGNORE INTO schema_version VALUES(1);
CREATE TABLE IF NOT EXISTS master_records(kind TEXT NOT NULL,id TEXT NOT NULL,name TEXT NOT NULL,active INTEGER NOT NULL,data TEXT NOT NULL CHECK(json_valid(data)),PRIMARY KEY(kind,id));
CREATE TABLE IF NOT EXISTS privileges(id TEXT PRIMARY KEY,name TEXT NOT NULL,description TEXT NOT NULL,cost INTEGER NOT NULL CHECK(cost>0 AND cost<=1000000000),stock INTEGER NOT NULL CHECK(stock>=0),active INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS customers(id TEXT PRIMARY KEY,name TEXT NOT NULL,email TEXT NOT NULL UNIQUE,active INTEGER NOT NULL DEFAULT 1,points INTEGER NOT NULL DEFAULT 0 CHECK(points>=0 AND points<=9000000000000000),token_hash TEXT NOT NULL UNIQUE,created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS point_ledger(id TEXT PRIMARY KEY,customer_id TEXT NOT NULL REFERENCES customers(id),delta INTEGER NOT NULL,reason TEXT NOT NULL,reference TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(customer_id,reference));
CREATE TABLE IF NOT EXISTS redemptions(id TEXT PRIMARY KEY,customer_id TEXT NOT NULL REFERENCES customers(id),privilege_id TEXT NOT NULL REFERENCES privileges(id),name TEXT NOT NULL,cost INTEGER NOT NULL,idempotency_key TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(customer_id,idempotency_key));
CREATE INDEX IF NOT EXISTS ledger_customer ON point_ledger(customer_id,created_at);
CREATE INDEX IF NOT EXISTS redemption_customer ON redemptions(customer_id,created_at);
`

func ID() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func hash(token string) string     { v := sha256.Sum256([]byte(token)); return hex.EncodeToString(v[:]) }
func valid(v string, max int) bool { return strings.TrimSpace(v) != "" && len(v) <= max }
func now() string                  { return time.Now().UTC().Format(time.RFC3339Nano) }
func dbError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func (s *Store) SaveRecord(ctx context.Context, r Record) error {
	if !valid(r.Kind, 80) || !valid(r.ID, 120) || !valid(r.Name, 200) || len(r.Data) > 32000 {
		return ErrInvalid
	}
	if len(r.Data) == 0 {
		r.Data = json.RawMessage(`{}`)
	}
	var obj map[string]any
	if json.Unmarshal(r.Data, &obj) != nil || obj == nil {
		return ErrInvalid
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO master_records VALUES(?,?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET name=excluded.name,active=excluded.active,data=excluded.data`, r.Kind, r.ID, r.Name, r.Active, string(r.Data))
	return err
}
func (s *Store) Records(ctx context.Context, kind string, offset int) ([]Record, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT kind,id,name,active,data FROM master_records WHERE (?='' OR kind=?) ORDER BY kind,id LIMIT 100 OFFSET ?`, kind, kind, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var r Record
		var raw string
		if err = rows.Scan(&r.Kind, &r.ID, &r.Name, &r.Active, &raw); err != nil {
			return nil, err
		}
		r.Data = json.RawMessage(raw)
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) SavePrivilege(ctx context.Context, p Privilege) error {
	if !valid(p.ID, 120) || !valid(p.Name, 200) || len(p.Description) > 4000 || p.Cost < 1 || p.Cost > 1000000000 || p.Stock < 0 || p.Stock > 1000000000 {
		return ErrInvalid
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO privileges VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,description=excluded.description,cost=excluded.cost,stock=excluded.stock,active=excluded.active`, p.ID, p.Name, p.Description, p.Cost, p.Stock, p.Active)
	return err
}
func (s *Store) Privileges(ctx context.Context, public bool, offset int) ([]Privilege, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,description,cost,stock,active FROM privileges WHERE (?=0 OR active=1) ORDER BY id LIMIT 100 OFFSET ?`, public, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Privilege{}
	for rows.Next() {
		var p Privilege
		if err = rows.Scan(&p.ID, &p.Name, &p.Description, &p.Cost, &p.Stock, &p.Active); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Store) Register(ctx context.Context, name, email string) (Customer, string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if !valid(name, 200) || !valid(email, 254) || !strings.Contains(email, "@") {
		return Customer{}, "", ErrInvalid
	}
	c := Customer{ID: ID(), Name: strings.TrimSpace(name), Email: email, Active: true}
	token := ID() + ID()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO customers(id,name,email,token_hash,created_at) VALUES(?,?,?,?,?)`, c.ID, c.Name, c.Email, hash(token), now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Customer{}, "", ErrConflict
		}
		return Customer{}, "", err
	}
	return c, token, nil
}
func (s *Store) Authenticate(ctx context.Context, token string) (string, error) {
	if len(token) != 96 {
		return "", ErrUnauthorized
	}
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM customers WHERE token_hash=? AND active=1`, hash(token)).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnauthorized
	}
	return id, err
}
func (s *Store) Customer(ctx context.Context, id string) (Customer, error) {
	var c Customer
	err := s.DB.QueryRowContext(ctx, `SELECT id,name,email,active,points FROM customers WHERE id=?`, id).Scan(&c.ID, &c.Name, &c.Email, &c.Active, &c.Points)
	return c, dbError(err)
}
func (s *Store) Customers(ctx context.Context, offset int) ([]Customer, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,name,email,active,points FROM customers ORDER BY created_at,id LIMIT 100 OFFSET ?`, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Customer{}
	for rows.Next() {
		var c Customer
		if err = rows.Scan(&c.ID, &c.Name, &c.Email, &c.Active, &c.Points); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) UpdateCustomer(ctx context.Context, id, name string, active bool) error {
	if !valid(id, 120) || !valid(name, 200) {
		return ErrInvalid
	}
	r, err := s.DB.ExecContext(ctx, `UPDATE customers SET name=?,active=? WHERE id=?`, name, active, id)
	if err != nil {
		return err
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) Adjust(ctx context.Context, id string, delta int64, reason, ref string) (Entry, error) {
	if delta == 0 || delta > 1000000000 || delta < -1000000000 || !valid(reason, 500) || !valid(ref, 120) || strings.HasPrefix(ref, "redeem:") {
		return Entry{}, ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Entry{}, err
	}
	defer tx.Rollback()
	var e Entry
	err = tx.QueryRowContext(ctx, `SELECT id,customer_id,delta,reason,reference,created_at FROM point_ledger WHERE customer_id=? AND reference=?`, id, ref).Scan(&e.ID, &e.CustomerID, &e.Delta, &e.Reason, &e.Reference, &e.CreatedAt)
	if err == nil {
		if e.Delta != delta || e.Reason != reason {
			return Entry{}, ErrConflict
		}
		return e, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return e, err
	}
	r, err := tx.ExecContext(ctx, `UPDATE customers SET points=points+? WHERE id=? AND active=1 AND points+? BETWEEN 0 AND 9000000000000000`, delta, id, delta)
	if err != nil {
		return e, err
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return e, ErrConflict
	}
	e = Entry{ID(), id, delta, reason, ref, now()}
	_, err = tx.ExecContext(ctx, `INSERT INTO point_ledger VALUES(?,?,?,?,?,?)`, e.ID, id, delta, reason, ref, e.CreatedAt)
	if err != nil {
		return e, err
	}
	return e, tx.Commit()
}
func (s *Store) Redeem(ctx context.Context, id, pid, key string) (Redemption, error) {
	var r Redemption
	if !valid(pid, 120) || !valid(key, 120) {
		return r, ErrInvalid
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return r, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT id,customer_id,privilege_id,name,cost,idempotency_key,created_at FROM redemptions WHERE customer_id=? AND idempotency_key=?`, id, key).Scan(&r.ID, &r.CustomerID, &r.PrivilegeID, &r.Name, &r.Cost, &r.Key, &r.CreatedAt)
	if err == nil {
		if r.PrivilegeID != pid {
			return Redemption{}, ErrConflict
		}
		return r, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return r, err
	}
	var p Privilege
	err = tx.QueryRowContext(ctx, `SELECT id,name,cost FROM privileges WHERE id=? AND active=1 AND stock>0`, pid).Scan(&p.ID, &p.Name, &p.Cost)
	if err != nil {
		return r, dbError(err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE customers SET points=points-? WHERE id=? AND active=1 AND points>=?`, p.Cost, id, p.Cost)
	if err != nil {
		return r, err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return r, ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE privileges SET stock=stock-1 WHERE id=?`, pid); err != nil {
		return r, err
	}
	r = Redemption{ID(), id, pid, p.Name, p.Cost, key, now()}
	if _, err = tx.ExecContext(ctx, `INSERT INTO redemptions VALUES(?,?,?,?,?,?,?)`, r.ID, id, pid, p.Name, p.Cost, key, r.CreatedAt); err != nil {
		return r, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO point_ledger VALUES(?,?,?,?,?,?)`, ID(), id, -p.Cost, "Privilege redemption", "redeem:"+r.ID, r.CreatedAt); err != nil {
		return r, err
	}
	return r, tx.Commit()
}
func (s *Store) History(ctx context.Context, id string, offset int) ([]Entry, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,customer_id,delta,reason,reference,created_at FROM point_ledger WHERE customer_id=? ORDER BY created_at DESC,id LIMIT 100 OFFSET ?`, id, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		if err = rows.Scan(&e.ID, &e.CustomerID, &e.Delta, &e.Reason, &e.Reference, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) Redemptions(ctx context.Context, id string, offset int) ([]Redemption, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,customer_id,privilege_id,name,cost,idempotency_key,created_at FROM redemptions WHERE (?='' OR customer_id=?) ORDER BY created_at DESC,id LIMIT 100 OFFSET ?`, id, id, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Redemption{}
	for rows.Next() {
		var r Redemption
		if err = rows.Scan(&r.ID, &r.CustomerID, &r.PrivilegeID, &r.Name, &r.Cost, &r.Key, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) Close() error      { return s.DB.Close() }
func Invalid(message string) error { return fmt.Errorf("%w: %s", ErrInvalid, message) }
