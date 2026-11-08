// SPDX-License-Identifier: AGPL-3.0-only
// Package store owns persistent endpoint identities and credential hashes.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/EasybotCE/easybotce/internal/domain"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound        = errors.New("resource not found")
	ErrConflict        = errors.New("resource already exists")
	ErrInvalid         = errors.New("invalid resource")
	ErrUnauthenticated = errors.New("invalid endpoint credential")
	idPattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	capPattern         = regexp.MustCompile(`^[a-z][a-z0-9_.:-]{0,127}$`)
)

type Endpoint struct {
	ID      string              `json:"id"`
	Type    domain.EndpointType `json:"type"`
	Name    string              `json:"name"`
	Enabled bool                `json:"enabled"`
}

type Credential struct {
	ID           string   `json:"id"`
	EndpointID   string   `json:"endpoint_id"`
	Capabilities []string `json:"capabilities"`
	CreatedAt    int64    `json:"created_at"`
}

type IssuedCredential struct {
	Credential
	Token string `json:"token"`
}

type Store struct {
	db      *sql.DB
	dialect string
	q       *queries
}

// Open never returns driver errors: DSNs can embed passwords, TLS keys and paths.
// Each database must be dedicated to this Hub. DDL is additive and idempotent.
func Open(ctx context.Context, driver, dsn string) (*Store, error) {
	sqlDriver := driver
	switch driver {
	case "sqlite", "mysql":
	case "postgres":
		sqlDriver = "pgx"
	default:
		return nil, errors.New("unsupported database driver")
	}
	db, err := sql.Open(sqlDriver, dsn)
	if err != nil {
		return nil, errors.New("cannot open database")
	}
	// A single connection also serializes SQLite transactions and makes :memory:
	// behave predictably. Raising this limit requires a separate concurrency audit.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{db: db, dialect: driver, q: newQueries(db, driver)}
	fail := func() (*Store, error) { db.Close(); return nil, errors.New("cannot initialize database") }
	if err := db.PingContext(ctx); err != nil {
		return fail()
	}
	if driver == "sqlite" {
		for _, query := range []string{"PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000", "PRAGMA journal_mode=WAL"} {
			if _, err := db.ExecContext(ctx, query); err != nil {
				return fail()
			}
		}
	}
	// Establish/check the version ledger before changing application tables.
	if _, err := db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS ce_schema (id INTEGER PRIMARY KEY, version INTEGER NOT NULL)"); err != nil {
		return fail()
	}
	var schemaVersion int
	err = db.QueryRowContext(ctx, "SELECT version FROM ce_schema WHERE id=1").Scan(&schemaVersion)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err = db.ExecContext(ctx, "INSERT INTO ce_schema(id,version) VALUES(1,0)"); err != nil {
			return fail()
		}
	} else if err != nil || schemaVersion < 0 || schemaVersion > 5 {
		return fail()
	}
	// Dedicated databases and one Hub process are required. MySQL DDL implicitly
	// commits; idempotent additive statements allow resuming an interrupted upgrade.
	for _, query := range strings.Split(schemaSQL, ";") {
		query = strings.TrimSpace(query)
		if query == "" {
			continue
		}
		query = strings.Replace(query, "CREATE TABLE ", "CREATE TABLE IF NOT EXISTS ", 1)
		query = strings.Replace(query, "CREATE INDEX ", "CREATE INDEX IF NOT EXISTS ", 1)
		if driver == "mysql" {
			query = strings.ReplaceAll(query, "VARCHAR(128)", "VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin")
			trimmed := strings.TrimSpace(query)
			if strings.HasPrefix(trimmed, "CREATE INDEX IF NOT EXISTS ") {
				fields := strings.Fields(trimmed)
				if len(fields) < 8 {
					return fail()
				}
				var exists int
				if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.statistics WHERE table_schema=DATABASE() AND table_name=? AND index_name=?", strings.Trim(fields[7], "`"), strings.Trim(fields[5], "`")).Scan(&exists); err != nil {
					return fail()
				}
				if exists > 0 {
					continue
				}
				query = strings.Replace(query, "CREATE INDEX IF NOT EXISTS ", "CREATE INDEX ", 1)
			} else if strings.HasPrefix(trimmed, "CREATE TABLE") {
				query += " ENGINE=InnoDB"
			}
		}
		if _, err = db.ExecContext(ctx, query); err != nil {
			return fail()
		}
	}
	if _, err = db.ExecContext(ctx, "UPDATE ce_schema SET version=5 WHERE id=1"); err != nil {
		return fail()
	}
	if err = s.RecoverOneBotActions(ctx, time.Now().Unix()); err != nil {
		return fail()
	}
	return s, nil
}

//go:embed sql/schema.sql
var schemaSQL string

