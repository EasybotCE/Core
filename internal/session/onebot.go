// SPDX-License-Identifier: AGPL-3.0-only
package session

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/EasybotCE/easybotce/internal/domain"
	"github.com/EasybotCE/easybotce/internal/onebot11"
	"github.com/EasybotCE/easybotce/internal/pipeline"
	"github.com/EasybotCE/easybotce/internal/protocol"
	"github.com/EasybotCE/easybotce/internal/store"
	"github.com/coder/websocket"
)

const MaxOneBotSessions = MaxSessions

type botAction struct{ id, channel, text string }
type oneBotSession struct {
	Info
	conn     *websocket.Conn
	cancel   context.CancelFunc
	outgoing chan botAction
	writeMu  sync.Mutex
}

func (s *oneBotSession) prepareAction(channel, text string) (botAction, bool) {
	platform, recipient, ok := strings.Cut(channel, "-")
	if !ok || (platform != "group" && platform != "private") {
		return botAction{}, false
	}
	id := store.NewID()
	if _, err := onebot11.Action(platform, recipient, text, id); err != nil {
		return botAction{}, false
	}
	return botAction{id: id, channel: platform + ":" + recipient, text: text}, true
}

func (s *oneBotSession) enqueue(action botAction) bool {
	select {
	case s.outgoing <- action:
		return true
	default:
		return false
	}
}

