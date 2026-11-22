package store

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestBindingChallengeIsSingleUseAndUnique(t *testing.T) {
	db, err := Open(t.TempDir() + "/bindings.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	challenge, err := db.CreateBindingChallenge(ctx, "player-1", "", "", 5*time.Minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := db.ConfirmBinding(ctx, challenge.Code, "onebot11", "qq-1", "Player", "")
	if err != nil {
		t.Fatal(err)
	}
	if binding.PlayerUUID != "player-1" || binding.AccountID != "qq-1" {
		t.Fatalf("unexpected binding %#v", binding)
	}
	if _, err = db.ConfirmBinding(ctx, challenge.Code, "onebot11", "qq-2", "Another", ""); !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("challenge replay accepted: %v", err)
	}
	other, err := db.CreateBindingChallenge(ctx, "player-2", "", "", 5*time.Minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ConfirmBinding(ctx, other.Code, "onebot11", "qq-1", "Private profile", "different-group"); err != nil {
		t.Fatalf("same platform identity should link another game player: %v", err)
	}
	items, err := db.ListBindings(ctx)
	if err != nil || len(items) != 2 {
		t.Fatalf("expected two game players linked to one social identity: %#v %v", items, err)
	}
}

func TestBindingChallengeConcurrentConsumeOnlyOnce(t *testing.T) {
	db, err := Open(t.TempDir() + "/concurrent.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	challenge, err := db.CreateBindingChallenge(ctx, "player-1", "", "", 5*time.Minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	success := 0
	var mu sync.Mutex
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := db.ConfirmBinding(ctx, challenge.Code, "onebot11", string(rune('a'+i)), "", "")
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("expected one successful consume, got %d", success)
	}
	items, err := db.ListBindings(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("unexpected bindings %#v %v", items, err)
	}
}

func TestSocialAccountCanBindAtMostFourPlayersAndChallengeIsNotGroupLocked(t *testing.T) {
	db, err := Open(t.TempDir() + "/limit.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	for i := 1; i <= 4; i++ {
		challenge, err := db.CreateBindingChallenge(ctx, fmt.Sprintf("p%d", i), "", "", 5*time.Minute, 5)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ConfirmBinding(ctx, challenge.Code, "onebot11", "same-social", "", fmt.Sprintf("group-%d", i)); err != nil {
			t.Fatalf("link %d: %v", i, err)
		}
	}
	challenge, err := db.CreateBindingChallenge(ctx, "p5", "", "", 5*time.Minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ConfirmBinding(ctx, challenge.Code, "onebot11", "same-social", "", "another-group"); !errors.Is(err, ErrBindingLimit) {
		t.Fatalf("fifth link err=%v, want ErrBindingLimit", err)
	}
	var attempts int
	if err = db.db.QueryRow(`SELECT attempts FROM binding_challenges WHERE code=?`, challenge.Code).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("rejected confirmation attempts = %d, %v; want persisted attempt 1", attempts, err)
	}
}

func TestBindingChallengeExpiryAndUnbindAudit(t *testing.T) {
	db, err := Open(t.TempDir() + "/expiry.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	challenge, err := db.CreateBindingChallenge(ctx, "player-1", "", "", time.Minute, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.db.Exec(`UPDATE binding_challenges SET expires_at=? WHERE code=?`, time.Now().Add(-time.Second), challenge.Code); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ConfirmBinding(ctx, challenge.Code, "onebot11", "qq-1", "", ""); !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf("expired challenge accepted: %v", err)
	}
	fresh, err := db.CreateBindingChallenge(ctx, "player-1", "", "", time.Minute, 2)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := db.ConfirmBinding(ctx, fresh.Code, "onebot11", "qq-1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.DeleteBinding(ctx, binding.ID); err != nil {
		t.Fatal(err)
	}
	bound, err := db.IsPlayerBound(ctx, "player-1")
	if err != nil || bound {
		t.Fatalf("binding still active: %v %v", bound, err)
	}
	audit, err := db.BindingAudit(ctx, 20)
	if err != nil || len(audit) != 2 {
		t.Fatalf("expected bind/unbind audit, got %#v %v", audit, err)
	}
}
