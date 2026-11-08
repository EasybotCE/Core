// SPDX-License-Identifier: AGPL-3.0-only
package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EasybotCE/easybotce/internal/domain"
)

// The same acceptance test can run against a dedicated external test database.
// It creates randomized IDs and removes only the records it created.
func TestStoreLifecycle(t *testing.T) {
	driver, dsn := "sqlite", filepath.Join(t.TempDir(), "hub.db")
	if v := os.Getenv("EASYBOTCE_TEST_DB_DRIVER"); v != "" {
		driver = v
		dsn = os.Getenv("EASYBOTCE_TEST_DB_DSN")
		if dsn == "" {
			t.Fatal("test DSN required")
		}
	}
	ctx := context.Background()
	db, err := Open(ctx, driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	id := "test-" + NewID()
	endpoint := Endpoint{ID: id, Type: domain.EndpointType("mc-server"), Name: "大厅", Enabled: true}
	if err := db.CreateEndpoint(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	defer db.DeleteEndpoint(ctx, id)
	if err := db.CreateEndpoint(ctx, endpoint); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
	// IDs must be case-sensitive, including MySQL primary and foreign keys.
	second := endpoint
	second.ID = strings.ToUpper(id)
	if err := db.CreateEndpoint(ctx, second); err != nil {
		t.Fatalf("case sensitivity: %v", err)
	}
	defer db.DeleteEndpoint(ctx, second.ID)
	if _, err := db.GetEndpoint(ctx, "missing-"+id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	issued, err := db.IssueCredential(ctx, id, []string{"chat.receive"})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Token == "" || issued.EndpointID != id {
		t.Fatal("bad issued credential")
	}
	authenticated, credential, err := db.Authenticate(ctx, issued.Token)
	if err != nil || authenticated.ID != id || credential.ID != issued.ID || len(credential.Capabilities) != 1 {
		t.Fatalf("authenticate: %v", err)
	}
	var stored string
	if err := db.db.QueryRowContext(ctx, db.query("SELECT token_hash FROM ce_credentials WHERE id=?"), issued.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == issued.Token || strings.Contains(stored, "ce_") || len(stored) != 64 {
		t.Fatal("token not hashed")
	}
	list, err := db.ListCredentials(ctx, id)
	if err != nil || len(list) != 1 {
		t.Fatalf("list credentials: %v", err)
	}
	if _, _, err := db.Authenticate(ctx, issued.Token+"x"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("accepted wrong credential")
	}
	endpoint.Enabled = false
	if err := db.UpdateEndpoint(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.Authenticate(ctx, issued.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("disabled endpoint authenticated")
	}
	endpoint.Enabled = true
	if err := db.UpdateEndpoint(ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	if err := db.RevokeCredential(ctx, id, issued.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.Authenticate(ctx, issued.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("revoked token authenticated")
	}
	if err := db.RevokeCredential(ctx, id, issued.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double revoke: %v", err)
	}
	issued, err = db.IssueCredential(ctx, id, []string{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteEndpoint(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.Authenticate(ctx, issued.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal("deleted endpoint authenticated")
	}
	var n int
	if err := db.db.QueryRowContext(ctx, db.query("SELECT COUNT(*) FROM ce_credentials WHERE endpoint_id=?"), id).Scan(&n); err != nil || n != 0 {
		t.Fatalf("cascade failed: n=%d %v", n, err)
	}
}
func TestSQLitePersistenceAndPagination(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "persist.db")
	db, err := Open(ctx, "sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := db.CreateEndpoint(ctx, Endpoint{id, "plugin", id, true}); err != nil {
			t.Fatal(err)
		}
	}
	issued, err := db.IssueCredential(ctx, "a", []string{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(ctx, "sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, _, err := db.Authenticate(ctx, issued.Token); err != nil {
		t.Fatalf("persistence: %v", err)
	}
	page, err := db.ListEndpoints(ctx, "", 2)
	if err != nil || len(page) != 2 || page[0].ID != "a" || page[1].ID != "b" {
		t.Fatalf("first page: %v %v", page, err)
	}
	page, err = db.ListEndpoints(ctx, "b", 2)
	if err != nil || len(page) != 1 || page[0].ID != "c" {
		t.Fatalf("second page: %v %v", page, err)
	}
	if _, err := db.ListEndpoints(ctx, "", 201); !errors.Is(err, ErrInvalid) {
		t.Fatal("unbounded page accepted")
	}
	bad := Endpoint{"x", "mc-server", "x", true}
	if err := db.CreateEndpoint(ctx, bad); err != nil {
		t.Fatal(err)
	}
	bad.Type = "plugin"
	if err := db.UpdateEndpoint(ctx, bad); !errors.Is(err, ErrInvalid) {
		t.Fatal("type mutation accepted")
	}
	for _, caps := range [][]string{nil, {"x", "x"}, {"BAD"}} {
		if _, err := db.IssueCredential(ctx, "a", caps); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid caps accepted")
		}
	}
}
func TestOpenDoesNotLeakDSN(t *testing.T) {
	secret := "password-do-not-log"
	if _, err := Open(context.Background(), "sqlite", "file:?mode="+secret); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("unsafe startup error", err)
	}
	if _, err := Open(context.Background(), "unsupported", secret); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("unsafe driver error", err)
	}
}
func TestFutureSchemaRejected(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := Open(ctx, "sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.ExecContext(ctx, "UPDATE ce_schema SET version=999"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if next, err := Open(ctx, "sqlite", path); err == nil {
		next.Close()
		t.Fatal("future schema accepted")
	}
}
