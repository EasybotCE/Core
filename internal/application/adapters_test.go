package application

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"easybot/internal/adapter/onebot11"
)

type adapterRuntimeFake struct {
	state                   onebot11.State
	operationErr, statusErr error
	calls                   []string
	deadlines               map[string]time.Duration
	observedSave            bool
	repository              *recordRepositoryFake
	message                 string
}

func (f *adapterRuntimeFake) record(ctx context.Context, operation string) error {
	f.calls = append(f.calls, operation)
	if deadline, ok := ctx.Deadline(); ok {
		if f.deadlines == nil {
			f.deadlines = make(map[string]time.Duration)
		}
		f.deadlines[operation] = time.Until(deadline)
	}
	return f.operationErr
}
func (f *adapterRuntimeFake) Start(ctx context.Context, _ string) error {
	return f.record(ctx, "start")
}
func (f *adapterRuntimeFake) Stop(ctx context.Context, _ string) error { return f.record(ctx, "stop") }
func (f *adapterRuntimeFake) Restart(ctx context.Context, id string) error {
	if f.repository != nil {
		f.observedSave = f.repository.items[id].Name == "updated"
	}
	return f.record(ctx, "restart")
}
func (f *adapterRuntimeFake) Status(string) (onebot11.Status, error) {
	return onebot11.Status{State: f.state}, f.statusErr
}
func (f *adapterRuntimeFake) Test(ctx context.Context, _ string) (map[string]any, error) {
	err := f.record(ctx, "test")
	return map[string]any{"connected": true}, err
}
func (f *adapterRuntimeFake) Capabilities(ctx context.Context, _ string) ([]string, error) {
	return []string{"group_list"}, f.record(ctx, "capabilities")
}
func (f *adapterRuntimeFake) Send(ctx context.Context, _, _, _, message string) (string, error) {
	f.message = message
	return "message-id", f.record(ctx, "send")
}
func (f *adapterRuntimeFake) Groups(ctx context.Context, _ string) ([]map[string]any, error) {
	return []map[string]any{{"group_id": "123"}}, f.record(ctx, "groups")
}

func TestAdaptersCreateDefaultsValidationAndRedaction(t *testing.T) {
	for _, tc := range []struct{ kind, config, secret string }{
		{"Onebot11", `{"url":"ws://localhost:3001","accessToken":"one-secret"}`, "one-secret"},
		{"QQBot", `{"AppID":"app","AppSecret":"qq-secret"}`, "qq-secret"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			repo := newRecordRepositoryFake()
			service := NewAdapters(repo, nil, nil)
			item, err := service.Create(context.Background(), tc.kind, "", tc.config)
			if err != nil {
				t.Fatal(err)
			}
			if item.Name != tc.kind || !item.Enabled || item.Kind != "adapters" {
				t.Fatalf("defaults missing: %#v", item)
			}
			if strings.Contains(item.Detail, tc.secret) || !strings.Contains(item.Detail, "Configured") {
				t.Fatalf("secret leaked: %s", item.Detail)
			}
			if !strings.Contains(repo.items[item.ID].Detail, tc.secret) {
				t.Fatal("stored secret missing")
			}
			list, err := service.List(context.Background())
			if err != nil || len(list) != 1 || strings.Contains(list[0].Detail, tc.secret) {
				t.Fatalf("list = %#v %v", list, err)
			}
		})
	}
	repo := newRecordRepositoryFake()
	service := NewAdapters(repo, nil, nil)
	if _, err := service.Create(context.Background(), "Telegram", "", "{}"); !errors.Is(err, ErrUnsupportedAdapter) {
		t.Fatal(err)
	}
	_, err := service.Create(context.Background(), "Onebot11", "", "{")
	requireValidation(t, err, "adapter config must be valid JSON")
	for _, config := range []string{"", "{}", "null", "[]"} {
		_, err := service.Create(context.Background(), "QQBot", "", config)
		var apiErr *onebot11.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != "adapter_config_invalid" {
			t.Fatalf("invalid config %q = %v", config, err)
		}
	}
	if repo.saves != 0 {
		t.Fatal("invalid adapter saved")
	}
}

