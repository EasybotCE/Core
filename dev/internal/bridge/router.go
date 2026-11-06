package bridge

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"easybot/internal/store"
)

// SocialSender is the subset of the OneBot 11 manager the router needs. It is
// declared here so the bridge package does not depend on a concrete adapter.
type SocialSender interface {
	Send(ctx context.Context, adapterID, messageType, peerID, message string) (string, error)
}

// Route is one "Minecraft server <-> social group" sync rule, stored in the
// records table with kind "sync-rules" and this shape in detail:
//
//	{"serverId":"...","adapterId":"...","groupId":"...","direction":"both",
//	 "mcFormat":"[{server}] {player}: {content}","socialFormat":"[{group}] {sender}: {content}"}
type Route struct {
	ServerID     string `json:"serverId"`
	AdapterID    string `json:"adapterId"`
	GroupID      string `json:"groupId"`
	Direction    string `json:"direction"`
	MinecraftFmt string `json:"mcFormat"`
	SocialFmt    string `json:"socialFormat"`
}

const (
	DirectionBoth      = "both"
	DirectionMCToChat  = "mc2social"
	DirectionChatToMC  = "social2mc"
	defaultMinecraft   = "[{server}] {player}: {content}"
	defaultSocial      = "[{group}] {sender}: {content}"
	echoWindow         = 5 * time.Second
)

// Router moves chat between connected Minecraft servers and OneBot 11 groups.
// It also suppresses the echo a two-way rule would otherwise create.
type Router struct {
	db     *store.Store
	bridge *Manager
	social SocialSender
	logger *slog.Logger

	mu     sync.Mutex
	recent map[string]time.Time
}

func NewRouter(db *store.Store, bridgeManager *Manager, social SocialSender, logger *slog.Logger) *Router {
	return &Router{db: db, bridge: bridgeManager, social: social, logger: logger, recent: make(map[string]time.Time)}
}

// OnMinecraftChat forwards a plugin chat line to every matching social group.
func (r *Router) OnMinecraftChat(serverID string, packet SyncMessagePacket) {
	content := strings.TrimSpace(packet.Message)
	if content == "" {
		return
	}
	if r.consumeEcho(serverID, content) {
		return
	}
	serverName := r.serverName(serverID)
	for _, route := range r.routes() {
		if route.ServerID != serverID || !route.allowsMCToSocial() {
			continue
		}
		text := render(route.MinecraftFmt, defaultMinecraft, map[string]string{
			"server":  serverName,
			"player":  packet.Player.PlayerName,
			"content": content,
		})
		if _, err := r.social.Send(context.Background(), route.AdapterID, "group", route.GroupID, text); err != nil {
			r.logger.Warn("forward minecraft chat to social failed",
				"server_id", serverID, "adapter_id", route.AdapterID, "group", route.GroupID, "error", err)
		}
	}
}

// OnSocialMessage forwards a group message from OneBot 11 into the matching
// Minecraft servers.
func (r *Router) OnSocialMessage(adapterID, groupID, senderName, content string) {
	content = strings.TrimSpace(content)
	if content == "" {
		return
	}
	for _, route := range r.routes() {
		if route.AdapterID != adapterID || route.GroupID != groupID || !route.allowsSocialToMC() {
			continue
		}
		text := render(route.SocialFmt, defaultSocial, map[string]string{
			"group":   groupID,
			"sender":  senderName,
			"content": content,
		})
		r.markEcho(route.ServerID, text)
		if err := r.bridge.SendToChat(route.ServerID, text); err != nil {
			r.logger.Warn("forward social message to minecraft failed",
				"server_id", route.ServerID, "adapter_id", adapterID, "error", err)
		}
	}
}

// routes loads enabled sync rules. Configuration errors are skipped instead of
// guessing a target.
func (r *Router) routes() []Route {
	records, err := r.db.List(context.Background(), "sync-rules")
	if err != nil {
		r.logger.Warn("sync rules unavailable", "error", err)
		return nil
	}
	routes := make([]Route, 0, len(records))
	for _, record := range records {
		if !record.Enabled {
			continue
		}
		var route Route
		if json.Unmarshal([]byte(record.Detail), &route) != nil {
			continue
		}
		if route.ServerID == "" || route.AdapterID == "" || route.GroupID == "" {
			continue
		}
		if route.Direction == "" {
			route.Direction = DirectionBoth
		}
		routes = append(routes, route)
	}
	return routes
}

func (r *Router) serverName(serverID string) string {
	status, err := r.bridge.Status(serverID)
	if err != nil || status.ServerName == "" {
		return "Minecraft"
	}
	return status.ServerName
}

func (route Route) allowsMCToSocial() bool {
	return route.Direction == DirectionBoth || route.Direction == DirectionMCToChat
}

func (route Route) allowsSocialToMC() bool {
	return route.Direction == DirectionBoth || route.Direction == DirectionChatToMC
}

// markEcho remembers a line the host just pushed into Minecraft, so a plugin
// that reports it back as a chat event does not bounce it into the group.
func (r *Router) markEcho(serverID, text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	for key, at := range r.recent {
		if now.Sub(at) > echoWindow {
			delete(r.recent, key)
		}
	}
	r.recent[serverID+"\x00"+text] = now
}

func (r *Router) consumeEcho(serverID, text string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := serverID + "\x00" + text
	at, ok := r.recent[key]
	if !ok {
		return false
	}
	delete(r.recent, key)
	return time.Since(at) <= echoWindow
}

// render substitutes the supported variables and leaves unknown braces alone so
// a malformed template cannot inject content or panic.
func render(template, fallback string, values map[string]string) string {
	if strings.TrimSpace(template) == "" {
		template = fallback
	}
	out := template
	for key, value := range values {
		out = strings.ReplaceAll(out, "{"+key+"}", value)
	}
	return strings.TrimSpace(out)
}
