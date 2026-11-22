package application

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// 用内存替身验证用例，不需要 HTTP、真实适配器或数据库连接。
type recordRepositoryFake struct {
	items                               map[string]Record
	getErr, listErr, saveErr, deleteErr error
	saves, deletes                      int
	beforeSave                          func(Record)
}

func newRecordRepositoryFake() *recordRepositoryFake {
	return &recordRepositoryFake{items: make(map[string]Record)}
}
func (f *recordRepositoryFake) Get(_ context.Context, kind, id string) (Record, error) {
	if f.getErr != nil {
		return Record{}, f.getErr
	}
	item, ok := f.items[id]
	if !ok || item.Kind != kind {
		return Record{}, sql.ErrNoRows
	}
	return item, nil
}
func (f *recordRepositoryFake) List(_ context.Context, kind string) ([]Record, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	items := []Record{}
	for _, item := range f.items {
		if item.Kind == kind {
			items = append(items, item)
		}
	}
	return items, nil
}
func (f *recordRepositoryFake) Save(_ context.Context, item Record) (Record, error) {
	f.saves++
	if f.beforeSave != nil {
		f.beforeSave(item)
	}
	if f.saveErr != nil {
		return Record{}, f.saveErr
	}
	if item.ID == "" {
		item.ID = fmt.Sprintf("record-%d", f.saves)
	}
	f.items[item.ID] = item
	return item, nil
}
func (f *recordRepositoryFake) Delete(_ context.Context, kind, id string) error {
	f.deletes++
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, err := f.Get(context.Background(), kind, id); err != nil {
		return err
	}
	delete(f.items, id)
	return nil
}
func requireValidation(t *testing.T, err error, message string) {
	t.Helper()
	var validation *ValidationError
	if !errors.As(err, &validation) || err.Error() != message {
		t.Fatalf("validation = %v, want %q", err, message)
	}
}

func TestRecordsRejectManualGroupsAndUnknownKinds(t *testing.T) {
	for _, kind := range []string{"groups", "not-a-kind", ""} {
		t.Run(kind, func(t *testing.T) {
			repo := newRecordRepositoryFake()
			service := NewRecords(repo)
			ctx := context.Background()
			_, err := service.List(ctx, kind)
			requireValidation(t, err, "unknown record kind")
			_, err = service.Create(ctx, Record{Kind: kind})
			requireValidation(t, err, "群组列表由平台适配器 API 提供，不支持手动配置")
			_, err = service.Update(ctx, Record{Kind: kind})
			requireValidation(t, err, "群组列表由平台适配器 API 提供，不支持手动配置")
			err = service.Delete(ctx, kind, "id")
			requireValidation(t, err, "群组列表由平台适配器 API 提供，不支持手动配置")
			if repo.saves != 0 || repo.deletes != 0 {
				t.Fatal("invalid kinds reached persistence")
			}
		})
	}
}

func TestRecordsServerTokenDefaultsAndPreservation(t *testing.T) {
	repo := newRecordRepositoryFake()
	service := NewRecords(repo)
	ctx := context.Background()
	for _, detail := range []string{"", "{}", "null", `{"Token":"legacy","port":25565}`, `{"serverToken":"legacy2"}`} {
		item, err := service.Create(ctx, Record{Kind: "servers", Name: "server", Detail: detail, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		token := serverToken(item.Detail)
		if token == "" {
			t.Fatalf("token missing for %q", detail)
		}
		if strings.Contains(detail, "legacy") && item.Detail != detail {
			t.Fatalf("explicit token changed: %s", item.Detail)
		}
		updated, err := service.Update(ctx, Record{ID: item.ID, Kind: "servers", Name: "renamed", Detail: `{"port":1234}`})
		if err != nil || serverToken(updated.Detail) != token || !strings.Contains(updated.Detail, `"port":1234`) {
			t.Fatalf("token was not preserved with other fields: %#v %v", updated, err)
		}
		updated.Detail = `{"token":"explicit-replacement"}`
		updated, err = service.Update(ctx, updated)
		if err != nil || serverToken(updated.Detail) != "explicit-replacement" {
			t.Fatalf("explicit token replacement failed: %#v %v", updated, err)
		}
	}
	saves := repo.saves
	for _, detail := range []string{"{", "[]", `"text"`} {
		_, err := service.Create(ctx, Record{Kind: "servers", Name: "bad", Detail: detail})
		requireValidation(t, err, "server config must be valid JSON")
	}
	if repo.saves != saves {
		t.Fatal("malformed server persisted")
	}
}

func TestRecordsCRUDAndAdapterRedaction(t *testing.T) {
	repo := newRecordRepositoryFake()
	service := NewRecords(repo)
	ctx := context.Background()
	item, err := service.Create(ctx, Record{Kind: "adapters", Name: "adapter", Detail: `{"adapterType":"QQBot","config":{"appSecret":"never-expose","appId":"app"}}`})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(item.Detail, "never-expose") || !strings.Contains(item.Detail, `"appSecretConfigured":true`) {
		t.Fatalf("create leaked credentials: %s", item.Detail)
	}
	items, err := service.List(ctx, "adapters")
	if err != nil || len(items) != 1 || strings.Contains(items[0].Detail, "never-expose") {
		t.Fatalf("list = %#v, %v", items, err)
	}
	item = repo.items[item.ID]
	item.Name = "renamed"
	updated, err := service.Update(ctx, item)
	if err != nil || updated.Name != "renamed" || strings.Contains(updated.Detail, "never-expose") {
		t.Fatalf("update = %#v, %v", updated, err)
	}
	if !strings.Contains(repo.items[item.ID].Detail, "never-expose") {
		t.Fatal("redaction mutated stored credentials")
	}
	if err := service.Delete(ctx, "adapters", item.ID); err != nil {
		t.Fatal(err)
	}
	items, err = service.List(ctx, "adapters")
	if err != nil || len(items) != 0 {
		t.Fatalf("delete failed: %#v %v", items, err)
	}
	// 返回空数组而非 null 的契约保留。
	encoded, _ := json.Marshal(items)
	if string(encoded) != "[]" {
		t.Fatalf("empty list = %s", encoded)
	}
}

func TestRecordsPreserveRepositoryErrors(t *testing.T) {
	repo := newRecordRepositoryFake()
	service := NewRecords(repo)
	failure := errors.New("storage failure")
	ctx := context.Background()
	repo.listErr = failure
	if _, err := service.List(ctx, "servers"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	repo.saveErr = failure
	if _, err := service.Create(ctx, Record{Kind: "players"}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, Record{Kind: "players"}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	repo.deleteErr = failure
	if err := service.Delete(ctx, "players", "id"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
