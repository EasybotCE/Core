// Package events handles adapter and bridge events through application use cases.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"

	"easybot/internal/adapter/onebot11"
	"easybot/internal/bridge"
	"easybot/internal/model"
)

type AdapterRecords interface {
	Get(context.Context, string, string) (model.Record, error)
}

type BindingConfirmer interface {
	Confirm(context.Context, string, string, string, string, string) (model.Binding, error)
}

type BindingJoiner interface {
	PlayerJoin(context.Context, model.BindingPlayerJoin) (model.BindingJoinResult, error)
}

type BindingStarter interface {
	StartBind(context.Context, string, string, string) (model.BindingChallenge, error)
}

type SocialAccountLookup interface {
	GetSocialAccount(context.Context, string) (model.Binding, error)
}

type MessageSender interface {
	Send(context.Context, string, string, string, string) (string, error)
}

type Router interface {
	OnSocialEvent(eventID, adapterID, groupID, senderName, content string)
	OnMinecraftChat(string, bridge.SyncMessagePacket)
}

// Service connects runtime callbacks to existing application use cases.
type Service struct {
	ctx           context.Context
	records       AdapterRecords
	bindings      BindingConfirmer
	sender        MessageSender
	router        Router
	adapterLogger *slog.Logger
	bridgeLogger  *slog.Logger
}

// New creates a handler without starting any event producer.
func New(ctx context.Context, records AdapterRecords, bindings BindingConfirmer, adapterLogger, bridgeLogger *slog.Logger) *Service {
	return &Service{ctx: ctx, records: records, bindings: bindings, adapterLogger: adapterLogger, bridgeLogger: bridgeLogger}
}

// Connect injects the runtimes after construction. Call once before starting any
// adapters or listeners; callbacks must not run concurrently with Connect.
func (h *Service) Connect(sender MessageSender, router Router) {
	h.sender = sender
	h.router = router
}

func (h *Service) OnSocialEvent(id string, event onebot11.Event) {
	h.adapterLogger.Info("适配器事件", "event", event.Type, "adapter", id)
	if event.Type != "message" || event.MessageType != "group" {
		return
	}
	if event.SelfID != "" && event.SenderID == event.SelfID {
		return
	}
	fields := strings.Fields(event.Content)
	if len(fields) == 2 && (fields[0] == "绑定" || strings.EqualFold(fields[0], "bind")) {
		platformType := "onebot11"
		if adapter, err := h.records.Get(h.ctx, "adapters", id); err == nil {
			var value struct {
				AdapterType string `json:"adapterType"`
			}
			if json.Unmarshal([]byte(adapter.Detail), &value) == nil && value.AdapterType != "" {
				platformType = value.AdapterType
			}
		}
		_, bindErr := h.bindings.Confirm(h.ctx, fields[1], platformType, event.SenderID, event.SenderName, event.PeerID)
		message := "绑定失败：验证码无效、已过期或账号冲突。"
		if bindErr == nil {
			message = "绑定成功。"
		}
		_, _ = h.sender.Send(context.Background(), id, "group", event.PeerID, message)
		return
	}
	if h.router != nil {
		h.router.OnSocialEvent(event.ID, id, event.PeerID, event.SenderName, event.Content)
	}
}

func (h *Service) BridgeCallbacks() bridge.Callbacks {
	return bridge.Callbacks{
		OnStartBind: func(serverID, playerName string) (model.BindingChallenge, error) {
			starter, ok := h.bindings.(BindingStarter)
			if !ok {
				return model.BindingChallenge{}, errors.New("binding start use case unavailable")
			}
			return starter.StartBind(h.ctx, "", playerName, serverID)
		},
		OnGetSocialAccount: func(_ string, playerName string) (model.Binding, error) {
			lookup, ok := h.bindings.(SocialAccountLookup)
			if !ok {
				return model.Binding{}, errors.New("social account lookup unavailable")
			}
			return lookup.GetSocialAccount(h.ctx, playerName)
		},
		OnPlayerJoin: func(serverID string, packet bridge.SyncMessagePacket, requireBinding bool, kickMessage string) (model.BindingJoinResult, error) {
			joiner, ok := h.bindings.(BindingJoiner)
			if !ok {
				return model.BindingJoinResult{}, nil
			}
			return joiner.PlayerJoin(h.ctx, model.BindingPlayerJoin{PlayerUUID: packet.Player.PlayerUUID, PlayerName: packet.Player.PlayerName, ServerID: serverID, RequireBinding: requireBinding, KickMessage: kickMessage})
		},
		OnChat: func(serverID string, packet bridge.SyncMessagePacket) {
			h.bridgeLogger.Info("收到 Minecraft 聊天", "player", packet.Player.PlayerName, "server", serverID)
			if h.router != nil {
				h.router.OnMinecraftChat(serverID, packet)
			}
		},
		OnEnterExit: func(serverID string, packet bridge.SyncEnterExitMessagePacket) {
			state := "left"
			if packet.IsEnter {
				state = "joined"
			}
			h.bridgeLogger.Info("玩家状态变化", "player", packet.Player.PlayerName, "state", state, "server", serverID)
		},
		OnDeath: func(serverID string, packet bridge.SyncDeathMessagePacket) {
			h.bridgeLogger.Info("玩家死亡", "server", serverID, "detail", packet.Raw)
		},
		OnState: func(serverID string, online bool) {
			if online {
				h.bridgeLogger.Info("Minecraft 服务器已连接", "server", serverID)
				return
			}
			h.bridgeLogger.Warn("Minecraft 服务器已断开", "server", serverID)
		},
	}
}
