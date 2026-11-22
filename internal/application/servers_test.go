package application

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"easybot/internal/bridge"
)

type bridgeRuntimeFake struct {
	statuses           map[string]bridge.Status
	status             bridge.Status
	statusErr, sendErr error
	disconnected       []string
	sends              []string
}

func (f *bridgeRuntimeFake) Statuses() (map[string]bridge.Status, error) {
	return f.statuses, f.statusErr
}
func (f *bridgeRuntimeFake) Status(string) (bridge.Status, error) { return f.status, f.statusErr }
func (f *bridgeRuntimeFake) SendToChat(id, text string) error {
	f.sends = append(f.sends, id+":"+text)
	return f.sendErr
}
func (f *bridgeRuntimeFake) DisconnectServer(id string) { f.disconnected = append(f.disconnected, id) }

func TestServersStatusesAndUnavailable(t *testing.T) {
	service := NewServers(newRecordRepositoryFake(), nil, nil)
	if service.Available() {
		t.Fatal("nil bridge runtime available")
	}
	if _, err := service.Statuses(); !errors.Is(err, ErrBridgeRuntimeUnavailable) {
		t.Fatal(err)
	}
	if _, err := service.Status("id"); !errors.Is(err, ErrBridgeRuntimeUnavailable) {
		t.Fatal(err)
	}
	if err := service.Send("id", "hi"); !errors.Is(err, ErrBridgeRuntimeUnavailable) {
		t.Fatal(err)
	}

	runtime := &bridgeRuntimeFake{statuses: map[string]bridge.Status{
		"b": {ServerID: "b", ServerName: "Zulu"},
		"a": {ServerID: "a", ServerName: "Alpha"},
	}}
	service = NewServers(newRecordRepositoryFake(), runtime, nil)
	statuses, err := service.Statuses()
	if err != nil || len(statuses) != 2 || statuses[0].ServerName != "Alpha" || statuses[1].ServerName != "Zulu" {
		t.Fatalf("statuses = %#v %v", statuses, err)
	}
	runtime.statuses = nil
	runtime.statusErr = errors.New("runtime unavailable")
	if _, err := service.Statuses(); !errors.Is(err, runtime.statusErr) {
		t.Fatal(err)
	}
	if _, err := service.Status("id"); !errors.Is(err, runtime.statusErr) {
		t.Fatal(err)
	}
}

func TestServersSendValidationAndErrors(t *testing.T) {
	runtime := &bridgeRuntimeFake{}
	service := NewServers(newRecordRepositoryFake(), runtime, nil)
	for _, text := range []string{"", "  \n", strings.Repeat("x", 2001)} {
		requireValidation(t, service.Send("server", text), "text must be 1-2000 bytes")
	}
	if len(runtime.sends) != 0 {
		t.Fatal("invalid bridge text sent")
	}
	if err := service.Send("server", "  hello  "); err != nil {
		t.Fatal(err)
	}
	if got := runtime.sends[0]; got != "server:hello" {
		t.Fatalf("send = %q", got)
	}
	runtime.sendErr = errors.New("offline")
	if err := service.Send("server", "hello"); !errors.Is(err, runtime.sendErr) {
		t.Fatal(err)
	}
}

func TestServerTokenCreateAndUpdate(t *testing.T) {
	repo := newRecordRepositoryFake()
	service := NewRecords(repo)
	ctx := context.Background()
	for _, detail := range []string{"", "{}", "null", `{"port":25565}`, `{"Token":"already"}`, `{"serverToken":"compat"}`} {
		item, err := service.Create(ctx, Record{Kind: "servers", Name: "MC", Detail: detail})
		if err != nil {
			t.Fatal(err)
		}
		token := serverToken(item.Detail)
		if token == "" {
			t.Fatalf("token missing for %q", detail)
		}
		if (token == "already" || token == "compat") && item.Detail != detail {
			t.Fatalf("compatible token changed: %s", item.Detail)
		}
		if token != "already" && token != "compat" && len(token) != 48 {
			t.Fatalf("generated token length = %d", len(token))
		}
		if _, err := service.Update(ctx, Record{ID: item.ID, Kind: "servers", Name: "MC2", Detail: `{"motd":"hello"}`}); err != nil {
			t.Fatal(err)
		}
		if got := serverToken(repo.items[item.ID].Detail); got != token {
			t.Fatalf("token changed on update: %q != %q", got, token)
		}
	}
	for _, detail := range []string{"{", "[]", `"scalar"`} {
		_, err := service.Create(ctx, Record{Kind: "servers", Name: "invalid", Detail: detail})
		requireValidation(t, err, "server config must be valid JSON")
	}
}

func TestServerTokenRotationPersistsBeforeDisconnect(t *testing.T) {
	repo := newRecordRepositoryFake()
	original := Record{ID: "s1", Kind: "servers", Name: "production", Detail: `{"port":25565,"token":"old"}`, Enabled: true}
	repo.items[original.ID] = original
	runtime := &bridgeRuntimeFake{}
	var output strings.Builder
	service := NewServers(repo, runtime, slog.New(slog.NewTextHandler(&output, nil)))
	result, err := service.RotateToken(context.Background(), original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.ServerID != original.ID || len(result.Token) != 48 || result.Token == "old" {
		t.Fatalf("token result = %#v", result)
	}
	saved := repo.items[original.ID]
	if serverToken(saved.Detail) != result.Token || !strings.Contains(saved.Detail, `"port":25565`) || !saved.Enabled {
		t.Fatalf("saved record = %#v", saved)
	}
	if len(runtime.disconnected) != 1 || runtime.disconnected[0] != original.ID {
		t.Fatalf("disconnects = %v", runtime.disconnected)
	}
	if !strings.Contains(output.String(), "production") {
		t.Fatal("rotation audit log missing")
	}
}

func TestServerTokenRotationFailureDoesNotDisconnect(t *testing.T) {
	for _, tc := range []struct {
		name    string
		saveErr error
		detail  string
		want    error
	}{
		{name: "missing", want: sql.ErrNoRows},
		{name: "save", saveErr: errors.New("disk failure"), detail: `{"token":"old"}`},
		{name: "malformed", detail: "{", want: &ValidationError{Message: "stored server config is invalid JSON"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRecordRepositoryFake()
			if tc.name != "missing" {
				repo.items["id"] = Record{ID: "id", Kind: "servers", Name: "server", Detail: tc.detail}
			}
			repo.saveErr = tc.saveErr
			runtime := &bridgeRuntimeFake{}
			result, err := NewServers(repo, runtime, nil).RotateToken(context.Background(), "id")
			want := tc.want
			if tc.saveErr != nil {
				want = tc.saveErr
			}
			if result.ServerID != "" || (want != nil && !errors.Is(err, want)) || (want == nil && err == nil) {
				t.Fatalf("rotate = %#v %v, want %v", result, err, want)
			}
			if len(runtime.disconnected) != 0 {
				t.Fatalf("disconnected after failure: %v", runtime.disconnected)
			}
		})
	}
}

func TestSetServerTokenHandlesNullJSON(t *testing.T) {
	detail, err := setServerToken("null", "token")
	if err != nil || serverToken(detail) != "token" {
		t.Fatalf("set token = %q, %v", detail, err)
	}
}
