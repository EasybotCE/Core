package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easybot/internal/command"
	"easybot/internal/runtime"
	"easybot/internal/store"
)

func TestCommandSimulationHTTP(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/commands.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createTestSession(t, db)
	logs := runtime.NewLogBuffer(100)
	handler := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)), logs)
	invoke := func(path string, body any, auth bool) *httptest.ResponseRecorder {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("POST", path, strings.NewReader(string(raw)))
		if auth {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	c := store.DefaultCommandConfig()
	c.Permission = "admin"
	c.Reply = "private preview {sender_name}"
	raw, _ := json.Marshal(c)
	item := store.Record{Kind: "commands", Name: "test", Detail: string(raw), Enabled: true}
	if res := invoke("/api/commands/simulate", map[string]any{}, false); res.Code != 401 {
		t.Fatal("unprotected simulation", res.Code)
	}
	res := invoke("/api/records", item, true)
	if res.Code != 201 {
		t.Fatal(res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	request := map[string]any{"id": item.ID, "message": "/help", "context": map[string]any{"userId": "u", "senderName": "secret-body", "verified": false, "role": "admin"}}
	res = invoke("/api/commands/simulate", request, true)
	var result command.Result
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil || res.Code != 200 || !result.Matched || result.Allowed || result.Executed || result.Reply != "" {
		t.Fatal(res.Code, result, err)
	}
	request["context"] = map[string]any{"userId": "u", "senderName": "secret-body", "verified": true, "role": "owner"}
	res = invoke("/api/commands/simulate", request, true)
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil || res.Code != 200 || !result.Allowed || result.Executed || !result.Simulated || result.RuntimeAvailable || result.Reply != "private preview secret-body" {
		t.Fatal(res.Code, result, err)
	}
	request["detail"] = string(raw)
	if res := invoke("/api/commands/simulate", request, true); res.Code != 400 {
		t.Fatal("ID and draft override accepted")
	}
	delete(request, "id")
	request["enabled"] = false
	res = invoke("/api/commands/simulate", request, true)
	if !strings.Contains(res.Body.String(), `"reason":"disabled"`) {
		t.Fatal(res.Body.String())
	}
	delete(request, "enabled")
	if res := invoke("/api/commands/simulate", request, true); res.Code != 400 {
		t.Fatal("missing enabled accepted")
	}
	if res := invoke("/api/commands/simulate", map[string]any{"id": "missing"}, true); res.Code != 404 {
		t.Fatal("missing command did not return 404", res.Code)
	}
	request["enabled"] = true
	request["detail"] = `{"schemaVersion":1,"action":"shell"}`
	if res := invoke("/api/commands/simulate", request, true); res.Code != 400 {
		t.Fatal("unsupported action accepted")
	}
	if res := invoke("/api/commands/validate", item, true); res.Code != 200 {
		t.Fatal("command preflight failed", res.Body.String())
	}
	stored, err := db.Get(context.Background(), "commands", item.ID)
	if err != nil || stored.Detail != item.Detail || stored.Enabled != item.Enabled || !stored.UpdatedAt.Equal(item.UpdatedAt) {
		t.Fatal("simulation mutated persisted configuration", stored, err)
	}
	items, err := db.List(context.Background(), "commands")
	if err != nil || len(items) != 1 {
		t.Fatal("draft simulation persisted a record")
	}
	entries, _ := json.Marshal(logs.Recent(100))
	if strings.Contains(string(entries), "secret-body") || strings.Contains(string(entries), "private preview") {
		t.Fatal("simulation leaked message/reply into logs")
	}
}