// ServeOneBotHTTP accepts authenticated OneBot 11 reverse-WebSocket sessions.
// Credentials are only accepted in Authorization headers, never URL queries.
func (m *Manager) ServeOneBotHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		reject(w, 405, "method_not_allowed")
		return
	}
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
	endpoint, credential, err := m.store.Authenticate(r.Context(), token)
	if err != nil {
		if errors.Is(err, store.ErrUnauthenticated) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="easybotce-onebot11"`)
			reject(w, 401, "unauthenticated")
		} else {
			reject(w, 503, "unavailable")
		}
		return
	}
	if endpoint.Type != domain.ChatBot {
		reject(w, 403, "endpoint_type_mismatch")
		return
	}
	if !slices.Contains(credential.Capabilities, protocol.PublishCapability) || !slices.Contains(credential.Capabilities, protocol.ReceiveCapability) {
		reject(w, 403, "capability_denied")
		return
	}
	m.mu.Lock()
	_, duplicateCE := m.sessions[endpoint.ID]
	_, duplicateBot := m.onebot[endpoint.ID]
	if duplicateCE || duplicateBot {
		m.mu.Unlock()
		reject(w, 409, "endpoint_already_connected")
		return
	}
	if m.closed || len(m.sessions)+len(m.onebot) >= MaxSessions {
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
	conn.SetReadLimit(onebot11.MaxEventBytes)
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
	m.mu.Lock()
	current, credential, authErr := m.store.Authenticate(ctx, token)
	_, duplicateCE = m.sessions[endpoint.ID]
	_, duplicateBot = m.onebot[endpoint.ID]
	if m.closed || len(m.sessions)+len(m.onebot) >= MaxSessions || duplicateCE || duplicateBot || authErr != nil || current.ID != endpoint.ID || current.Type != domain.ChatBot {
		m.mu.Unlock()
		reject(w, 403, "endpoint_unavailable")
		return
	}
	capabilities := []string{}
	for _, required := range []string{protocol.PublishCapability, protocol.ReceiveCapability} {
		if slices.Contains(credential.Capabilities, required) {
			capabilities = append(capabilities, required)
		}
	}
	if len(capabilities) != 2 {
		m.mu.Unlock()
		reject(w, 403, "capability_denied")
		return
	}
	live := &oneBotSession{Info: Info{ID: store.NewID(), EndpointID: endpoint.ID, CredentialID: credential.ID, ConnectedAt: time.Now().Unix(), Capabilities: capabilities}, conn: conn, cancel: cancel, outgoing: make(chan botAction, 64)}
	<-m.pending
	pending = false
	m.onebot[endpoint.ID] = live
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		if m.onebot[endpoint.ID] == live {
			delete(m.onebot, endpoint.ID)
		}
		m.mu.Unlock()
	}()
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case action := <-live.outgoing:
				platform, recipient, _ := strings.Cut(action.channel, ":")
				data, err := onebot11.Action(platform, recipient, action.text, action.id)
				if err != nil {
					m.updateOneBotAction(action.id, "failed", nil)
					continue
				}
				writeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
				live.writeMu.Lock()
				err = conn.Write(writeCtx, websocket.MessageText, data)
				live.writeMu.Unlock()
				stop()
				if err != nil {
					m.updateOneBotAction(action.id, "failed", nil)
					cancel()
					return
				}
				m.updateOneBotAction(action.id, "sent", nil)
			}
		}
	}()
	defer func() { cancel(); <-writerDone }()
	seen := make(map[string]struct{}, 2048)
	seenOrder := make([]string, 0, 2048)
	for {
		typ, data, readErr := conn.Read(ctx)
		if readErr != nil {
			return
		}
		if typ != websocket.MessageText {
			return
		}
		var envelope struct {
			Status   string          `json:"status"`
			Retcode  int             `json:"retcode"`
			Echo     json.RawMessage `json:"echo"`
			PostType string          `json:"post_type"`
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.Status != "" {
			// async is acceptance only; await a final action response.
			if strings.EqualFold(envelope.Status, "async") {
				continue
			}
			if envelope.Echo != nil {
				m.oneBotActionResponse(envelope.Echo, envelope.Status, envelope.Retcode)
			}
			continue
		}
		event, parseErr := onebot11.ParseMessageEvent(data, "")
		if parseErr != nil {
			var probe map[string]json.RawMessage
			if json.Unmarshal(data, &probe) != nil || len(probe) == 0 {
				return
			}
			var postType string
			_ = json.Unmarshal(probe["post_type"], &postType)
			if postType != "message" {
				continue
			}
			return
		}
		dedupeKey := event.ChannelID + ":" + event.MessageID
		if _, duplicate := seen[dedupeKey]; duplicate {
			continue
		}
		if len(seenOrder) == cap(seenOrder) {
			delete(seen, seenOrder[0])
			copy(seenOrder, seenOrder[1:])
			seenOrder = seenOrder[:len(seenOrder)-1]
		}
		seen[dedupeKey] = struct{}{}
		seenOrder = append(seenOrder, dedupeKey)
		pub := protocol.Publish{ChannelID: event.ChannelID, SenderID: event.UserID, SenderName: event.Nickname, Text: event.Text}
		routeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		_, routeErr := m.publishExternal(routeCtx, endpoint.ID, event.MessageID, pub)
		stop()
		if routeErr != nil {
			return
		}
	}
}

func (m *Manager) publishExternal(ctx context.Context, sourceID, messageID string, p protocol.Publish) (protocol.Accepted, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ack := protocol.Accepted{}
	if m.closed || m.onebot[sourceID] == nil {
		return ack, errors.New("OneBot session is no longer active")
	}
	routes, err := m.store.ResolveRoutes(ctx, sourceID, p.ChannelID)
	if err != nil {
		return ack, err
	}
	for _, route := range routes {
		text, matched, applyErr := route.Rules.Apply(pipeline.Context{Text: p.Text, SenderID: p.SenderID, SenderName: p.SenderName, SourceEndpointID: sourceID, TargetEndpointID: route.Binding.TargetEndpointID})
		if applyErr != nil || len([]rune(text)) > 2048 {
			ack.Dropped++
			continue
		}
		if !matched {
			ack.Filtered++
			continue
		}
		delivery := protocol.Delivery{BindingID: route.Binding.ID, SourceEndpointID: sourceID, SourceMessageID: messageID, SourceChannelID: p.ChannelID, ChannelID: route.Binding.TargetChannel, SenderID: p.SenderID, SenderName: p.SenderName, Text: text}
		if target := m.sessions[route.Binding.TargetEndpointID]; target != nil && slices.Contains(target.Capabilities, protocol.ReceiveCapability) {
			if enqueue(target, outgoing{kind: protocol.DeliverType, payload: delivery}) {
				ack.Queued++
			} else {
				ack.Dropped++
			}
			continue
		}
		if target := m.onebot[route.Binding.TargetEndpointID]; target != nil {
			if action, ok := target.prepareAction(route.Binding.TargetChannel, text); ok && m.trackOneBotAction(action, route.Binding.TargetEndpointID) {
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
