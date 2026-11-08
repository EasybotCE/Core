// SPDX-License-Identifier: AGPL-3.0-only
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EasybotCE/easybotce/internal/config"
	"github.com/EasybotCE/easybotce/internal/protocol"
	"github.com/EasybotCE/easybotce/internal/session"
	"github.com/EasybotCE/easybotce/internal/store"
	"github.com/coder/websocket"
)

type runtimeFixture struct {
	server   *httptest.Server
	sessions *session.Manager
	db       *store.Store
	logs     *bytes.Buffer
}

func runtimeForTest(t *testing.T) *runtimeFixture {
	t.Helper()
	db, err := store.Open(context.Background(), "sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	manager := session.New(db)
	logs := new(bytes.Buffer)
	cfg := config.Config{ListenAddr: "127.0.0.1:8080", AdminToken: testToken, ShutdownTimeout: time.Second}
	handler, err := NewRuntimeHandler(cfg, slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})), db, manager)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(func() { manager.Close(); server.Close(); db.Close() })
	return &runtimeFixture{server, manager, db, logs}
}
func (f *runtimeFixture) request(t *testing.T, method, path, body string, want int) []byte {
	t.Helper()
	r, err := http.NewRequest(method, f.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+testToken)
	r.Header.Set("Content-Type", "application/json")
	response, err := f.server.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("%s %s: status %d want %d: %s", method, path, response.StatusCode, want, data)
	}
	return data
}
func (f *runtimeFixture) provision(t *testing.T, id string) store.IssuedCredential {
	t.Helper()
	f.request(t, "POST", "/v1/endpoints", `{"id":"`+id+`","type":"mc-server","name":"大厅","enabled":true}`, 201)
	data := f.request(t, "POST", "/v1/endpoints/"+id+"/credentials", `{"capabilities":["chat.receive","command.execute"]}`, 201)
	var c store.IssuedCredential
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	return c
}
func (f *runtimeFixture) dial(t *testing.T, token string) (*websocket.Conn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.server.URL, "http")+"/v1/gateway", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}}})
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("dial: %v, status %d", err, status)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn, ctx
}
func hello(t *testing.T, ctx context.Context, conn *websocket.Conn, id string, versions []string) protocol.Message {
	t.Helper()
	payload, _ := json.Marshal(protocol.Hello{EndpointID: id, EndpointType: "mc-server", Versions: versions, Capabilities: []string{"chat.receive", "command.execute", "admin.root"}})
	data, _ := json.Marshal(protocol.Message{Version: protocol.Version, ID: "hello-1", Type: protocol.HelloType, Payload: payload})
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	message, err := protocol.Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return message
}
func TestRuntimeCRUD(t *testing.T) {
	f := runtimeForTest(t)
	c := f.provision(t, "lobby")
	data := f.request(t, "GET", "/v1/endpoints/lobby/credentials", "", 200)
	if bytes.Contains(data, []byte(c.Token)) || bytes.Contains(data, []byte("token_hash")) {
		t.Fatal("credential disclosure")
	}
	f.request(t, "GET", "/v1/system/readiness", "", 200)
	f.request(t, "GET", "/v1/endpoints?limit=1", "", 200)
	f.request(t, "GET", "/v1/endpoints?limit=1000", "", 400)
	f.request(t, "GET", "/v1/endpoints/lobby", "", 200)
	f.request(t, "PUT", "/v1/endpoints/lobby", `{"name":"renamed","enabled":false}`, 200)
	f.request(t, "PUT", "/v1/endpoints/lobby", `{"name":"renamed","enabled":true}`, 200)
	for _, body := range []string{`{"name":"x","Name":"y","enabled":true}`, `{"name":"x","name":"y","enabled":true}`, `{"name":"x"}`, `{"name":null,"enabled":true}`, `[]`, `{"name":"x","enabled":true} {}`} {
		f.request(t, "PUT", "/v1/endpoints/lobby", body, 400)
	}
	f.request(t, "POST", "/v1/endpoints/lobby/credentials", `{"capabilities":null}`, 400)
	f.request(t, "POST", "/v1/endpoints/lobby/credentials", `{"capabilities":["x","x"]}`, 400)
	f.request(t, "DELETE", "/v1/endpoints/lobby/credentials/"+c.ID, "", 204)
	f.request(t, "DELETE", "/v1/endpoints/lobby/credentials/"+c.ID, "", 404)
	f.request(t, "DELETE", "/v1/endpoints/lobby", "", 204)
	f.request(t, "GET", "/v1/endpoints/lobby", "", 404)
}
func TestWebSocketAuthenticationBoundary(t *testing.T) {
	f := runtimeForTest(t)
	c := f.provision(t, "lobby")
	for _, token := range []string{"", testToken, "wrong", c.Token + "x"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(f.server.URL, "http")+"/v1/gateway", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}}})
		cancel()
		if err == nil {
			conn.CloseNow()
			t.Fatal("invalid credential upgraded")
		}
		if response == nil || response.StatusCode != 401 {
			t.Fatalf("want 401: %v %v", response, err)
		}
	}
	conn, ctx := f.dial(t, c.Token)
	msg := hello(t, ctx, conn, "another-server", []string{protocol.Version})
	if msg.Type != protocol.ErrorType {
		t.Fatal("identity impersonation accepted")
	}
	var failure protocol.Failure
	json.Unmarshal(msg.Payload, &failure)
	if failure.Code != "permission_denied" {
		t.Fatal("wrong failure", failure)
	}
	conn.CloseNow()
	conn, ctx = f.dial(t, c.Token)
	msg = hello(t, ctx, conn, "lobby", []string{"9.0.0"})
	json.Unmarshal(msg.Payload, &failure)
	if msg.Type != protocol.ErrorType || failure.Code != "unsupported_version" {
		t.Fatal("bad version accepted")
	}
}
func TestWebSocketWelcomeDuplicateRevokeAndReconnect(t *testing.T) {
	f := runtimeForTest(t)
	c := f.provision(t, "lobby")
	conn, ctx := f.dial(t, c.Token)
	msg := hello(t, ctx, conn, "lobby", []string{protocol.Version})
	if msg.Type != protocol.WelcomeType || msg.ReplyTo != "hello-1" {
		t.Fatal("no welcome")
	}
	var welcome protocol.Welcome
	if err := json.Unmarshal(msg.Payload, &welcome); err != nil {
		t.Fatal(err)
	}
	if len(welcome.Capabilities) != 0 || welcome.HeartbeatIntervalMS != 20000 {
		t.Fatal("unimplemented or unauthorized capabilities granted")
	}
	f.request(t, "GET", "/v1/sessions", "", 200)
	duplicate, dupCtx := f.dial(t, c.Token)
	if msg := hello(t, dupCtx, duplicate, "lobby", []string{protocol.Version}); msg.Type != protocol.ErrorType {
		t.Fatal("duplicate session accepted")
	}
	duplicate.CloseNow()
	f.request(t, "DELETE", "/v1/endpoints/lobby/credentials/"+c.ID, "", 204)
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("revoked connection stayed open")
	}
	if sessions := f.sessions.List(); len(sessions) != 0 {
		t.Fatal("revoked session retained", sessions)
	}
	data := f.request(t, "POST", "/v1/endpoints/lobby/credentials", `{"capabilities":[]}`, 201)
	var next store.IssuedCredential
	json.Unmarshal(data, &next)
	conn, ctx = f.dial(t, next.Token)
	if msg := hello(t, ctx, conn, "lobby", []string{protocol.Version}); msg.Type != protocol.WelcomeType {
		t.Fatal("reconnect failed")
	}
	f.request(t, "PUT", "/v1/endpoints/lobby", `{"name":"lobby","enabled":false}`, 200)
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("disabled connection stayed open")
	}
	// Reading logs while handlers finish would race; server.Close waits for them.
	f.sessions.Close()
	f.server.Close()
	if strings.Contains(f.logs.String(), c.Token) || strings.Contains(f.logs.String(), next.Token) || strings.Contains(f.logs.String(), testToken) {
		t.Fatal("secret in logs")
	}
}
func TestWebSocketRevocationDuringHandshake(t *testing.T) {
	f := runtimeForTest(t)
	c := f.provision(t, "lobby")
	conn, ctx := f.dial(t, c.Token)
	// HTTP authentication has completed but hello has not. Final admission must
	// observe revocation rather than relying on the earlier upgrade check.
	f.request(t, "DELETE", "/v1/endpoints/lobby/credentials/"+c.ID, "", 204)
	if msg := hello(t, ctx, conn, "lobby", []string{protocol.Version}); msg.Type != protocol.ErrorType {
		t.Fatal("revocation raced with admission")
	}
}
func TestWebSocketShutdown(t *testing.T) {
	f := runtimeForTest(t)
	c := f.provision(t, "lobby")
	conn, ctx := f.dial(t, c.Token)
	if msg := hello(t, ctx, conn, "lobby", []string{protocol.Version}); msg.Type != protocol.WelcomeType {
		t.Fatal("handshake failed")
	}
	done := make(chan struct{})
	go func() { f.sessions.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("manager shutdown blocked")
	}
	if _, _, err := conn.Read(ctx); err == nil {
		t.Fatal("connection survived shutdown")
	}
}
