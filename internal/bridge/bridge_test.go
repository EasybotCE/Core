package bridge

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"easybot/internal/store"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const testToken = "secret-server-token"

type captureSocial struct {
	mu    sync.Mutex
	calls []sentMessage
}

type sentMessage struct {
	adapterID string
	message   string
	peerID    string
	text      string
}

func (c *captureSocial) Send(_ context.Context, adapterID, messageType, peerID, message string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, sentMessage{adapterID: adapterID, message: messageType, peerID: peerID, text: message})
	return "message-id", nil
}

func (c *captureSocial) last() (sentMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.calls) == 0 {
		return sentMessage{}, false
	}
	return c.calls[len(c.calls)-1], true
}

func newTestStack(t *testing.T) (*store.Store, *Manager, *Router, *captureSocial, string, string) {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/EasybotCE.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	server, err := db.Save(ctx, store.Record{Kind: "servers", Name: "生存服", Detail: `{"token":"` + testToken + `","port":25565}`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := db.Save(ctx, store.Record{
		Kind:    "sync-rules",
		Name:    "官方群 <-> 生存服",
		Detail:  `{"serverId":"` + server.ID + `","adapterId":"adapter-1","groupId":"999","direction":"both"}`,
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	social := &captureSocial{}
	manager := NewManager(db, logger, Callbacks{})
	router := NewRouter(db, manager, social, logger)
	manager.call.OnChat = router.OnMinecraftChat
	return db, manager, router, social, server.ID, rule.ID
}

// dialPlugin connects a simulated original plugin and completes the handshake.
func dialPlugin(t *testing.T, manager *Manager, token string) *websocket.Conn {
	t.Helper()
	server := httptest.NewServer(manager)
	t.Cleanup(server.Close)
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/bridge"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	var hello map[string]any
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatalf("reading hello: %v", err)
	}
	if op, _ := hello["op"].(float64); OpCode(op) != OpHello {
		t.Fatalf("first packet must be Hello, got %#v", hello)
	}
	for _, field := range []string{"version", "session_id", "token", "interval"} {
		if _, ok := hello[field]; !ok {
			t.Fatalf("hello is missing %q: %#v", field, hello)
		}
	}
	servers, err := manager.db.List(context.Background(), "servers")
	if err != nil || len(servers) == 0 {
		t.Fatalf("loading test server: %v", err)
	}
	connectionID, _ := hello["session_id"].(string)
	nonce, _ := hello["token"].(string)
	serverID := servers[0].ID
	timestamp := time.Now().Unix()
	if err := conn.WriteJSON(map[string]any{
		"op": OpIdentify, "protocol_version": ProtocolVersion, "connection_id": connectionID,
		"server_id": serverID, "timestamp": timestamp,
		"proof": Proof(token, connectionID, nonce, serverID, timestamp),
		"token": token, "plugin_version": "2.3.0", "server_description": "Paper 1.21",
	}); err != nil {
		t.Fatal(err)
	}
	var success map[string]any
	if err := conn.ReadJSON(&success); err != nil {
		t.Fatalf("reading identify success: %v", err)
	}
	if op, _ := success["op"].(float64); OpCode(op) != OpIdentifySuccess {
		t.Fatalf("expected IdentifySuccess, got %#v", success)
	}
	if success["server_name"] != "生存服" {
		t.Fatalf("unexpected server_name: %#v", success)
	}
	return conn
}

func TestPluginHandshakeAndChatForwardingToSocial(t *testing.T) {
	_, manager, _, social, serverID, _ := newTestStack(t)
	conn := dialPlugin(t, manager, testToken)

	status, err := manager.Status(serverID)
	if err != nil || status.State != "online" || status.PluginVersion != "2.3.0" {
		t.Fatalf("unexpected status: %#v error %v", status, err)
	}

	packet := map[string]any{"op": OpPacket, "callback_id": "cb-1", "exec_op": ExecSyncMessage,
		"player": map[string]any{"player_name": "Steve", "player_uuid": "uuid-1"}, "message": "大家好", "use_command": false}
	if err := conn.WriteJSON(envelopePacketForTest(manager, serverID, 1, packet)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if call, ok := social.last(); ok {
			if call.adapterID != "adapter-1" || call.peerID != "999" || call.message != "group" {
				t.Fatalf("wrong routing target: %#v", call)
			}
			if call.text != "[生存服] Steve: 大家好" {
				t.Fatalf("unexpected formatted text: %q", call.text)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("minecraft chat was never forwarded to the social adapter")
}

func TestMinecraftCommandsAreNotForwardedToSocial(t *testing.T) {
	_, manager, _, social, serverID, _ := newTestStack(t)
	_ = dialPlugin(t, manager, testToken)
	manager.call.OnChat(serverID, SyncMessagePacket{Message: "/op Alice", UseCommand: true, Player: PlayerInfo{PlayerName: "Steve"}})
	if _, ok := social.last(); ok {
		t.Fatal("plugin command leaked to the linked social group")
	}
}

func TestSocialEventReplayIsDeliveredOnlyOnce(t *testing.T) {
	_, manager, router, _, _, _ := newTestStack(t)
	conn := dialPlugin(t, manager, testToken)
	router.OnSocialEvent("event-1", "adapter-1", "999", "Alice", "repeat")
	router.OnSocialEvent("event-1", "adapter-1", "999", "Alice", "repeat")
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var packet map[string]any
	if err := conn.ReadJSON(&packet); err != nil || packet["exec_op"] != ExecSendToChat {
		t.Fatalf("expected first event delivery, packet=%#v err=%v", packet, err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if err := conn.ReadJSON(&packet); err == nil {
		t.Fatalf("replayed event caused a duplicate delivery: %#v", packet)
	}
}

func TestSocialMessageReachesMinecraftAndDoesNotEcho(t *testing.T) {
	_, manager, router, social, _, _ := newTestStack(t)
	conn := dialPlugin(t, manager, testToken)

	router.OnSocialMessage("adapter-1", "999", "Alice", "群里的消息")

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var packet map[string]any
	if err := conn.ReadJSON(&packet); err != nil {
		t.Fatalf("plugin did not receive the chat push: %v", err)
	}
	if op, _ := packet["op"].(float64); OpCode(op) != OpPacket {
		t.Fatalf("expected an op=Packet frame, got %#v", packet)
	}
	if packet["exec_op"] != ExecSendToChat {
		t.Fatalf("expected SEND_TO_CHAT, got %#v", packet)
	}
	text, _ := packet["text"].(string)
	if text != "[999] Alice: 群里的消息" {
		t.Fatalf("unexpected text: %q", text)
	}

	// A plugin reporting the pushed line back must not bounce into the group.
	router.OnMinecraftChat(serverOf(t, manager), SyncMessagePacket{Message: text, Player: PlayerInfo{PlayerName: "Alice"}})
	if call, ok := social.last(); ok {
		t.Fatalf("echo was forwarded back to the social platform: %#v", call)
	}
}

func TestUnknownTokenIsRejected(t *testing.T) {
	_, manager, _, _, _, _ := newTestStack(t)
	server := httptest.NewServer(manager)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/bridge"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var hello map[string]any
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(map[string]any{"op": OpIdentify, "token": "wrong", "plugin_version": "2.3.0"}); err != nil {
		t.Fatal(err)
	}
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				return
			}
			return
		}
		t.Fatal("host kept the socket open for an invalid token")
	}
}

func TestStatusesListConfiguredServersAsOffline(t *testing.T) {
	_, manager, _, _, serverID, _ := newTestStack(t)
	statuses, err := manager.Statuses()
	if err != nil {
		t.Fatal(err)
	}
	status, ok := statuses[serverID]
	if !ok || status.State != "offline" {
		t.Fatalf("expected an offline entry, got %#v", statuses)
	}
	if err := manager.SendToChat(serverID, "x"); err != ErrServerOffline {
		t.Fatalf("expected ErrServerOffline, got %v", err)
	}
}

func serverOf(t *testing.T, manager *Manager) string {
	t.Helper()
	statuses, err := manager.Statuses()
	if err != nil || len(statuses) != 1 {
		t.Fatalf("unexpected statuses: %#v %v", statuses, err)
	}
	for id := range statuses {
		return id
	}
	return ""
}

func TestServerTokenExtraction(t *testing.T) {
	cases := map[string]string{
		`{"token":"abc"}`:         "abc",
		`{"Token":"legacy"}`:      "legacy",
		`{"serverToken":"snake"}`: "snake",
		`{}`:                      "",
		`not json`:                "",
	}
	for detail, want := range cases {
		if got := ServerToken(store.Record{Detail: detail}); got != want {
			t.Fatalf("ServerToken(%s) = %q, want %q", detail, got, want)
		}
	}
}

func TestRenderKeepsUnknownPlaceholders(t *testing.T) {
	if got := render("", defaultMinecraft, map[string]string{"server": "S", "player": "P", "content": "C"}); got != "[S] P: C" {
		t.Fatalf("unexpected default render: %q", got)
	}
	if got := render("{server} {unknown}", defaultMinecraft, map[string]string{"server": "S"}); got != "S {unknown}" {
		t.Fatalf("unknown placeholders must be preserved: %q", got)
	}
}

func TestPluginJoinIsAdmittedWithoutBinding(t *testing.T) {
	_, manager, _, _, _, _ := newTestStack(t)
	conn := dialPlugin(t, manager, testToken)
	manager.mu.RLock()
	serverID := ""
	for id := range manager.sessions {
		serverID = id
	}
	manager.mu.RUnlock()
	packet := map[string]any{"op": OpPacket, "callback_id": "join-1", "exec_op": ExecPlayerJoin,
		"player": map[string]any{"player_name": "Steve", "player_uuid": "uuid-1"}}
	if err := conn.WriteJSON(envelopePacketForTest(manager, serverID, 1, packet)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var callback map[string]any
	if err := conn.ReadJSON(&callback); err != nil {
		t.Fatalf("no login callback: %v", err)
	}
	if callback["callback_id"] != "join-1" || callback["kick"] != false {
		t.Fatalf("unexpected login callback: %#v", callback)
	}
	if op, _ := callback["op"].(float64); OpCode(op) != OpCallBack {
		t.Fatalf("expected op=CallBack, got %#v", callback)
	}
}

func TestDisabledServerRejectsToken(t *testing.T) {
	db, manager, _, _, serverID, _ := newTestStack(t)
	record, err := db.Get(context.Background(), "servers", serverID)
	if err != nil {
		t.Fatal(err)
	}
	record.Enabled = false
	if _, err := db.Save(context.Background(), record); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(manager)
	defer server.Close()
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/bridge"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := conn.ReadJSON(&map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(map[string]any{"op": OpIdentify, "token": testToken, "plugin_version": "2.3.0"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("a disabled server must reject a valid token")
	}
}

func TestDisconnectServerClosesSession(t *testing.T) {
	_, manager, _, _, serverID, _ := newTestStack(t)
	conn := dialPlugin(t, manager, testToken)
	if !manager.Online(serverID) {
		t.Fatal("expected the bridge session to be online")
	}

	manager.DisconnectServer(serverID)

	deadline := time.Now().Add(2 * time.Second)
	online := true
	for time.Now().Before(deadline) {
		if !manager.Online(serverID) {
			online = false
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if online {
		t.Fatal("expected the bridge session to be offline after DisconnectServer")
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("expected the plugin socket to be closed after DisconnectServer")
	}
}

func TestV2HandshakeReplayProtectionAndStatus(t *testing.T) {
	db, _, _, _, serverID, _ := newTestStack(t)
	manager := NewManager(db, slog.New(slog.NewTextHandler(io.Discard, nil)), Callbacks{})
	server := httptest.NewServer(manager)
	defer server.Close()
	defer manager.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var hello HelloPacket
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatal(err)
	}
	if hello.ProtocolVersion != ProtocolVersion || hello.AuthMethod != "hmac-sha256" {
		t.Fatalf("unexpected hello: %#v", hello)
	}
	timestamp := time.Now().Unix()
	identify := IdentifyPacket{Op: OpIdentify, ProtocolVersion: ProtocolVersion, ConnectionID: hello.SessionID,
		ServerID: serverID, Timestamp: timestamp, Proof: Proof(testToken, hello.SessionID, hello.Token, serverID, timestamp), PluginVersion: "MCDReforged/0.1.0"}
	if err := conn.WriteJSON(identify); err != nil {
		t.Fatal(err)
	}
	var success IdentifySuccessPacket
	if err := conn.ReadJSON(&success); err != nil {
		t.Fatal(err)
	}
	if success.Op != OpIdentifySuccess {
		t.Fatalf("unexpected identify result: %#v", success)
	}
	status, err := manager.Status(serverID)
	if err != nil || status.ProtocolVersion != ProtocolVersion {
		t.Fatalf("status=%#v err=%v", status, err)
	}
	frame := envelopeForTest(hello.SessionID, serverID, 1)
	if err := conn.WriteJSON(frame); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if !manager.Online(serverID) {
		t.Fatal("valid heartbeat frame disconnected the bridge")
	}
	if err := conn.WriteJSON(envelopeForTest(hello.SessionID, serverID, 1)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !manager.Online(serverID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("replayed sequence did not close the connection")
}

func envelopePacketForTest(manager *Manager, serverID string, sequence int, payload any) map[string]any {
	manager.mu.RLock()
	var connectionID string
	for _, session := range manager.sessions {
		connectionID = session.id
	}
	manager.mu.RUnlock()
	encoded, _ := json.Marshal(payload)
	return map[string]any{"protocol_version": ProtocolVersion, "connection_id": connectionID, "server_id": serverID,
		"request_id": uuid.NewString(), "timestamp": time.Now().Unix(), "sequence": sequence, "type": "packet", "payload": json.RawMessage(encoded)}
}

func envelopeForTest(connectionID, serverID string, sequence int) map[string]any {
	return map[string]any{"op": OpHeartBeat, "protocol_version": ProtocolVersion,
		"connection_id": connectionID, "server_id": serverID, "request_id": uuid.NewString(),
		"timestamp": time.Now().Unix(), "sequence": sequence, "type": "heartbeat", "payload": map[string]any{"op": OpHeartBeat}}
}

func TestRequiredBindingRejectsUnboundAndAllowsBoundPlayer(t *testing.T) {
	db, manager, _, _, serverID, _ := newTestStack(t)
	server, err := db.Get(context.Background(), "servers", serverID)
	if err != nil {
		t.Fatal(err)
	}
	server.Detail = `{"token":"` + testToken + `","requireBinding":true,"kickMessage":"先绑定"}`
	if _, err = db.Save(context.Background(), server); err != nil {
		t.Fatal(err)
	}
	join := func(callbackID string) bool {
		conn := dialPlugin(t, manager, testToken)
		defer conn.Close()
		packet := map[string]any{"op": OpPacket, "callback_id": callbackID, "exec_op": ExecPlayerJoin, "player": map[string]any{"player_name": "Steve", "player_uuid": "uuid-required"}}
		if err := conn.WriteJSON(envelopePacketForTest(manager, serverID, 1, packet)); err != nil {
			t.Fatal(err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var response map[string]any
		if err := conn.ReadJSON(&response); err != nil {
			t.Fatal(err)
		}
		return response["kick"] == true
	}
	if !join("unbound") {
		t.Fatal("unbound player was admitted")
	}
	challenge, err := db.CreateBindingChallenge(context.Background(), "uuid-required", serverID, "", 5*time.Minute, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ConfirmBinding(context.Background(), challenge.Code, "onebot11", "qq-required", "Steve", ""); err != nil {
		t.Fatal(err)
	}
	if join("bound") {
		t.Fatal("bound player was rejected")
	}
}

func TestSecureBridgeRejectsLegacyTokenOnlyIdentify(t *testing.T) {
	db, _, _, _, _, _ := newTestStack(t)
	manager := NewManager(db, slog.New(slog.NewTextHandler(io.Discard, nil)), Callbacks{})
	server := httptest.NewServer(manager)
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var hello HelloPacket
	if err := conn.ReadJSON(&hello); err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteJSON(map[string]any{"op": OpIdentify, "token": testToken}); err != nil {
		t.Fatal(err)
	}
	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Fatal("legacy token-only authentication unexpectedly succeeded")
	}
}
