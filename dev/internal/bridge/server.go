package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"easybot/internal/store"
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
	OnChat      func(serverID string, packet SyncMessagePacket)
	OnEnterExit func(serverID string, packet SyncEnterExitMessagePacket)
	OnDeath     func(serverID string, packet SyncDeathMessagePacket)
	OnState     func(serverID string, online bool)
}

// Status is the observed connection state of one Minecraft server. It is never
// derived from the stored configuration.
type Status struct {
	ServerID          string     `json:"serverId"`
	ServerName        string     `json:"serverName"`
	State             string     `json:"state"`
	PluginVersion     string     `json:"pluginVersion,omitempty"`
	ServerDescription string     `json:"serverDescription,omitempty"`
	RemoteAddr        string     `json:"remoteAddr,omitempty"`
	ConnectedAt       *time.Time `json:"connectedAt,omitempty"`
	LastSeenAt        *time.Time `json:"lastSeenAt,omitempty"`
	OnlinePlayers     int        `json:"onlinePlayers"`
	LastError         string     `json:"lastError,omitempty"`
}

// Manager owns every live plugin connection and exposes the host side of the
// Bridge protocol as an http.Handler mounted on /bridge.
type Manager struct {
	db      *store.Store
	logger  *slog.Logger
	call    Callbacks
	upgrade websocket.Upgrader

	mu       sync.RWMutex
	sessions map[string]*session
	statuses map[string]Status
}

func NewManager(db *store.Store, logger *slog.Logger, callbacks Callbacks) *Manager {
	return &Manager{
		db:       db,
		logger:   logger,
		call:     callbacks,
		sessions: make(map[string]*session),
		statuses: make(map[string]Status),
		upgrade: websocket.Upgrader{
			HandshakeTimeout: 5 * time.Second,
			// The plugin is a desktop-side client, not a browser; it does not
			// send an Origin header.
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	if err := s.writeJSON(HelloPacket{
		Op:        OpHello,
		Version:   "BridgeCore (26.5.8)",
		System:    "Unix-based(linux-x64)",
		Dotnet:    ".NET 8.0.0",
		SessionID: s.id,
		Token:     uuid.NewString(),
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

	record, token, err := m.authenticate(identify.Token)
	if err != nil {
		m.logger.Warn("bridge authentication rejected", "remote", remoteAddr, "error", err)
		m.closeWith(s, "错误的令牌, 请检查令牌是否正确!!!")
		return
	}

	s.serverID = record.ID
	s.serverName = record.Name
	s.pluginVersion = identify.PluginVersion
	s.token = token
	if err := s.writeJSON(IdentifySuccessPacket{Op: OpIdentifySuccess, ServerName: record.Name, SupportGetPluginList: true}); err != nil {
		return
	}

	m.register(s)
	defer m.unregister(s)

	m.logger.Info("minecraft bridge connected",
		"server", record.Name, "server_id", record.ID, "plugin", identify.PluginVersion,
		"description", identify.ServerDescription, "remote", remoteAddr)
	if m.call.OnState != nil {
		m.call.OnState(record.ID, true)
	}
	defer func() {
		if m.call.OnState != nil {
			m.call.OnState(record.ID, false)
		}
		m.logger.Info("minecraft bridge disconnected", "server", record.Name, "server_id", record.ID)
	}()

	for {
		_ = conn.SetReadDeadline(time.Now().Add(2*HeartbeatInterval + 30*time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		m.touch(s.serverID)
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
		m.setPlayers(s.serverID, 0)
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
		// ReportPlay is a binding gate in the original product. Without the
		// binding workflow the host always admits the player.
		s.writeJSON(map[string]any{
			"op":          OpCallBack,
			"callback_id": header.CallbackID,
			"kick":        false,
			"kick_message": "",
		})
	}
}

// authenticate resolves a server record by its bridge token. Only configured
// servers can connect; an unknown token is rejected.
func (m *Manager) authenticate(token string) (store.Record, string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return store.Record{}, "", ErrUnknownServer
	}
	records, err := m.db.List(context.Background(), "servers")
	if err != nil {
		return store.Record{}, "", err
	}
	for _, record := range records {
		if ServerToken(record) == token {
			return record, token, nil
		}
	}
	return store.Record{}, "", ErrUnknownServer
}

// ServerToken extracts the bridge token from a server record's detail JSON.
func ServerToken(record store.Record) string {
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

func (m *Manager) register(s *session) {
	m.mu.Lock()
	if previous := m.sessions[s.serverID]; previous != nil && previous != s {
		m.mu.Unlock()
		previous.writeClose("该服务器已从其他地方连接")
		m.mu.Lock()
	}
	m.sessions[s.serverID] = s
	now := time.Now().UTC()
	m.statuses[s.serverID] = Status{
		ServerID:          s.serverID,
		ServerName:        s.serverName,
		State:             "online",
		PluginVersion:     s.pluginVersion,
		RemoteAddr:        s.remoteAddr,
		ConnectedAt:       &now,
		LastSeenAt:        &now,
	}
	m.mu.Unlock()
}

func (m *Manager) unregister(s *session) {
	s.closePending()
	m.mu.Lock()
	defer m.mu.Unlock()
	if current := m.sessions[s.serverID]; current != s {
		return
	}
	delete(m.sessions, s.serverID)
	status := m.statuses[s.serverID]
	status.State = "offline"
	status.PluginVersion = ""
	status.OnlinePlayers = 0
	status.ConnectedAt = nil
	m.statuses[s.serverID] = status
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
	id            string
	serverID      string
	serverName    string
	pluginVersion string
	token         string
	remoteAddr    string
	conn          *websocket.Conn

	writeMu sync.Mutex
	pendingMu sync.Mutex
	pending map[string]chan []byte
}

func (s *session) writeJSON(value any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
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
