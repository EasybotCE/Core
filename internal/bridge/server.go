package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"easybot/internal/model"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// HeartbeatInterval is advertised to the plugin in HelloPacket and used to size
// the socket read deadline. The original default is 120 seconds.
const HeartbeatInterval = 120 * time.Second

// identifyTimeout bounds how long a socket may stay unauthenticated.
const identifyTimeout = 10 * time.Second

var (
	ErrServerOffline = errors.New("minecraft server is not connected")
	ErrUnknownServer = errors.New("minecraft server not found")
)

// Callbacks are the host-side reactions to plugin events. All of them are
// optional and are invoked from the socket read goroutine, so they must not
// block for long.
type Callbacks struct {
	OnChat func(serverID string, packet SyncMessagePacket)
	// OnStartBind and OnGetSocialAccount implement binding calls from the official game plugins.
	OnStartBind        func(serverID, playerName string) (model.BindingChallenge, error)
	OnGetSocialAccount func(serverID, playerName string) (model.Binding, error)
	// OnPlayerJoin runs binding admission and challenge creation before the plugin receives its join callback.
	OnPlayerJoin func(serverID string, packet SyncMessagePacket, requireBinding bool, kickMessage string) (model.BindingJoinResult, error)
	OnEnterExit  func(serverID string, packet SyncEnterExitMessagePacket)
	OnDeath      func(serverID string, packet SyncDeathMessagePacket)
	OnState      func(serverID string, online bool)
}

// Status is the observed connection state of one Minecraft server. It is never
// derived from the stored configuration.
type Status struct {
	ServerID          string     `json:"serverId"`
	ServerName        string     `json:"serverName"`
	State             string     `json:"state"`
	PluginVersion     string     `json:"pluginVersion,omitempty"`
	ServerDescription string     `json:"serverDescription,omitempty"`
	ProtocolVersion   int        `json:"protocolVersion"`
	ConnectionID      string     `json:"connectionId,omitempty"`
	Capabilities      []string   `json:"capabilities,omitempty"`
	RemoteAddr        string     `json:"remoteAddr,omitempty"`
	ConnectedAt       *time.Time `json:"connectedAt,omitempty"`
	LastSeenAt        *time.Time `json:"lastSeenAt,omitempty"`
	OnlinePlayers     int        `json:"onlinePlayers"`
	LastError         string     `json:"lastError,omitempty"`
}

// Manager owns every live plugin connection and exposes the host side of the
// Bridge protocol as an http.Handler mounted on /bridge.
type Manager struct {
	db      Repository
	logger  *slog.Logger
	call    Callbacks
	upgrade websocket.Upgrader

	mu       sync.RWMutex
	sessions map[string]*session
	statuses map[string]Status
	closed   bool
}

func NewManager(db Repository, logger *slog.Logger, callbacks Callbacks) *Manager {
	return &Manager{
		db: db, logger: logger, call: callbacks, sessions: make(map[string]*session), statuses: make(map[string]Status),
		upgrade: websocket.Upgrader{HandshakeTimeout: 5 * time.Second, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }},
	}
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	closed := m.closed
	m.mu.RUnlock()
	if closed {
		http.Error(w, "bridge is shutting down", http.StatusServiceUnavailable)
		return
	}

	conn, err := m.upgrade.Upgrade(w, r, nil)
	if err != nil {
		m.logger.Warn("bridge upgrade failed", "remote", r.RemoteAddr, "error", err)
		return
	}
	m.serve(conn, r.RemoteAddr)
}

