package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easybot/internal/adapter/onebot11"
	"easybot/internal/runtime"
	"easybot/internal/store"
)

func TestModuleLogsPersistDiagnosticContext(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "easybot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createTestSession(t, db)
	logs := runtime.NewLogs(10, nil)
	logs.SetSink(func(entry runtime.LogEntry) {
		if err := db.WriteLog(context.Background(), entry); err != nil {
			t.Fatal(err)
		}
	})
	logs.New("bridge").With("request_id", "review-request").Error("连接失败",
		"server", "survival", "error", errors.New("connection refused"), "password", "private password with spaces")
	// Query with an empty memory buffer to prove the context was persisted.
	handler := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime.NewLogs(10, nil))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/logs?q=connection%20refused&requestId=review-request&component=bridge", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var page struct {
		Items []runtime.LogEntry `json:"items"`
		Total int                `json:"total"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Items) != 1 || page.Total != 1 {
		t.Fatalf("persisted log query: %d %s", response.Code, response.Body.String())
	}
	if !strings.Contains(page.Items[0].Message, "survival") || strings.Contains(response.Body.String(), "private password") {
		t.Fatalf("missing context or credential leak: %s", response.Body.String())
	}
}

func createTestSession(t *testing.T, db *store.Store) {
	t.Helper()
	if _, err := db.CreateInitialAdmin(context.Background(), "admin", "internal-test-identity", sessionHash("test-session"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestRecordCRUDAndSession(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/easybot.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createTestSession(t, db)
	logs := runtime.NewLogs(20, nil)
	handler := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)), logs)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/records?kind=servers", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized, got %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/records", strings.NewReader(`{"kind":"servers","name":"survival","detail":"{}","enabled":true}`))
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create returned %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/records?kind=servers", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "survival") {
		t.Fatalf("list returned %d: %s", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v1/adapter/create", strings.NewReader(`{"adapterType":"Onebot11","displayName":"QQ Bridge","configJson":"{\"url\":\"ws://localhost:3001\"}"}`))
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `Onebot11`) {
		t.Fatalf("adapter create returned %d: %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/adapter/create", strings.NewReader(`{"adapterType":"QQBot","displayName":"QQ Official","configJson":"{\"AppID\":\"app\",\"AppSecret\":\"secret\"}"}`))
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `QQBot`) || strings.Contains(response.Body.String(), `secret`) {
		t.Fatalf("QQ adapter create returned %d: %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/adapter/create", strings.NewReader(`{"adapterType":"Telegram","displayName":"unsupported","configJson":"{}"}`))
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("unsupported adapter returned %d: %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/logs?limit=10", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "POST /api/v1/records") {
		t.Fatalf("logs returned %d: %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPut, "/api/v1/logs/settings", strings.NewReader(`{"persistent":false,"retentionDays":45,"maxRows":2000}`))
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"retentionDays":45`) {
		t.Fatalf("log settings update returned %d: %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/logs/status", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"persistent":false`) {
		t.Fatalf("log status returned %d: %s", response.Code, response.Body.String())
	}
}

func TestAdapterCredentialsAreRedactedAndPreservedOnUpdate(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/easybot.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createTestSession(t, db)
	handler := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime.NewLogs(20, nil))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/adapter/create", strings.NewReader(`{"adapterType":"Onebot11","displayName":"Mock","configJson":"{\"url\":\"ws://127.0.0.1:3001/\",\"accessToken\":\"never-return-this\"}"}`))
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || strings.Contains(response.Body.String(), "never-return-this") {
		t.Fatalf("create leaked secret or failed: %d %s", response.Code, response.Body.String())
	}
	var created store.Record
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/adapter/list", "/api/v1/records?kind=adapters"} {
		request = httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "never-return-this") {
			t.Fatalf("%s leaked secret or failed: %d %s", path, response.Code, response.Body.String())
		}
	}
	request = httptest.NewRequest(http.MethodPut, "/api/v1/adapter/"+created.ID, strings.NewReader(`{"displayName":"Mock updated","configJson":"{\"url\":\"ws://127.0.0.1:3002/\",\"accessTokenConfigured\":true}"}`))
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "never-return-this") {
		t.Fatalf("update failed or leaked secret: %d %s", response.Code, response.Body.String())
	}
	items, err := db.List(context.Background(), "adapters")
	if err != nil || len(items) != 1 {
		t.Fatalf("adapter record missing: %v", err)
	}
	var detail struct {
		Config json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal([]byte(items[0].Detail), &detail); err != nil {
		t.Fatal(err)
	}
	config, err := onebot11.ParseConfig(detail.Config)
	if err != nil || config.AccessToken != "never-return-this" || config.WsURL != "ws://127.0.0.1:3002/" {
		t.Fatalf("update failed to preserve the token: %#v %v", config, err)
	}
}

func TestAdapterAPIReportsStoppedAndRejectsUnknownID(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/easybot.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createTestSession(t, db)
	manager := onebot11.NewManager(context.Background(), db, nil)
	defer manager.Shutdown(context.Background())
	handler := NewWithAdapterManager(db, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime.NewLogs(20, nil), manager)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/adapter/missing/status", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing adapter status returned %d: %s", response.Code, response.Body.String())
	}
}

func TestPasswordLoginAndSessionAuthorization(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/easybot.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	admin, err := db.EnsureConfigAdmin(context.Background(), "123456789012")
	if err != nil {
		t.Fatal(err)
	}
	makeHandler := func() http.Handler {
		return NewForListener(db, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime.NewLogs(20, nil), nil, "0.0.0.0:5001", "123456789012", admin.ID)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"password":"wrong"}`))
	response := httptest.NewRecorder()
	makeHandler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password returned %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"password":"123456789012"}`))
	response = httptest.NewRecorder()
	makeHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login returned %d: %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}
	request = httptest.NewRequest(http.MethodGet, "/api/v1/records", nil)
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	makeHandler().ServeHTTP(response, request)
	if response.Code == http.StatusUnauthorized {
		t.Fatal("valid session was rejected")
	}
}

func TestLocalListenerAuthenticationBoundary(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/easybot.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, test := range []struct {
		name, listener, remote, host, origin string
		allowed                              bool
	}{
		{name: "IPv4", listener: "127.0.0.1:5001", remote: "127.0.0.1:1234", host: "127.0.0.1:5001", allowed: true},
		{name: "IPv4 loopback range", listener: "127.0.0.2:5001", remote: "127.0.0.1:1234", host: "127.0.0.2:5001", allowed: true},
		{name: "IPv6", listener: "[::1]:5001", remote: "[::1]:1234", host: "[::1]:5001", allowed: true},
		{name: "localhost", listener: "localhost:5001", remote: "127.0.0.1:1234", host: "localhost:5001", allowed: true},
		{name: "external listener still requires login", listener: "0.0.0.0:5001", remote: "127.0.0.1:1234", host: "127.0.0.1:5001"},
		{name: "forged local host", listener: "127.0.0.1:5001", remote: "192.0.2.1:1234", host: "localhost:5001"},
		{name: "public host", listener: "127.0.0.1:5001", remote: "127.0.0.1:1234", host: "evil.example:5001"},
		{name: "public origin", listener: "127.0.0.1:5001", remote: "127.0.0.1:1234", host: "localhost:5001", origin: "https://evil.example"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := NewForListener(db, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime.NewLogs(20, nil), nil, test.listener, "", "")
			for _, path := range []string{"/api/v1/records?kind=servers", "/api/v1/auth/session"} {
				request := httptest.NewRequest(http.MethodGet, path, nil)
				request.RemoteAddr, request.Host = test.remote, test.host
				if test.origin != "" {
					request.Header.Set("Origin", test.origin)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if path == "/api/v1/auth/session" {
					var session struct{ Authenticated bool }
					if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil || session.Authenticated != test.allowed {
						t.Fatalf("session: %s, error %v", response.Body.String(), err)
					}
				} else if (response.Code == http.StatusOK) != test.allowed {
					t.Fatalf("management request returned %d", response.Code)
				}
			}
		})
	}
}

func TestLoginRateLimit(t *testing.T) {
	s := &server{loginAttempts: make(map[string]loginAttempt)}
	for i := 0; i < 10; i++ {
		if !s.allowLogin("127.0.0.1:1234") {
			t.Fatalf("attempt %d was unexpectedly rejected", i+1)
		}
	}
	if s.allowLogin("127.0.0.1:1234") {
		t.Fatal("11th attempt should be rate limited")
	}
}

func TestAPIVersionPrefix(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "easybot.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	handler := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime.NewLogs(10, nil))

	for _, test := range []struct {
		path string
		want int
	}{
		{path: "/api/v1/health", want: http.StatusOK},
		{path: "/api/health", want: http.StatusNotFound},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != test.want {
			t.Errorf("GET %s returned %d, want %d", test.path, response.Code, test.want)
		}
	}
}