func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error {
	if err := s.db.PingContext(ctx); err != nil {
		return errors.New("database unavailable")
	}
	return nil
}
func (s *Store) query(q string) string {
	if s.dialect != "postgres" {
		return q
	}
	var b strings.Builder
	n := 0
	for _, c := range q {
		if c == '?' {
			n++
			fmt.Fprintf(&b, "$%d", n)
		} else {
			b.WriteRune(c)
		}
	}
	return b.String()
}
func ValidID(id string) bool { return idPattern.MatchString(id) }
func (e Endpoint) Validate() error {
	if !ValidID(e.ID) || !e.Type.Valid() || !utf8.ValidString(e.Name) || len([]rune(e.Name)) < 1 || len([]rune(e.Name)) > 128 || strings.TrimSpace(e.Name) == "" {
		return ErrInvalid
	}
	return nil
}
func ValidCapabilities(caps []string) bool {
	if caps == nil || len(caps) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, c := range caps {
		if !capPattern.MatchString(c) || seen[c] {
			return false
		}
		seen[c] = true
	}
	return true
}
func NewID() string { return rand.Text() }
func (s *Store) CreateEndpoint(ctx context.Context, e Endpoint) error {
	if err := e.Validate(); err != nil {
		return err
	}
	if _, err := s.GetEndpoint(ctx, e.ID); err == nil {
		return ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := s.q.create(ctx, e); err != nil {
		return errors.New("cannot create endpoint")
	}
	return nil
}
func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
func endpointFromRow(row endpointRow) Endpoint {
	return Endpoint{ID: row.ID, Type: domain.EndpointType(row.Kind), Name: row.Name, Enabled: row.Enabled != 0}
}
func (s *Store) GetEndpoint(ctx context.Context, id string) (Endpoint, error) {
	row, err := s.q.get(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Endpoint{}, ErrNotFound
	}
	if err != nil {
		return Endpoint{}, errors.New("cannot read endpoint")
	}
	return endpointFromRow(row), nil
}
func (s *Store) ListEndpoints(ctx context.Context, after string, limit int) ([]Endpoint, error) {
	if limit < 1 || limit > 200 {
		return nil, ErrInvalid
	}
	rows, err := s.q.list(ctx, after, int64(limit))
	if err != nil {
		return nil, errors.New("cannot list endpoints")
	}
	result := []Endpoint{}
	for _, row := range rows {
		result = append(result, endpointFromRow(row))
	}
	return result, nil
}
func (s *Store) UpdateEndpoint(ctx context.Context, e Endpoint) error {
	if err := e.Validate(); err != nil {
		return err
	}
	old, err := s.GetEndpoint(ctx, e.ID)
	if err != nil {
		return err
	}
	if old.Type != e.Type {
		return ErrInvalid
	}
	if err := s.q.update(ctx, e); err != nil {
		return errors.New("cannot update endpoint")
	}
	return nil
}
func (s *Store) DeleteEndpoint(ctx context.Context, id string) error {
	n, err := s.q.remove(ctx, id)
	if err != nil {
		return errors.New("cannot delete endpoint")
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func hashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
func (s *Store) IssueCredential(ctx context.Context, id string, caps []string) (IssuedCredential, error) {
	if !ValidCapabilities(caps) {
		return IssuedCredential{}, ErrInvalid
	}
	if _, err := s.GetEndpoint(ctx, id); err != nil {
		return IssuedCredential{}, err
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return IssuedCredential{}, errors.New("cannot generate credential")
	}
	token := "ce_" + hex.EncodeToString(tokenBytes)
	c := Credential{ID: NewID(), EndpointID: id, Capabilities: append([]string{}, caps...), CreatedAt: time.Now().Unix()}
	raw, _ := json.Marshal(caps)
	row := credentialRow{ID: c.ID, EndpointID: id, TokenHash: hashToken(token), Capabilities: string(raw), CreatedAt: c.CreatedAt}
	if err := s.q.issue(ctx, row); err != nil {
		return IssuedCredential{}, errors.New("cannot issue credential")
	}
	return IssuedCredential{Credential: c, Token: token}, nil
}
func (s *Store) ListCredentials(ctx context.Context, id string) ([]Credential, error) {
	if _, err := s.GetEndpoint(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.q.credentials(ctx, id)
	if err != nil {
		return nil, errors.New("cannot list credentials")
	}
	result := []Credential{}
	for _, row := range rows {
		c := Credential{ID: row.ID, EndpointID: row.EndpointID, CreatedAt: row.CreatedAt}
		if err := json.Unmarshal([]byte(row.Capabilities), &c.Capabilities); err != nil {
			return nil, errors.New("invalid stored credential")
		}
		result = append(result, c)
	}
	return result, nil
}
func (s *Store) RevokeCredential(ctx context.Context, id, credentialID string) error {
	n, err := s.q.revoke(ctx, credentialID, id)
	if err != nil {
		return errors.New("cannot revoke credential")
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) Authenticate(ctx context.Context, token string) (Endpoint, Credential, error) {
	if len(token) != 67 || !strings.HasPrefix(token, "ce_") {
		return Endpoint{}, Credential{}, ErrUnauthenticated
	}
	row, err := s.q.authenticate(ctx, hashToken(token))
	if errors.Is(err, sql.ErrNoRows) {
		return Endpoint{}, Credential{}, ErrUnauthenticated
	}
	if err != nil {
		return Endpoint{}, Credential{}, errors.New("cannot authenticate endpoint")
	}
	if row.Enabled != 1 {
		return Endpoint{}, Credential{}, ErrUnauthenticated
	}
	e := Endpoint{ID: row.ID, Type: domain.EndpointType(row.Kind), Name: row.Name, Enabled: true}
	c := Credential{ID: row.CredentialID, EndpointID: e.ID, CreatedAt: row.CreatedAt}
	if err := json.Unmarshal([]byte(row.Capabilities), &c.Capabilities); err != nil {
		return Endpoint{}, Credential{}, errors.New("invalid stored credential")
	}
	return e, c, nil
}
