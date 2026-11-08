// SPDX-License-Identifier: AGPL-3.0-only
// Package session implements authenticated CE WebSocket sessions. It never
// interprets a self-declared hello identity as an authentication credential.
package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/EasybotCE/easybotce/internal/pipeline"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EasybotCE/easybotce/internal/protocol"
	"github.com/EasybotCE/easybotce/internal/store"
	"github.com/coder/websocket"
)

const HeartbeatInterval = 20 * time.Second
const MaxSessions = 1024

type Info struct {
	ID           string   `json:"id"`
	EndpointID   string   `json:"endpoint_id"`
	CredentialID string   `json:"credential_id"`
	ConnectedAt  int64    `json:"connected_at"`
	Capabilities []string `json:"capabilities"`
}
type liveSession struct {
	Info
	conn     *websocket.Conn
	cancel   context.CancelFunc
	outgoing chan outgoing
}
type OneBotActionStatus struct {
	ID         string `json:"id"`
	EndpointID string `json:"endpoint_id"`
	Status     string `json:"status"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	Retcode    *int   `json:"retcode,omitempty"`
}

type Manager struct {
	store       *store.Store
	mu          sync.Mutex
	sessions    map[string]*liveSession // CE endpoint ID -> session; one active per endpoint
	onebot      map[string]*oneBotSession
	pending     chan struct{}
	connections map[*websocket.Conn]context.CancelFunc
	actions     map[string]OneBotActionStatus
	actionOrder []string
	closed      bool
	wg          sync.WaitGroup
}

func New(s *store.Store) *Manager {
	return &Manager{store: s, sessions: map[string]*liveSession{}, onebot: map[string]*oneBotSession{}, connections: map[*websocket.Conn]context.CancelFunc{}, pending: make(chan struct{}, 128), actions: map[string]OneBotActionStatus{}}
}

func SupportedCapabilities() []string {
	return []string{protocol.PublishCapability, protocol.ReceiveCapability}
}
func negotiate(requested, granted []string) []string {
	result := []string{}
	r := map[string]bool{}
	g := map[string]bool{}
	for _, c := range requested {
		r[c] = true
	}
	for _, c := range granted {
		g[c] = true
	}
	for _, c := range SupportedCapabilities() {
		if r[c] && g[c] {
			result = append(result, c)
		}
	}
	return result
}
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := []Info{}
	for _, s := range m.sessions {
		info := s.Info
		info.Capabilities = append([]string{}, s.Capabilities...)
		result = append(result, info)
	}
	for _, s := range m.onebot {
		result = append(result, s.Info)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].EndpointID < result[j].EndpointID })
	return result
}

// Mutate serializes credential/endpoint changes with final session admission.
// Committing a revocation and collecting its sessions under one lock prevents
// a pre-upgrade authentication result from winning a revoke/register race.
// match may be nil for a mutation that need not disconnect existing sessions.
func (m *Manager) Mutate(fn func() error, match func(Info) bool) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("session manager closed")
	}
	err := fn()
	var closing []*liveSession
	var closingBot []*oneBotSession
	if err == nil && match != nil {
		for id, s := range m.sessions {
			if match(s.Info) {
				delete(m.sessions, id)
				closing = append(closing, s)
			}
		}
		for id, s := range m.onebot {
			if match(s.Info) {
				delete(m.onebot, id)
				s.cancel()
				closingBot = append(closingBot, s)
			}
		}
	}
	m.mu.Unlock()
	for _, s := range closing {
		s.cancel()
		s.conn.CloseNow()
	}
	for _, s := range closingBot {
		s.conn.CloseNow()
	}
	return err
}
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	for conn, cancel := range m.connections {
		cancel()
		conn.CloseNow()
	}
	for _, s := range m.sessions {
		s.cancel()
		s.conn.CloseNow()
	}
	for _, s := range m.onebot {
		s.cancel()
		s.conn.CloseNow()
	}
	m.mu.Unlock()
	m.wg.Wait()
}
func bearer(r *http.Request) string {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return ""
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return token
}
func reject(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": "endpoint connection rejected"}})
}
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		reject(w, 405, "method_not_allowed")
		return
	}
	// No tokens in URL parameters. Explicitly reject query strings rather than
	// allowing a proxy/client to accidentally put credentials into access logs.
	if r.URL.RawQuery != "" {
		reject(w, 400, "invalid_request")
		return
	}
	pending := false
	select {
	case m.pending <- struct{}{}:
		pending = true
		defer func() {
			if pending {
				<-m.pending
			}
		}()
	default:
		reject(w, 503, "capacity_exceeded")
		return
	}
	token := bearer(r)
	endpoint, _, err := m.store.Authenticate(r.Context(), token)
	if err != nil {
		if errors.Is(err, store.ErrUnauthenticated) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="easybotce-endpoint"`)
			reject(w, 401, "unauthenticated")
		} else {
			reject(w, 503, "unavailable")
		}
		return
	}
	// Track upgraded handshakes as well as admitted sessions during shutdown.
	m.mu.Lock()
	if m.closed || len(m.sessions) >= MaxSessions {
		m.mu.Unlock()
		reject(w, 503, "capacity_exceeded")
		return
	}
	m.wg.Add(1)
	m.mu.Unlock()
	defer m.wg.Done()
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(protocol.MaxMessageBytes)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.connections[conn] = cancel
	m.mu.Unlock()
	defer func() { m.mu.Lock(); delete(m.connections, conn); m.mu.Unlock() }()
	helloCtx, helloCancel := context.WithTimeout(ctx, 5*time.Second)
	typ, data, err := conn.Read(helloCtx)
	helloCancel()
	if err != nil {
		return
	}
	msg, err := protocol.Decode(data)
	if err != nil || typ != websocket.MessageText || msg.Type != protocol.HelloType {
		fail(ctx, conn, "", "invalid_message", "first message must be a valid session.hello")
		return
	}
	var hello protocol.Hello
	_ = json.Unmarshal(msg.Payload, &hello)
	if hello.EndpointID != endpoint.ID || hello.EndpointType != endpoint.Type {
		fail(ctx, conn, msg.ID, "permission_denied", "hello identity does not match authenticated endpoint")
		return
	}
	if _, err := protocol.NegotiateVersion(hello.Versions); err != nil {
		fail(ctx, conn, msg.ID, "unsupported_version", "no supported protocol version")
		return
	}
	m.mu.Lock()
	// Re-authenticate inside the mutation/admission lock. Disabled, deleted or
	// revoked endpoints cannot slip through a handshake started before mutation.
	current, credential, authErr := m.store.Authenticate(ctx, token)
	_, duplicate := m.sessions[endpoint.ID]
	if m.closed || len(m.sessions) >= MaxSessions || duplicate || authErr != nil || current.ID != endpoint.ID {
		m.mu.Unlock()
		fail(ctx, conn, msg.ID, "permission_denied", "endpoint unavailable or already connected")
		return
	}
	info := Info{ID: store.NewID(), EndpointID: endpoint.ID, CredentialID: credential.ID, ConnectedAt: time.Now().Unix(), Capabilities: negotiate(hello.Capabilities, credential.Capabilities)}
	live := &liveSession{Info: info, conn: conn, cancel: cancel, outgoing: make(chan outgoing, 64)}
	<-m.pending
	pending = false
	m.sessions[endpoint.ID] = live
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if m.sessions[endpoint.ID] == live {
			delete(m.sessions, endpoint.ID)
		}
		m.mu.Unlock()
	}()
	welcome := protocol.Welcome{SessionID: info.ID, Version: protocol.Version, Capabilities: info.Capabilities, HeartbeatIntervalMS: int(HeartbeatInterval / time.Millisecond)}
	if err := send(ctx, conn, protocol.WelcomeType, msg.ID, welcome); err != nil {
		return
	}
	pingDone := make(chan struct{})
	go func() {
		defer close(pingDone)
		ticker := time.NewTicker(HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-pingDone }()
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case item := <-live.outgoing:
				if send(ctx, conn, item.kind, item.replyTo, item.payload) != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-writerDone }()
	// A bounded session-local ID cache prevents duplicate sends after client
	// retries. Reusing an ID with a different payload is rejected. Reconnects
	// start a new cache: this is deliberately not durable exactly-once delivery.
	type cached struct {
		hash     [32]byte
		accepted protocol.Accepted
	}
	seen := map[string]cached{}
	order := make([]string, 0, 1024)
	tokens := 40
	lastRefill := time.Now()
	for {
		typ, data, err = conn.Read(ctx)
		if err != nil {
			return
		}
		next, decodeErr := protocol.Decode(data)
		if typ != websocket.MessageText || decodeErr != nil || next.Type != protocol.PublishType {
			fail(ctx, conn, "", "invalid_message", "expected a valid message.publish")
			return
		}
		if !slices.Contains(info.Capabilities, protocol.PublishCapability) {
			fail(ctx, conn, next.ID, "permission_denied", "message.publish capability required")
			return
		}
		// Token bucket 20 messages/second, burst 40, independently per session.
		now := time.Now()
		refill := int(now.Sub(lastRefill) / (50 * time.Millisecond))
		if refill > 0 {
			tokens = min(40, tokens+refill)
			lastRefill = now
		}
		if tokens == 0 {
			fail(ctx, conn, next.ID, "permission_denied", "publish rate exceeded")
			return
		}
		tokens--
		var pub protocol.Publish
		_ = json.Unmarshal(next.Payload, &pub)
		canonical, _ := json.Marshal(pub)
		hash := sha256.Sum256(canonical)
		if old, ok := seen[next.ID]; ok {
			if old.hash != hash {
				fail(ctx, conn, next.ID, "invalid_message", "message id reused with different payload")
				return
			}
			ack := old.accepted
			ack.Duplicate = true
			if !enqueue(live, outgoing{protocol.AcceptedType, next.ID, ack}) {
				return
			}
			continue
		}
		routeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		ack, routeErr := m.publish(routeCtx, live, next.ID, pub)
		stop()
		if routeErr != nil {
			fail(ctx, conn, next.ID, "internal_error", "message routing unavailable")
			return
		}
		if len(order) == 1024 {
			delete(seen, order[0])
			order = order[1:]
		}
		order = append(order, next.ID)
		seen[next.ID] = cached{hash, ack}
		if !enqueue(live, outgoing{protocol.AcceptedType, next.ID, ack}) {
			return
		}
	}

}
func send(ctx context.Context, conn *websocket.Conn, kind, replyTo string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	data, err := json.Marshal(protocol.Message{Version: protocol.Version, ID: store.NewID(), Type: kind, ReplyTo: replyTo, Payload: raw})
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, data)
}
func fail(ctx context.Context, conn *websocket.Conn, replyTo, code, message string) {
	_ = send(ctx, conn, protocol.ErrorType, replyTo, protocol.Failure{Code: code, Message: message})
	// CloseNow in the caller bounds failure cleanup even if the peer never reads.
	// The error frame is the authoritative diagnostic; no user data is echoed.
}