func (m *Manager) serve(conn *websocket.Conn, remoteAddr string) {
	conn.SetReadLimit(maxFrameSize)
	defer conn.Close()

	s := &session{id: uuid.NewString(), conn: conn, remoteAddr: remoteAddr, pending: make(map[string]chan []byte)}

	nonce := uuid.NewString()
	if err := s.writeJSON(HelloPacket{
		ProtocolVersion: ProtocolVersion, AuthMethod: "hmac-sha256",
		Op:        OpHello,
		Version:   "EasyBotCE Bridge/2",
		System:    "Go",
		Dotnet:    "",
		SessionID: s.id,
		Token:     nonce,
		Interval:  int(HeartbeatInterval.Seconds()),
	}); err != nil {
		m.logger.Warn("bridge hello failed", "remote", remoteAddr, "error", err)
		return
	}

	_ = conn.SetReadDeadline(time.Now().Add(identifyTimeout))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		m.logger.Info("bridge client disconnected before identify", "remote", remoteAddr, "error", err)
		return
	}
	var identify IdentifyPacket
	if err := json.Unmarshal(raw, &identify); err != nil || identify.Op != OpIdentify {
		m.closeWith(s, "未授权: 首包必须是 Identify")
		return
	}

	record, token, err := m.authenticateIdentify(identify, s, nonce)
	if err != nil {
		m.logger.Warn("bridge authentication rejected", "remote", remoteAddr, "error", err)
		m.closeWith(s, "鉴权失败或协议版本不兼容，请检查 server_id、令牌及系统时间")
		return
	}

	s.serverID = record.ID
	s.serverName = record.Name
	s.pluginVersion = identify.PluginVersion
	s.token = token
	s.description = identify.ServerDescription
	s.capabilities = identify.Capabilities

	// Make the authenticated session visible before acknowledging identification.
	// Clients may immediately query status after receiving IdentifySuccess.
	if !m.register(s) {
		return
	}
	defer m.unregister(s)
	if err := s.writeJSON(IdentifySuccessPacket{Op: OpIdentifySuccess, ServerName: record.Name, SupportGetPluginList: false}); err != nil {
		return
	}

	m.logger.Info("minecraft bridge connected",
		"server", record.Name, "server_id", record.ID, "plugin", identify.PluginVersion,
		"description", identify.ServerDescription, "remote", remoteAddr)
	if m.call.OnState != nil {
		m.call.OnState(record.ID, true)
	}
	defer func() {
		m.logger.Info("minecraft bridge disconnected", "server", record.Name, "server_id", record.ID)
	}()

	for {
		_ = conn.SetReadDeadline(time.Now().Add(2*HeartbeatInterval + 30*time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if m.session(s.serverID) != s {
			return
		}
		if !s.validateEnvelope(raw) {
			m.markError(s, "协议帧无效、过期或重复")
			m.closeWith(s, "协议帧无效、过期或重复")
			return
		}
		m.touch(s.serverID)
		if s.protocolVersion == ProtocolVersion {
			var envelope struct {
				Payload json.RawMessage `json:"payload"`
			}
			if json.Unmarshal(raw, &envelope) != nil || len(envelope.Payload) == 0 {
				return
			}
			raw = envelope.Payload
		}
		m.handleFrame(s, raw)
	}
}

func (m *Manager) handleFrame(s *session, raw []byte) {
	var header PacketHeader
	if err := json.Unmarshal(raw, &header); err != nil {
		return
	}
	switch header.Op {
	case OpHeartBeat:
		return
	case OpIdentify:
		s.writeClose("重复鉴权!")
		return
	case OpCallBack:
		s.resolve(header.CallbackID, raw)
		return
	case OpPacket:
	default:
		return
	}

	switch header.ExecOp {
	case ExecStartBind:
		var request struct {
			PlayerName string `json:"player_name"`
		}
		if json.Unmarshal(raw, &request) != nil {
			return
		}
		result := map[string]any{"op": OpCallBack, "callback_id": header.CallbackID, "exec_op": header.ExecOp, "code": "", "time": ""}
		if m.call.OnStartBind != nil {
			if challenge, err := m.call.OnStartBind(s.serverID, request.PlayerName); err == nil {
				result["code"] = challenge.Code
				result["time"] = time.Until(challenge.ExpiresAt).Round(time.Second).String()
			}
		}
		s.writeJSON(result)
	case ExecGetSocialAccount:
		var request struct {
			PlayerName string `json:"player_name"`
		}
		if json.Unmarshal(raw, &request) != nil {
			return
		}
		result := map[string]any{"op": OpCallBack, "callback_id": header.CallbackID, "exec_op": header.ExecOp, "time": "", "platform": "", "name": "", "uuid": ""}
		if m.call.OnGetSocialAccount != nil {
			if binding, err := m.call.OnGetSocialAccount(s.serverID, request.PlayerName); err == nil {
				result["time"] = binding.BoundAt.Format(time.RFC3339)
				result["platform"] = binding.PlatformType
				result["name"] = binding.DisplayName
				result["uuid"] = binding.AccountID
			}
		}
		s.writeJSON(result)
	case ExecSyncMessage:
		var packet SyncMessagePacket
		if json.Unmarshal(raw, &packet) != nil {
			return
		}
		packet.Message = clampChatLine(packet.Message)
		if packet.Message == "" {
			return
		}
		if m.call.OnChat != nil {
			m.call.OnChat(s.serverID, packet)
		}
	case ExecSyncEnterExitMessage:
		var packet SyncEnterExitMessagePacket
		if json.Unmarshal(raw, &packet) != nil {
			return
		}
		if packet.Players != "" {
			m.setPlayers(s.serverID, countPlayers(packet.Players))
		} else {
			delta := -1
			if packet.IsEnter {
				delta = 1
			}
			m.adjustPlayers(s.serverID, delta)
		}
		if m.call.OnEnterExit != nil {
			m.call.OnEnterExit(s.serverID, packet)
		}
	case ExecSyncDeathMessage:
		var packet SyncDeathMessagePacket
		if json.Unmarshal(raw, &packet) != nil {
			return
		}
		if m.call.OnDeath != nil {
			m.call.OnDeath(s.serverID, packet)
		}
	case ExecServerStateChanged:
		var packet ServerStatePacket
		if json.Unmarshal(raw, &packet) != nil {
			return
		}
		m.setPlayers(s.serverID, countPlayers(packet.Players))
	case ExecPlayerJoin:
		var packet SyncMessagePacket
		_ = json.Unmarshal(raw, &packet)
		var config struct {
			RequireBinding bool   `json:"requireBinding"`
			KickMessage    string `json:"kickMessage"`
		}
		if server, err := m.db.Get(context.Background(), "servers", s.serverID); err == nil {
			_ = json.Unmarshal([]byte(server.Detail), &config)
		}
		result := model.BindingJoinResult{}
		if m.call.OnPlayerJoin != nil {
			var err error
			result, err = m.call.OnPlayerJoin(s.serverID, packet, config.RequireBinding, config.KickMessage)
			if err != nil {
				result.Kick = config.RequireBinding
				result.KickMessage = "绑定校验暂时不可用，请稍后重试。"
			}
		} else if config.RequireBinding {
			bound, err := m.db.IsPlayerBound(context.Background(), packet.Player.PlayerUUID)
			if err != nil || !bound {
				result.Kick = true
				result.KickMessage = config.KickMessage
				if result.KickMessage == "" {
					result.KickMessage = "请先完成账号绑定后再进入服务器。"
				}
			}
		}
		s.writeJSON(map[string]any{"op": OpCallBack, "callback_id": header.CallbackID, "kick": result.Kick, "kick_message": result.KickMessage})
	}
}

// ServerToken extracts the bridge token from a server record's detail JSON.
func ServerToken(record model.Record) string {
	var detail map[string]json.RawMessage
	if json.Unmarshal([]byte(record.Detail), &detail) != nil {
		return ""
	}
	for _, key := range []string{"token", "Token", "serverToken"} {
		if raw, ok := detail[key]; ok {
			var value string
			if json.Unmarshal(raw, &value) == nil && value != "" {
				return value
			}
		}
	}
	return ""
}

func (m *Manager) register(s *session) bool {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return false
	}
	previous := m.sessions[s.serverID]
	m.sessions[s.serverID] = s
	now := time.Now().UTC()
	m.statuses[s.serverID] = Status{
		ServerID: s.serverID, ServerName: s.serverName, State: "online",
		PluginVersion: s.pluginVersion, ServerDescription: s.description,
		ProtocolVersion: s.protocolVersion, ConnectionID: s.id, Capabilities: s.capabilities,
		RemoteAddr: s.remoteAddr, ConnectedAt: &now, LastSeenAt: &now,
	}
	m.mu.Unlock()
	if previous != nil && previous != s {
		m.closeWith(previous, "该服务器已从其他地方连接")
	}
	return true
}