func TestAdaptersUpdatePreservesSecretsAndFields(t *testing.T) {
	for _, tc := range []struct{ kind, old, next, expected string }{
		{"Onebot11", `{"url":"ws://localhost:3001","accessToken":"old"}`, `{"url":"ws://localhost:3002","accessTokenConfigured":true}`, "old"},
		{"Onebot11", `{"WsUrl":"ws://localhost:3001","AccessToken":"old"}`, `{"WsUrl":"ws://localhost:3002","AccessToken":"********"}`, "old"},
		{"Onebot11", `{"url":"ws://localhost:3001","AccessToken":"old"}`, `{"url":"ws://localhost:3002","AccessToken":"new"}`, "new"},
		{"QQBot", `{"AppID":"app","appSecret":"old"}`, `{"AppID":"app","appSecretConfigured":true}`, "old"},
		{"QQBot", `{"AppID":"app","AppSecret":"old"}`, `{"AppID":"app","AppSecret":"new"}`, "new"},
	} {
		t.Run(tc.kind+tc.next, func(t *testing.T) {
			repo := newRecordRepositoryFake()
			detail, _ := json.Marshal(map[string]any{"adapterType": tc.kind, "config": json.RawMessage(tc.old)})
			repo.items["id"] = Record{ID: "id", Kind: "adapters", Name: "old", Detail: string(detail), Enabled: false}
			service := NewAdapters(repo, nil, nil)
			item, err := service.Update(context.Background(), "id", "updated", tc.next)
			if err != nil {
				t.Fatal(err)
			}
			if item.ID != "id" || item.Enabled || item.Name != "updated" {
				t.Fatalf("fields changed: %#v", item)
			}
			if strings.Contains(item.Detail, `:"`+tc.expected+`"`) {
				t.Fatalf("secret returned: %s", item.Detail)
			}
			stored := repo.items["id"]
			if !strings.Contains(stored.Detail, `:"`+tc.expected+`"`) || strings.Contains(stored.Detail, "Configured") {
				t.Fatalf("stored config = %s", stored.Detail)
			}
		})
	}
}

func TestAdaptersUpdateRestartOnlyAfterSuccessfulSave(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		state                          onebot11.State
		statusErr, restartErr, saveErr error
		restarts                       int
	}{
		{name: "connected", state: onebot11.StateConnected, restarts: 1},
		{name: "failed", state: onebot11.StateFailed, restarts: 1},
		{name: "stopped", state: onebot11.StateStopped},
		{name: "unknown-status", statusErr: errors.New("missing")},
		{name: "save-failed", state: onebot11.StateConnected, saveErr: errors.New("disk full")},
		{name: "restart-failed", state: onebot11.StateConnected, restartErr: errors.New("cannot start"), restarts: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRecordRepositoryFake()
			repo.items["id"] = Record{ID: "id", Kind: "adapters", Name: "before", Enabled: true, Detail: `{"adapterType":"Onebot11","config":{"url":"ws://localhost:3001"}}`}
			repo.saveErr = tc.saveErr
			runtime := &adapterRuntimeFake{state: tc.state, statusErr: tc.statusErr, operationErr: tc.restartErr, repository: repo}
			var output bytes.Buffer
			service := NewAdapters(repo, runtime, slog.New(slog.NewTextHandler(&output, nil)))
			_, err := service.Update(context.Background(), "id", "updated", `{"url":"ws://localhost:3002"}`)
			if !errors.Is(err, tc.saveErr) {
				t.Fatalf("update = %v, want %v", err, tc.saveErr)
			}
			if len(runtime.calls) != tc.restarts {
				t.Fatalf("calls = %v", runtime.calls)
			}
			if tc.restarts > 0 && !runtime.observedSave {
				t.Fatal("restart happened before save")
			}
			if tc.restartErr != nil && !strings.Contains(output.String(), "更新配置后重启失败") {
				t.Fatal("restart failure not logged")
			}
		})
	}
}

