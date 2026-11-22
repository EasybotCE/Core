package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"easybot/internal/runtime"
	"easybot/internal/store"
)

func TestCommandDiagnostics(t *testing.T) {
	path := t.TempDir() + "/diagnose.db"
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createTestSession(t, db)
	handler := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime.NewLogBuffer(100))
	invoke := func(body string, auth bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/commands/diagnose", strings.NewReader(body))
		if auth {
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	if res := invoke(`{}`, false); res.Code != 401 {
		t.Fatal("unprotected diagnostic")
	}
	request := `{"message":"/help","context":{"userId":"u","role":"admin","verified":false}}`
	if res := invoke(request, true); res.Code != 200 || !strings.Contains(res.Body.String(), `"items":[]`) {
		t.Fatal("empty diagnostic", res.Body.String())
	}
	ctx := context.Background()
	for _, permission := range []string{"all", "admin", "owner"} {
		config := store.DefaultCommandConfig()
		config.Permission = permission
		raw, _ := json.Marshal(config)
		_, err := db.Save(ctx, store.Record{Kind: "commands", Name: permission, Detail: string(raw), Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	config, _ := json.Marshal(store.DefaultCommandConfig())
	for _, name := range []string{"disabled", "legacy"} {
		item, err := db.Save(ctx, store.Record{Kind: "commands", Name: name, Detail: string(config), Enabled: name != "disabled"})
		if err != nil {
			t.Fatal(err)
		}
		if name == "legacy" {
			// An existing unversioned configuration must be isolated as an error.
			item.Detail = "{}"
			// Create the legacy fixture via the same pre-contract DB fixture helper below.
			fixture, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = fixture.Exec(`UPDATE records SET detail=? WHERE id=?`, item.Detail, item.ID)
			fixture.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	before, _ := db.List(ctx, "commands")
	res := invoke(request, true)
	var report struct {
		Matched, Allowed, Invalid, Disabled int
		Conflict, Executed, Simulated       bool
		Items                               []struct{ ID, Error string }
	}
	if err := json.Unmarshal(res.Body.Bytes(), &report); err != nil || res.Code != 200 || report.Matched != 3 || report.Allowed != 1 || report.Invalid != 1 || report.Disabled != 1 || !report.Conflict || report.Executed || !report.Simulated {
		t.Fatal(res.Code, res.Body.String(), err)
	}
	for i := 1; i < len(report.Items); i++ {
		if report.Items[i-1].ID >= report.Items[i].ID {
			t.Fatal("unstable ordering")
		}
	}
	after, _ := db.List(ctx, "commands")
	a, _ := json.Marshal(before)
	b, _ := json.Marshal(after)
	if string(a) != string(b) {
		t.Fatal("diagnostic changed records")
	}
	if res := invoke(`{"message":"/unknown"}`, true); res.Code != 200 || !strings.Contains(res.Body.String(), `"conflict":false`) {
		t.Fatal("non-match conflict")
	}
	oversized, _ := json.Marshal(map[string]string{"message": strings.Repeat("x", 8193)})
	if res := invoke(string(oversized), true); res.Code != 400 {
		t.Fatal("oversized message accepted")
	}
}