func (m *Manager) unregister(s *session) {
	s.closePending()
	m.mu.Lock()
	if current := m.sessions[s.serverID]; current != s {
		m.mu.Unlock()
		return
	}
	delete(m.sessions, s.serverID)
	status := m.statuses[s.serverID]
	status.State = "offline"
	status.OnlinePlayers = 0
	status.ConnectedAt = nil
	m.statuses[s.serverID] = status
	m.mu.Unlock()
	if m.call.OnState != nil {
		m.call.OnState(s.serverID, false)
	}
}

func (m *Manager) markError(s *session, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions[s.serverID] != s {
		return
	}
	status := m.statuses[s.serverID]
	status.LastError = message
	m.statuses[s.serverID] = status
}

// Close closes hijacked WebSockets, which http.Server.Shutdown does not close.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	sessions := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()
	for _, s := range sessions {
		m.closeWith(s, "Bridge 服务已停止")
	}
}

func (m *Manager) touch(serverID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := m.statuses[serverID]
	now := time.Now().UTC()
	status.LastSeenAt = &now
	status.State = "online"
	m.statuses[serverID] = status
}

func (m *Manager) adjustPlayers(serverID string, delta int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := m.statuses[serverID]
	status.OnlinePlayers += delta
	if status.OnlinePlayers < 0 {
		status.OnlinePlayers = 0
	}
	m.statuses[serverID] = status
}

