// SPDX-License-Identifier: AGPL-3.0-only
package store

import (
	"context"
	"errors"
	"github.com/EasybotCE/easybotce/internal/domain"
	"path/filepath"
	"testing"
	"time"
)

func TestAccountBindingAndPlayerState(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, "sqlite", filepath.Join(t.TempDir(), "accounts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mcID, botID := "mc-"+NewID(), "bot-"+NewID()
	if err = db.CreateEndpoint(ctx, Endpoint{ID: mcID, Type: domain.MCServer, Name: "Server", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err = db.CreateEndpoint(ctx, Endpoint{ID: botID, Type: domain.ChatBot, Name: "Bot", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	code, _, err := db.CreateAccountChallenge(ctx, mcID, "player-1", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := db.ClaimAccountChallenge(ctx, code, botID, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ClaimAccountChallenge(ctx, code, botID, "user-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("challenge reused: %v", err)
	}
	if _, err = db.ClaimAccountChallenge(ctx, func() string {
		c, _, e := db.CreateAccountChallenge(ctx, mcID, "player-2", 5*time.Minute)
		if e != nil {
			t.Fatal(e)
		}
		return c
	}(), botID, "user-1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate platform identity accepted: %v", err)
	}
	got, err := db.ListAccountBindings(ctx, mcID, "player-1")
	if err != nil || len(got) != 1 || got[0].ID != binding.ID {
		t.Fatalf("bindings: %+v, %v", got, err)
	}
	state := PlayerState{EndpointID: mcID, PlayerID: "player-1", PlayerName: "Steve", Online: true, World: "world", X: 1, Y: 64, Z: -2}
	if err = db.PutPlayerState(ctx, state); err != nil {
		t.Fatal(err)
	}
	gotState, err := db.GetPlayerState(ctx, mcID, "player-1")
	if err != nil || !gotState.Online || gotState.PlayerName != "Steve" || gotState.Y != 64 {
		t.Fatalf("state: %+v, %v", gotState, err)
	}
	if err = db.DeleteAccountBinding(ctx, binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ListAccountBindings(ctx, mcID, "player-1"); err != nil {
		t.Fatal(err)
	}
}