type outgoing struct {
	kind, replyTo string
	payload       any
}

func enqueue(s *liveSession, item outgoing) bool {
	select {
	case s.outgoing <- item:
		return true
	default:
		return false
	}
}
func (m *Manager) publish(ctx context.Context, source *liveSession, id string, p protocol.Publish) (protocol.Accepted, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ack := protocol.Accepted{}
	if m.closed || m.sessions[source.EndpointID] != source {
		return ack, errors.New("session no longer active")
	}
	routes, err := m.store.ResolveRoutes(ctx, source.EndpointID, p.ChannelID)
	if err != nil {
		return ack, err
	}
	for _, r := range routes {
		text, matched, err := r.Rules.Apply(pipeline.Context{Text: p.Text, SenderID: p.SenderID, SenderName: p.SenderName, SourceEndpointID: source.EndpointID, TargetEndpointID: r.Binding.TargetEndpointID})
		if err != nil || len([]rune(text)) > 2048 {
			ack.Dropped++
			continue
		}
		if !matched {
			ack.Filtered++
			continue
		}
		delivery := protocol.Delivery{BindingID: r.Binding.ID, SourceEndpointID: source.EndpointID, SourceMessageID: id, SourceChannelID: p.ChannelID, ChannelID: r.Binding.TargetChannel, SenderID: p.SenderID, SenderName: p.SenderName, Text: text}
		if target := m.sessions[r.Binding.TargetEndpointID]; target != nil && slices.Contains(target.Capabilities, protocol.ReceiveCapability) {
			if enqueue(target, outgoing{kind: protocol.DeliverType, payload: delivery}) {
				ack.Queued++
			} else {
				ack.Dropped++
			}
			continue
		}
		if target := m.onebot[r.Binding.TargetEndpointID]; target != nil {
			if action, ok := target.prepareAction(r.Binding.TargetChannel, text); ok && m.trackOneBotAction(action, r.Binding.TargetEndpointID) {
				if target.enqueue(action) {
					ack.Queued++
				} else {
					m.markOneBotActionFailedLocked(action.id)
					ack.Dropped++
				}
			} else {
				ack.Dropped++
			}
		} else {
			ack.Dropped++
		}
	}
	return ack, nil
}