func (m *Manager) setPlayers(serverID string, players int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	status := m.statuses[serverID]
	status.OnlinePlayers = players
	m.statuses[serverID] = status
}

// Statuses reports every configured server, using the observed state when a
// plugin is connected and "offline" otherwise.
func (m *Manager) Statuses() (map[string]Status, error) {
	records, err := m.db.List(context.Background(), "servers")
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]Status, len(records))
	for _, record := range records {
		status, ok := m.statuses[record.ID]
		if !ok {
			status = Status{ServerID: record.ID, ServerName: record.Name, State: "offline"}
		}
		if status.ServerName == "" {
			status.ServerName = record.Name
		}
		result[record.ID] = status
	}
	return result, nil
}

// Status reports one server, returning ErrUnknownServer for unknown ids.
func (m *Manager) Status(serverID string) (Status, error) {
	all, err := m.Statuses()
	if err != nil {
		return Status{}, err
	}
	status, ok := all[serverID]
	if !ok {
		return Status{}, ErrUnknownServer
	}
	return status, nil
}

// SendToChat broadcasts a line to every player on the server.
func (m *Manager) SendToChat(serverID, text string) error {
	s := m.session(serverID)
	if s == nil {
		return ErrServerOffline
	}
	return s.writeJSON(SendToChatPacket{
		Op:         OpPacket,
		CallbackID: uuid.NewString(),
		ExecOp:     ExecSendToChat,
		Text:       clampChatLine(text),
	})
}

// RunCommand asks the plugin to run a command. An empty player name runs it
// from the console.
func (m *Manager) RunCommand(ctx context.Context, serverID, playerName, command string) ([]byte, error) {
	s := m.session(serverID)
	if s == nil {
		return nil, ErrServerOffline
	}
	return s.call(ctx, RunCommandPacket{
		Op:         OpPacket,
		ExecOp:     ExecRunCommand,
		PlayerName: playerName,
		Command:    command,
	})
}

