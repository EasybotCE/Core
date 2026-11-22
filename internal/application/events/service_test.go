package events

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"testing"

	"easybot/internal/adapter/onebot11"
	"easybot/internal/bridge"
	"easybot/internal/model"
)

type recordsStub struct {
	detail  string
	err     error
	lookups [][2]string
}

func (r *recordsStub) Get(_ context.Context, kind, id string) (model.Record, error) {
	r.lookups = append(r.lookups, [2]string{kind, id})
	return model.Record{Detail: r.detail}, r.err
}

type bindingsStub struct {
	calls      [][]string
	err        error
	joined     *model.BindingPlayerJoin
	joinResult model.BindingJoinResult
}

func (b *bindingsStub) Confirm(_ context.Context, code, platform, account, name, group string) (model.Binding, error) {
	b.calls = append(b.calls, []string{code, platform, account, name, group})
	return model.Binding{}, b.err
}
func (b *bindingsStub) PlayerJoin(_ context.Context, event model.BindingPlayerJoin) (model.BindingJoinResult, error) {
	b.joined = &event
	return b.joinResult, b.err
}

type senderStub struct{ calls [][]string }

func (s *senderStub) Send(_ context.Context, adapter, kind, peer, message string) (string, error) {
	s.calls = append(s.calls, []string{adapter, kind, peer, message})
	return "reply", nil
}

type routerStub struct {
	social [][]string
	server string
	chat   []bridge.SyncMessagePacket
}

func (r *routerStub) OnSocialEvent(eventID, adapter, group, name, content string) {
	r.social = append(r.social, []string{eventID, adapter, group, name, content})
}
func (r *routerStub) OnMinecraftChat(server string, packet bridge.SyncMessagePacket) {
	r.server = server
	r.chat = append(r.chat, packet)
}

func newService(t *testing.T) (*Service, *recordsStub, *bindingsStub, *senderStub, *routerStub) {
	t.Helper()
	records := &recordsStub{}
	bindings := &bindingsStub{}
	sender := &senderStub{}
	router := &routerStub{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := New(context.Background(), records, bindings, logger, logger)
	service.Connect(sender, router)
	return service, records, bindings, sender, router
}

func groupEvent(content string) onebot11.Event {
	return onebot11.Event{Type: "message", MessageType: "group", ID: "event1", SelfID: "bot", SenderID: "player", SenderName: "玩家", PeerID: "group1", Content: content}
}

func TestBindingCommandKeepsExistingContract(t *testing.T) {
	tests := []struct {
		name, command, detail, platform string
		lookupErr, bindErr              error
	}{
		{name: "Chinese", command: "绑定 123456", detail: `{"adapterType":"qq"}`, platform: "qq"},
		{name: "English case and spaces", command: "  BiNd   123456  ", detail: `{"adapterType":"onebot11"}`, platform: "onebot11"},
		{name: "unknown adapter", command: "bind 123456", platform: "onebot11", lookupErr: errors.New("not found")},
		{name: "invalid adapter detail", command: "bind 123456", detail: "invalid JSON", platform: "onebot11"},
		{name: "rejected binding", command: "bind 123456", detail: "{}", platform: "onebot11", bindErr: errors.New("expired")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, records, bindings, sender, router := newService(t)
			records.detail, records.err, bindings.err = tt.detail, tt.lookupErr, tt.bindErr
			service.OnSocialEvent("adapter1", groupEvent(tt.command))
			want := [][]string{{"123456", tt.platform, "player", "玩家", "group1"}}
			if !reflect.DeepEqual(bindings.calls, want) {
				t.Fatalf("confirmation = %v, want %v", bindings.calls, want)
			}
			message := "绑定成功。"
			if tt.bindErr != nil {
				message = "绑定失败：验证码无效、已过期或账号冲突。"
			}
			if !reflect.DeepEqual(sender.calls, [][]string{{"adapter1", "group", "group1", message}}) {
				t.Fatalf("reply = %v", sender.calls)
			}
			if !reflect.DeepEqual(records.lookups, [][2]string{{"adapters", "adapter1"}}) || len(router.social) != 0 {
				t.Fatalf("lookups = %v; unexpected forwarded command = %v", records.lookups, router.social)
			}
		})
	}
}

func TestIgnoreNonGroupAndSelfEvents(t *testing.T) {
	events := []onebot11.Event{groupEvent("绑定 123456"), groupEvent("绑定 123456"), groupEvent("绑定 123456")}
	events[0].Type = "notice"
	events[1].MessageType = "private"
	events[2].SenderID = events[2].SelfID
	for _, event := range events {
		service, records, bindings, sender, router := newService(t)
		service.OnSocialEvent("adapter1", event)
		if len(records.lookups)+len(bindings.calls)+len(sender.calls)+len(router.social) != 0 {
			t.Fatalf("ignored event triggered use cases: %+v", event)
		}
	}
}

func TestForwardOrdinarySocialEvents(t *testing.T) {
	for _, content := range []string{"hello", "bind", "bind 123 extra", "绑定"} {
		service, records, bindings, sender, router := newService(t)
		service.OnSocialEvent("adapter1", groupEvent(content))
		want := [][]string{{"event1", "adapter1", "group1", "玩家", content}}
		if !reflect.DeepEqual(router.social, want) || len(records.lookups)+len(bindings.calls)+len(sender.calls) != 0 {
			t.Fatalf("content %q: forwarded = %v", content, router.social)
		}
	}
}

func TestBridgeCallbacks(t *testing.T) {
	service, _, _, _, router := newService(t)
	callbacks := service.BridgeCallbacks()
	packet := bridge.SyncMessagePacket{Player: bridge.PlayerInfo{PlayerName: "Steve"}}
	callbacks.OnChat("server1", packet)
	if router.server != "server1" || !reflect.DeepEqual(router.chat, []bridge.SyncMessagePacket{packet}) {
		t.Fatalf("Minecraft chat lost routing context: %+v", router)
	}
	callbacks.OnEnterExit("server1", bridge.SyncEnterExitMessagePacket{IsEnter: true})
	callbacks.OnEnterExit("server1", bridge.SyncEnterExitMessagePacket{})
	callbacks.OnDeath("server1", bridge.SyncDeathMessagePacket{})
	callbacks.OnState("server1", true)
	callbacks.OnState("server1", false)
	if len(router.chat) != 1 || len(router.social) != 0 {
		t.Fatalf("non-chat callbacks unexpectedly routed events: %+v", router)
	}
}

func TestBridgePlayerJoinUsesBindingAdmission(t *testing.T) {
	service, _, bindings, _, _ := newService(t)
	bindings.joinResult = model.BindingJoinResult{Kick: true, KickMessage: "绑定"}
	callback := service.BridgeCallbacks().OnPlayerJoin
	got, err := callback("server-1", bridge.SyncMessagePacket{Player: bridge.PlayerInfo{PlayerUUID: "uuid-1", PlayerName: "Steve"}}, true, "#code")
	if err != nil || !got.Kick || got.KickMessage != "绑定" {
		t.Fatalf("join result = %+v, %v", got, err)
	}
	if bindings.joined == nil || bindings.joined.PlayerUUID != "uuid-1" || bindings.joined.PlayerName != "Steve" || bindings.joined.ServerID != "server-1" || !bindings.joined.RequireBinding || bindings.joined.KickMessage != "#code" {
		t.Fatalf("join event = %+v", bindings.joined)
	}
}
