// SPDX-License-Identifier: AGPL-3.0-only
package store

import (
	"context"
	"database/sql"
	"errors"
	"github.com/EasybotCE/easybotce/internal/pipeline"
	"os"
	"path/filepath"
	"testing"
)

func TestRoutingLifecycle(t *testing.T) {
	ctx := context.Background()
	driver, dsn := os.Getenv("EASYBOTCE_TEST_DB_DRIVER"), os.Getenv("EASYBOTCE_TEST_DB_DSN")
	if driver == "" {
		driver = "sqlite"
		dsn = ":memory:"
	}
	db, err := Open(ctx, driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	prefix := NewID()
	a, b := prefix+"a", prefix+"b"
	ruleID, bindingID := prefix+"rule", prefix+"binding"
	for _, id := range []string{a, b} {
		if err = db.CreateEndpoint(ctx, Endpoint{id, "mc-server", id, true}); err != nil {
			t.Fatal(err)
		}
	}
	defer db.DeleteEndpoint(ctx, a)
	defer db.DeleteEndpoint(ctx, b)
	defer func() { db.DeleteRoutingBinding(ctx, bindingID); db.DeleteRuleGroup(ctx, ruleID) }()
	rule := RuleGroup{ID: ruleID, Name: "shared", Rules: pipeline.Rules{Transforms: []pipeline.Transform{}, Template: "{text}"}}
	if err = db.CreateRuleGroup(ctx, rule); err != nil {
		t.Fatal(err)
	}
	if err = db.CreateRuleGroup(ctx, rule); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	binding := RoutingBinding{ID: bindingID, SourceEndpointID: a, TargetEndpointID: b, SourceChannel: "*", TargetChannel: "chat", RuleGroupID: ruleID, Enabled: true}
	if err = db.CreateRoutingBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err = db.DeleteRuleGroup(ctx, ruleID); !errors.Is(err, ErrConflict) {
		t.Fatal("deleted referenced rule", err)
	}
	routes, err := db.ResolveRoutes(ctx, a, "group")
	if err != nil || len(routes) != 1 || routes[0].Binding.ID != bindingID {
		t.Fatal(routes, err)
	}
	rule.Rules.Template = "changed"
	if err = db.UpdateRuleGroup(ctx, rule); err != nil {
		t.Fatal(err)
	}
	routes, err = db.ResolveRoutes(ctx, a, "other")
	if err != nil || len(routes) != 1 || routes[0].Rules.Template != "changed" {
		t.Fatal(routes, err)
	}
	binding.Enabled = false
	if err = db.UpdateRoutingBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	routes, err = db.ResolveRoutes(ctx, a, "chat")
	if err != nil || len(routes) != 0 {
		t.Fatal(routes, err)
	}
	binding.Enabled = true
	binding.SourceChannel = "chat"
	if err = db.UpdateRoutingBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	routes, err = db.ResolveRoutes(ctx, a, "other")
	if err != nil || len(routes) != 0 {
		t.Fatal(routes, err)
	}
	if err = db.UpdateEndpoint(ctx, Endpoint{b, "mc-server", b, false}); err != nil {
		t.Fatal(err)
	}
	routes, err = db.ResolveRoutes(ctx, a, "chat")
	if err != nil || len(routes) != 0 {
		t.Fatal(routes, err)
	}
	if err = db.DeleteEndpoint(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err = db.GetRoutingBinding(ctx, bindingID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cascade", err)
	}
	if err = db.DeleteRuleGroup(ctx, ruleID); err != nil {
		t.Fatal(err)
	}
}
func TestUpgradeV1PreservesCredentials(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "upgrade.db")
	db, err := Open(ctx, "sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.CreateEndpoint(ctx, Endpoint{"lobby", "mc-server", "lobby", true}); err != nil {
		t.Fatal(err)
	}
	c, err := db.IssueCredential(ctx, "lobby", []string{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"DROP TABLE ce_routing_bindings", "DROP TABLE ce_rule_groups", "UPDATE ce_schema SET version=1"} {
		if _, err = db.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	db, err = Open(ctx, "sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, _, err = db.Authenticate(ctx, c.Token); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ListRuleGroups(ctx, "", 10); err != nil {
		t.Fatal(err)
	}
}
func TestFutureSchemaDoesNotCreateApplicationTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for _, q := range []string{"CREATE TABLE ce_schema(id INTEGER PRIMARY KEY,version INTEGER)", "INSERT INTO ce_schema VALUES(1,99)"} {
		if _, err = raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if s, err := Open(context.Background(), "sqlite", path); err == nil {
		s.Close()
		t.Fatal("future accepted")
	}
	var n int
	if err = raw.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='ce_endpoints'").Scan(&n); err != nil || n != 0 {
		t.Fatal("future DB modified", n, err)
	}
}