// Online reports whether a server currently has an authenticated socket.
func (m *Manager) Online(serverID string) bool {
	return m.session(serverID) != nil
}

// DisconnectServer closes any live bridge session for the server and asks the
// plugin to reconnect. It is used after a token rotation so the old, still
// authenticated session cannot keep sending or receiving messages, and the
// plugin must re-authenticate with the new token on its next connect.
func (m *Manager) DisconnectServer(serverID string) {
	s := m.session(serverID)
	if s == nil {
		return
	}
	m.closeWith(s, "令牌已轮换，请使用新令牌重新连接")
}

func (m *Manager) session(serverID string) *session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[serverID]
}

func (m *Manager) closeWith(s *session, reason string) {
	s.writeClose(reason)
	_ = s.conn.Close()
}

func countPlayers(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	return len(strings.Split(value, ","))
}

// session is one authenticated plugin connection.
type session struct {
	id               string
	serverID         string
	serverName       string
	pluginVersion    string
	token            string
	remoteAddr       string
	description      string
	capabilities     []string
	protocolVersion  int
	sentSequence     uint64
	receivedSequence uint64
	conn             *websocket.Conn

	writeMu   sync.Mutex
	pendingMu sync.Mutex
	pending   map[string]chan []byte
}

func (s *session) writeJSON(value any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if s.protocolVersion == ProtocolVersion {
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		var packet map[string]any
		if err := json.Unmarshal(raw, &packet); err != nil {
			return err
		}
		s.sentSequence++
		packet["protocol_version"] = ProtocolVersion
		packet["type"] = packetType(packet)
		packet["payload"] = copyPacketPayload(packet)
		packet["connection_id"] = s.id
		packet["server_id"] = s.serverID
		packet["request_id"] = uuid.NewString()
		packet["timestamp"] = time.Now().Unix()
		packet["sequence"] = s.sentSequence
		return s.conn.WriteJSON(packet)
	}
	return s.conn.WriteJSON(value)
}

func (s *session) writeClose(reason string) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_ = s.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason))
}

func (s *session) resolve(callbackID string, raw []byte) {
	if callbackID == "" {
		return
	}
	s.pendingMu.Lock()
	waiter := s.pending[callbackID]
	delete(s.pending, callbackID)
	s.pendingMu.Unlock()
	if waiter != nil {
		select {
		case waiter <- raw:
		default:
		}
	}
}

func (s *session) closePending() {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()
	for id, waiter := range s.pending {
		delete(s.pending, id)
		close(waiter)
	}
}

func (s *session) call(ctx context.Context, packet RunCommandPacket) ([]byte, error) {
	packet.CallbackID = uuid.NewString()
	waiter := make(chan []byte, 1)
	s.pendingMu.Lock()
	s.pending[packet.CallbackID] = waiter
	s.pendingMu.Unlock()
	defer func() {
		s.pendingMu.Lock()
		delete(s.pending, packet.CallbackID)
		s.pendingMu.Unlock()
	}()
	if err := s.writeJSON(packet); err != nil {
		return nil, err
	}
	select {
	case raw, ok := <-waiter:
		if !ok {
			return nil, ErrServerOffline
		}
		return raw, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func packetType(packet map[string]any) string {
	if value, ok := packet["exec_op"].(string); ok && value != "" {
		return value
	}
	if value, ok := packet["op"].(float64); ok {
		return fmt.Sprintf("op_%d", int(value))
	}
	if value, ok := packet["op"].(OpCode); ok {
		return fmt.Sprintf("op_%d", value)
	}
	return "packet"
}

func copyPacketPayload(packet map[string]any) map[string]any {
	payload := make(map[string]any, len(packet))
	for key, value := range packet {
		if key == "protocol_version" || key == "connection_id" || key == "server_id" || key == "request_id" || key == "timestamp" || key == "sequence" || key == "type" || key == "payload" {
			continue
		}
		payload[key] = value
	}
	return payload
}
