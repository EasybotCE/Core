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

func TestUnauthorizedRequestsAreWarnLogged(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "EasybotCE.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	logs := runtime.NewLogs(10, slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})))
	handler := New(db, slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})), logs)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/adapter/list", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	entries := logs.Recent(1)
	if len(entries) != 1 || entries[0].Level != "warn" || entries[0].Source != "api" {
		t.Fatalf("unauthorized request log = %#v, want api warn entry", entries)
	}
}

func TestRequestLogLevelsFollowHTTPStatus(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{http.StatusCreated, "debug"}, {http.StatusBadRequest, "warn"},
		{http.StatusUnauthorized, "warn"}, {http.StatusBadGateway, "error"}, {http.StatusInternalServerError, "error"},
	}
	for _, test := range tests {
		logs := runtime.NewLogs(10, nil)
		server := &server{logs: logs}
		handler := server.observe(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(test.status) }))
		request := httptest.NewRequest(http.MethodPost, "/api/v1/test", nil)
		handler.ServeHTTP(httptest.NewRecorder(), request)
		entries := logs.Recent(1)
		if len(entries) != 1 || entries[0].Level != test.want {
			t.Errorf("status %d log = %#v, want %s", test.status, entries, test.want)
		}
	}
}

func TestModuleLogsPersistDiagnosticContext(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "EasybotCE.db"))
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

func TestBindingAPIChallengeConfirmAuditAndUnbind(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "bindings-api.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createTestSession(t, db)
	handler := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime.NewLogs(10, nil))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/bindings/challenges", strings.NewReader(`{"playerUuid":"player-api","groupId":"group-1"}`))
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var challenge struct {
		Code string `json:"code"`
	}
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &challenge) != nil || challenge.Code == "" {
		t.Fatalf("challenge response %d: %s", response.Code, response.Body.String())
	}
	confirm := func(group string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"code": challenge.Code, "platformType": "onebot11", "accountId": "account-1", "displayName": "Test", "groupId": group})
		r := httptest.NewRequest(http.MethodPost, "/api/v1/bindings/confirm", strings.NewReader(string(body)))
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if got := confirm("different-group"); got.Code != http.StatusCreated {
		t.Fatalf("confirmation status %d: %s", got.Code, got.Body.String())
	}
	list := httptest.NewRequest(http.MethodGet, "/api/v1/bindings", nil)
	list.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, list)
	var items []store.Binding
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &items) != nil || len(items) != 1 {
		t.Fatalf("binding list %d: %s", listed.Code, listed.Body.String())
	}
	remove := httptest.NewRequest(http.MethodDelete, "/api/v1/bindings/"+items[0].ID, nil)
	remove.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	removed := httptest.NewRecorder()
	handler.ServeHTTP(removed, remove)
	if removed.Code != http.StatusNoContent {
		t.Fatalf("unbind status %d: %s", removed.Code, removed.Body.String())
	}
	if audit, err := db.BindingAudit(context.Background(), 10); err != nil || len(audit) != 2 {
		t.Fatalf("audit entries %#v, %v", audit, err)
	}
}

func createTestSession(t *testing.T, db *store.Store) {
	t.Helper()
	if _, err := db.CreateInitialAdmin(context.Background(), "admin", "internal-test-identity", sessionHash("test-session"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestGroupsAreNotManagedAsManualRecords(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "EasybotCE.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createTestSession(t, db)
	handler := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime.NewLogs(10, nil))

	requests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/records?kind=groups", ""},
		{http.MethodPost, "/api/v1/records", `{"kind":"groups","name":"manual group"}`},
		{http.MethodPut, "/api/v1/records/groups/group-id", `{"name":"manual group"}`},
		{http.MethodDelete, "/api/v1/records/groups/group-id", ""},
	}
	for _, test := range requests {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
			}
		})
	}
	groups, err := db.List(context.Background(), "groups")
	if err != nil || len(groups) != 0 {
		t.Fatalf("manual group records persisted: %#v, err=%v", groups, err)
	}
}

func TestRecordCRUDAndSession(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/EasybotCE.db")
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
	request = httptest.NewRequest(http.MethodPut, "/api/v1/logs/settings", strings.NewReader(`{"level":"debug","persistent":false,"retentionDays":45,"maxRows":2000}`))
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
	db, err := store.Open(t.TempDir() + "/EasybotCE.db")
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
	db, err := store.Open(t.TempDir() + "/EasybotCE.db")
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
	db, err := store.Open(t.TempDir() + "/EasybotCE.db")
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
	request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"token":"123456789012"}`))
	response = httptest.NewRecorder()
	makeHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("token credential login returned %d: %s", response.Code, response.Body.String())
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

func TestRecentLogTotalIncludesMemoryOnlyEntries(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "EasybotCE.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createTestSession(t, db)
	logs := runtime.NewLogs(20, nil)
	logs.New("adapter").Info("内存中的连接状态")
	handler := New(db, slog.New(slog.NewTextHandler(io.Discard, nil)), logs)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/logs?limit=10", nil)
	request.AddCookie(&http.Cookie{Name: sessionCookie, Value: "test-session"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var page struct {
		Items []runtime.LogEntry `json:"items"`
		Total int                `json:"total"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusOK || len(page.Items) != 1 || page.Total != 1 {
		t.Fatalf("memory log page has incorrect count: %d %s", response.Code, response.Body.String())
	}
}
func TestLoginFailuresResetAfterSuccessfulAuthentication(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "EasybotCE.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	admin, err := db.EnsureConfigAdmin(context.Background(), "correct-password")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewForListener(db, slog.New(slog.NewTextHandler(io.Discard, nil)), runtime.NewLogs(20, nil), nil, "0.0.0.0:5001", "correct-password", admin.ID)
	login := func(password string) int {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"password":"`+password+`"}`))
		request.RemoteAddr = "192.0.2.10:4321"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code
	}
	for i := 0; i < 9; i++ {
		if status := login("wrong-password"); status != http.StatusUnauthorized {
			t.Fatalf("failed attempt %d returned %d", i+1, status)
		}
	}
	if status := login("correct-password"); status != http.StatusOK {
		t.Fatalf("valid password after failures returned %d", status)
	}
	if status := login("wrong-password"); status != http.StatusUnauthorized {
		t.Fatalf("failed attempt after successful login returned %d; want 401", status)
	}
}
func TestEveryListenerRequiresAuthentication(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/EasybotCE.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, test := range []struct {
		name, listener, remote, host, origin string
	}{
		{name: "loopback", listener: "127.0.0.1:5001", remote: "127.0.0.1:1234", host: "127.0.0.1:5001"},
		{name: "external listener", listener: "0.0.0.0:5001", remote: "192.0.2.1:1234", host: "192.0.2.1:5001"},
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
					if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil || session.Authenticated {
						t.Fatalf("session: %s, error %v", response.Body.String(), err)
					}
				} else if response.Code != http.StatusUnauthorized {
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
	db, err := store.Open(filepath.Join(t.TempDir(), "EasybotCE.db"))
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

func TestWriteAdapterPermissionErrorUsesForbidden(t *testing.T) {
	response := httptest.NewRecorder()
	writeAdapterError(response, &onebot11.APIError{Code: "adapter_permission_denied", Message: "permission denied"})
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusForbidden, response.Body.String())
	}
}
