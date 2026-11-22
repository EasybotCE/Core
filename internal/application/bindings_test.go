package application

import (
	"context"
	"testing"
	"time"

	"easybot/internal/model"
)

type bindingRepositoryStub struct {
	lifetime    time.Duration
	maxAttempts int
	playerUUID  string
	playerName  string
	serverID    string
	challenge   model.BindingChallenge
}

func (s *bindingRepositoryStub) CreateBindingChallenge(_ context.Context, _, _, _ string, lifetime time.Duration, maxAttempts int) (model.BindingChallenge, error) {
	s.lifetime, s.maxAttempts = lifetime, maxAttempts
	return model.BindingChallenge{}, nil
}
func (s *bindingRepositoryStub) StartBinding(_ context.Context, playerUUID, playerName, serverID string, lifetime time.Duration, maxAttempts int) (model.BindingChallenge, error) {
	s.lifetime, s.maxAttempts = lifetime, maxAttempts
	s.playerUUID, s.playerName, s.serverID = playerUUID, playerName, serverID
	if s.challenge.Code == "" {
		s.challenge = model.BindingChallenge{Code: "ABCD", ExpiresAt: time.Now().Add(time.Minute)}
	}
	return s.challenge, nil
}
func (*bindingRepositoryStub) IsPlayerBound(context.Context, string) (bool, error) { return false, nil }
func (*bindingRepositoryStub) ConfirmBinding(context.Context, string, string, string, string, string) (model.Binding, error) {
	return model.Binding{}, nil
}
func (*bindingRepositoryStub) ListBindings(context.Context) ([]model.Binding, error) { return nil, nil }
func (*bindingRepositoryStub) DeleteBinding(context.Context, string) error           { return nil }
func (*bindingRepositoryStub) BindingAudit(context.Context, int) ([]map[string]any, error) {
	return nil, nil
}

func TestCreateChallengeDefaults(t *testing.T) {
	repository := &bindingRepositoryStub{}
	service := NewBindings(repository)
	if _, err := service.CreateChallenge(context.Background(), "player", "", "", 0, 0); err != nil {
		t.Fatal(err)
	}
	if repository.lifetime != 5*time.Minute || repository.maxAttempts != 5 {
		t.Fatalf("defaults = (%s, %d), want (5m, 5)", repository.lifetime, repository.maxAttempts)
	}
}

func TestPlayerJoinUsesPluginPlayerNameIdentity(t *testing.T) {
	repository := &bindingRepositoryStub{}
	service := NewBindings(repository)
	result, err := service.PlayerJoin(context.Background(), model.BindingPlayerJoin{
		PlayerUUID: "minecraft-uuid", PlayerName: "Steve", ServerID: "server-1", RequireBinding: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Kick || result.Challenge == nil || result.Challenge.Code != "ABCD" || result.KickMessage == "" {
		t.Fatalf("join result = %+v", result)
	}
	if repository.playerUUID != "" || repository.playerName != "Steve" || repository.serverID != "server-1" {
		t.Fatalf("challenge identity = uuid:%q name:%q server:%q", repository.playerUUID, repository.playerName, repository.serverID)
	}
}

func TestStartBindNormalizesToPlayerNameIdentity(t *testing.T) {
	repository := &bindingRepositoryStub{}
	service := NewBindings(repository)
	if _, err := service.StartBind(context.Background(), "minecraft-uuid", "Steve", "server-1"); err != nil {
		t.Fatal(err)
	}
	if repository.playerUUID != "" || repository.playerName != "Steve" {
		t.Fatalf("start bind identity = uuid:%q name:%q", repository.playerUUID, repository.playerName)
	}
}