func TestAdaptersUpdateLookupAndInvalidConfig(t *testing.T) {
	repo := newRecordRepositoryFake()
	service := NewAdapters(repo, nil, nil)
	if _, err := service.Update(context.Background(), "missing", "", "{}"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	repo.items["id"] = Record{ID: "id", Kind: "adapters", Detail: "{"}
	_, err := service.Update(context.Background(), "id", "", "{}")
	requireValidation(t, err, "stored adapter config is invalid")
	repo.items["id"] = Record{ID: "id", Kind: "adapters", Detail: `{"adapterType":"QQBot","config":{"AppID":"app","AppSecret":"secret"}}`}
	for _, config := range []string{"null", "[]", "{"} {
		if _, err := service.Update(context.Background(), "id", "updated", config); err == nil {
			t.Fatalf("invalid update accepted: %s", config)
		}
	}
	if repo.saves != 0 {
		t.Fatal("invalid update reached storage")
	}
}

func TestAdaptersRuntimeOperationsAndTimeouts(t *testing.T) {
	runtime := &adapterRuntimeFake{state: onebot11.StateConnected}
	service := NewAdapters(newRecordRepositoryFake(), runtime, nil)
	ctx := context.Background()
	if status, err := service.Start(ctx, "id"); err != nil || status.State != onebot11.StateConnected {
		t.Fatalf("start: %#v %v", status, err)
	}
	if _, err := service.Stop(ctx, "id"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Restart(ctx, "id"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Test(ctx, "id"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Status("id"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Capabilities(ctx, "id"); err != nil {
		t.Fatal(err)
	}
	if id, err := service.Send(ctx, "id", "group", "123", " text "); err != nil || id != "message-id" || runtime.message != " text " {
		t.Fatalf("send: %s %v", id, err)
	}
	if _, err := service.Groups(ctx, "id"); err != nil {
		t.Fatal(err)
	}
	for op, expected := range map[string]time.Duration{"stop": 5 * time.Second, "test": 10 * time.Second, "send": 10 * time.Second, "groups": 10 * time.Second} {
		if remaining := runtime.deadlines[op]; remaining <= 0 || remaining > expected {
			t.Fatalf("%s timeout = %s", op, remaining)
		}
	}
	before := len(runtime.calls)
	for _, message := range []string{"", "   ", strings.Repeat("x", 4001)} {
		_, err := service.Send(ctx, "id", "group", "123", message)
		requireValidation(t, err, "peerId and a message of 1-4000 bytes are required")
	}
	if len(runtime.calls) != before {
		t.Fatal("invalid message sent")
	}
	failure := errors.New("runtime failure")
	runtime.operationErr = failure
	if _, err := service.Restart(ctx, "id"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	// 调用者更短的 deadline 不能被用例默认超时延长。
	shortCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, _ = service.Groups(shortCtx, "id")
	if runtime.deadlines["groups"] > time.Second {
		t.Fatal("caller deadline extended")
	}
}

func TestAdaptersUnavailableRuntime(t *testing.T) {
	s := NewAdapters(newRecordRepositoryFake(), nil, nil)
	if s.Available() {
		t.Fatal("nil runtime available")
	}
	ctx := context.Background()
	_, e1 := s.Start(ctx, "id")
	_, e2 := s.Stop(ctx, "id")
	_, e3 := s.Restart(ctx, "id")
	_, e4 := s.Test(ctx, "id")
	_, e5 := s.Status("id")
	_, e6 := s.Capabilities(ctx, "id")
	_, e7 := s.Send(ctx, "id", "group", "peer", "hi")
	_, e8 := s.Groups(ctx, "id")
	for _, err := range []error{e1, e2, e3, e4, e5, e6, e7, e8} {
		if !errors.Is(err, ErrAdapterRuntimeUnavailable) {
			t.Fatal(err)
		}
	}
}

func TestPublicAdapterRecordMalformedConfigDoesNotLeak(t *testing.T) {
	for _, detail := range []string{"{secret", `{"adapterType":"QQBot","config":"secret"}`, `{"adapterType":"QQBot","config":["secret"]}`} {
		result := publicAdapterRecord(Record{Detail: detail})
		if strings.Contains(result.Detail, "secret") || !strings.Contains(result.Detail, `"configError":true`) {
			t.Fatalf("invalid detail = %s", result.Detail)
		}
	}
}