const maxActionHistory = 4096
const actionTimeout = 30 * time.Second

func (m *Manager) trackOneBotAction(action botAction, endpointID string) bool {
	now := time.Now().Unix()
	status := OneBotActionStatus{ID: action.id, EndpointID: endpointID, Status: "queued", CreatedAt: now, UpdatedAt: now}
	if err := m.store.SaveOneBotAction(context.Background(), store.OneBotAction{ID: status.ID, EndpointID: status.EndpointID, Status: status.Status, CreatedAt: status.CreatedAt, UpdatedAt: status.UpdatedAt}); err != nil {
		return false
	}
	m.actions[action.id] = status
	m.actionOrder = append(m.actionOrder, action.id)
	for len(m.actionOrder) > maxActionHistory {
		delete(m.actions, m.actionOrder[0])
		m.actionOrder = m.actionOrder[1:]
	}
	return true
}

// markOneBotActionFailedLocked is used while publish already owns m.mu.
func (m *Manager) markOneBotActionFailedLocked(id string) {
	action, ok := m.actions[id]
	if !ok {
		return
	}
	action.Status, action.UpdatedAt = "failed", time.Now().Unix()
	m.actions[id] = action
	_ = m.store.SaveOneBotAction(context.Background(), store.OneBotAction{ID: action.ID, EndpointID: action.EndpointID, Status: action.Status, CreatedAt: action.CreatedAt, UpdatedAt: action.UpdatedAt, Retcode: action.Retcode})
}

