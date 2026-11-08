// SPDX-License-Identifier: AGPL-3.0-only
package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOneBotActionsPersistAndRecover(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "actions.db")
	db, err := Open(ctx, "sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	action := OneBotAction{ID: NewID(), EndpointID: NewID(), Status: "sent", CreatedAt: 100, UpdatedAt: 110}
	if err := db.SaveOneBotAction(ctx, action); err != nil {
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
	got, err := db.GetOneBotAction(ctx, action.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "timeout" || got.CreatedAt != 100 || got.UpdatedAt < 110 {
		t.Fatalf("recovered action = %#v", got)
	}
	retcode := 0
	got.Status = "succeeded"
	got.Retcode = &retcode
	if err := db.SaveOneBotAction(ctx, got); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListOneBotActions(ctx, action.EndpointID, 0, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != action.ID || list[0].Status != "succeeded" || list[0].Retcode == nil || *list[0].Retcode != 0 {
		t.Fatalf("action list = %#v", list)
	}
	if _, err := db.ListOneBotActions(ctx, "", 0, "", 101); err == nil {
		t.Fatal("accepted an excessive page size")
	}
}

func TestOneBotActionCursorIncludesSameTimestamp(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, "sqlite", filepath.Join(t.TempDir(), "cursor.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	endpoint := NewID()
	for i := 0; i < 3; i++ {
		a := OneBotAction{ID: NewID(), EndpointID: endpoint, Status: "succeeded", CreatedAt: 1234, UpdatedAt: 1234}
		if err := db.SaveOneBotAction(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	first, err := db.ListOneBotActions(ctx, endpoint, 0, "", 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	last := first[len(first)-1]
	second, err := db.ListOneBotActions(ctx, endpoint, last.CreatedAt, last.ID, 2)
	if err != nil || len(second) != 1 {
		t.Fatalf("second page = %#v, %v", second, err)
	}
	if second[0].ID == last.ID || second[0].CreatedAt != last.CreatedAt {
		t.Fatalf("cursor skipped or repeated a same-timestamp action: %#v", second)
	}
}
