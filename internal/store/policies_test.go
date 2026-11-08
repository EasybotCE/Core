package store

import (
	"context"
	"testing"
	"time"
)

func TestPlayerPolicyAndAudit(t *testing.T) {
	s, err := Open(context.Background(), "sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now().Unix()
	for _, p := range []PlayerPolicy{
		{ID: "deny-1", EndpointID: "srv-a", PlayerID: "player-1", Mode: "deny", Action: "ban", Reason: "abuse", CreatedAt: now},
		{ID: "allow-1", EndpointID: "*", PlayerID: "player-2", Mode: "allow", Action: "none", Reason: "trusted", CreatedAt: now},
		{ID: "expired", EndpointID: "srv-a", PlayerID: "player-3", Mode: "deny", Action: "kick", Reason: "old", ExpiresAt: now - 1, CreatedAt: now},
	} {
		if err := s.PutPlayerPolicy(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListPlayerPolicies(context.Background(), "srv-a", "player-1")
	if err != nil || len(got) != 1 || got[0].Action != "ban" {
		t.Fatalf("policy query = %#v, %v", got, err)
	}
	got, err = s.ListPlayerPolicies(context.Background(), "srv-a", "player-2")
	if err != nil || len(got) != 1 || got[0].Mode != "allow" {
		t.Fatalf("global policy query = %#v, %v", got, err)
	}
	got, err = s.ListPlayerPolicies(context.Background(), "srv-a", "player-3")
	if err != nil || len(got) != 0 {
		t.Fatalf("expired policy query = %#v, %v", got, err)
	}
	if err := s.AddAudit(context.Background(), "admin", "player_policy.create", "deny-1"); err != nil {
		t.Fatal(err)
	}
	audit, err := s.ListAudit(context.Background(), 10)
	if err != nil || len(audit) != 1 || audit[0].Resource != "deny-1" {
		t.Fatalf("audit = %#v, %v", audit, err)
	}
	if err := s.DeletePlayerPolicy(context.Background(), "deny-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePlayerPolicy(context.Background(), "deny-1"); err != ErrNotFound {
		t.Fatalf("second delete = %v", err)
	}
}