func (m *Manager) updateOneBotAction(id, status string, retcode *int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	action, ok := m.actions[id]
	if !ok || action.Status == "succeeded" || action.Status == "failed" || action.Status == "timeout" {
		return
	}
	action.Status, action.UpdatedAt, action.Retcode = status, time.Now().Unix(), retcode
	m.actions[id] = action
	_ = m.store.SaveOneBotAction(context.Background(), store.OneBotAction{ID: action.ID, EndpointID: action.EndpointID, Status: action.Status, CreatedAt: action.CreatedAt, UpdatedAt: action.UpdatedAt, Retcode: action.Retcode})
}
func (m *Manager) OneBotAction(id string) (OneBotActionStatus, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	action, ok := m.actions[id]
	if !ok {
		persisted, err := m.store.GetOneBotAction(context.Background(), id)
		if err != nil {
			return OneBotActionStatus{}, false
		}
		action = OneBotActionStatus{ID: persisted.ID, EndpointID: persisted.EndpointID, Status: persisted.Status, CreatedAt: persisted.CreatedAt, UpdatedAt: persisted.UpdatedAt, Retcode: persisted.Retcode}
		m.actions[id] = action
	}
	if (action.Status == "queued" || action.Status == "sent") && time.Since(time.Unix(action.UpdatedAt, 0)) > actionTimeout {
		action.Status, action.UpdatedAt = "timeout", time.Now().Unix()
		m.actions[id] = action
		_ = m.store.SaveOneBotAction(context.Background(), store.OneBotAction{ID: action.ID, EndpointID: action.EndpointID, Status: action.Status, CreatedAt: action.CreatedAt, UpdatedAt: action.UpdatedAt, Retcode: action.Retcode})
	}
	return action, true
}
func (m *Manager) ListOneBotActions(endpointID string, before int64, beforeID string, limit int) ([]OneBotActionStatus, error) {
	items, err := m.store.ListOneBotActions(context.Background(), endpointID, before, beforeID, limit)
	if err != nil {
		return nil, err
	}
	result := make([]OneBotActionStatus, 0, len(items))
	for _, item := range items {
		result = append(result, OneBotActionStatus{ID: item.ID, EndpointID: item.EndpointID, Status: item.Status, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, Retcode: item.Retcode})
	}
	return result, nil
}
func (m *Manager) oneBotActionResponse(echo json.RawMessage, status string, retcode int) {
	var id string
	if json.Unmarshal(echo, &id) != nil || id == "" {
		return
	}
	final := "failed"
	if strings.EqualFold(status, "ok") && retcode == 0 {
		final = "succeeded"
	}
	m.updateOneBotAction(id, final, &retcode)
}
