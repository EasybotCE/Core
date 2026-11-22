package application

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"easybot/internal/model"
)

type BindingRepository interface {
	CreateBindingChallenge(context.Context, string, string, string, time.Duration, int) (model.BindingChallenge, error)
	StartBinding(context.Context, string, string, string, time.Duration, int) (model.BindingChallenge, error)
	ConfirmBinding(context.Context, string, string, string, string, string) (model.Binding, error)
	IsPlayerBound(context.Context, string) (bool, error)
	ListBindings(context.Context) ([]model.Binding, error)
	DeleteBinding(context.Context, string) error
	BindingAudit(context.Context, int) ([]map[string]any, error)
}

var (
	ErrBindingConflict                   = model.ErrBindingConflict
	ErrBindingNotFound                   = model.ErrBindingNotFound
	ErrChallengeInvalid                  = model.ErrChallengeInvalid
	ErrCrossPlatformConfirmationRequired = model.ErrCrossPlatformConfirmationRequired
	ErrBindingLimit                      = model.ErrBindingLimit
)

type Bindings struct{ repository BindingRepository }

func NewBindings(repository BindingRepository) *Bindings { return &Bindings{repository: repository} }

func (s *Bindings) CreateChallenge(ctx context.Context, playerUUID, serverID, groupID string, lifetime time.Duration, maxAttempts int) (model.BindingChallenge, error) {
	if lifetime == 0 {
		lifetime = 5 * time.Minute
	}
	if maxAttempts == 0 {
		maxAttempts = 5
	}
	return s.repository.CreateBindingChallenge(ctx, playerUUID, serverID, groupID, lifetime, maxAttempts)
}
func (s *Bindings) StartBind(ctx context.Context, playerUUID, playerName, serverID string) (model.BindingChallenge, error) {
	if strings.TrimSpace(playerName) != "" {
		playerUUID = ""
	}
	return s.repository.StartBinding(ctx, playerUUID, playerName, serverID, 5*time.Minute, 5)
}
func (s *Bindings) PlayerJoin(ctx context.Context, event model.BindingPlayerJoin) (model.BindingJoinResult, error) {
	if !event.RequireBinding {
		return model.BindingJoinResult{}, nil
	}
	identity := strings.TrimSpace(event.PlayerUUID)
	if strings.TrimSpace(event.PlayerName) != "" {
		identity = "name:" + strings.ToLower(strings.TrimSpace(event.PlayerName))
	}
	if identity == "" {
		return model.BindingJoinResult{}, errors.New("player join omitted both UUID and name")
	}
	bound, err := s.repository.IsPlayerBound(ctx, identity)
	if err != nil {
		return model.BindingJoinResult{}, err
	}
	if bound {
		return model.BindingJoinResult{}, nil
	}
	challenge, err := s.StartBind(ctx, "", event.PlayerName, event.ServerID)
	if err != nil {
		return model.BindingJoinResult{}, err
	}
	message := event.KickMessage
	if message == "" {
		message = "\u8bf7\u5148\u7ed1\u5b9a\u8d26\u53f7\uff1a\u5728\u7fa4\u5185\u53d1\u9001 \u7ed1\u5b9a " + challenge.Code
	}
	message = strings.ReplaceAll(message, "#code", challenge.Code)
	message = strings.ReplaceAll(message, "#time", time.Until(challenge.ExpiresAt).Round(time.Second).String())
	if !strings.Contains(event.KickMessage, "#code") && event.KickMessage != "" {
		message += "\uff08\u7fa4\u5185\u53d1\u9001\uff1a\u7ed1\u5b9a " + challenge.Code + "\uff09"
	}
	return model.BindingJoinResult{Kick: true, KickMessage: message, Challenge: &challenge}, nil
}

var bindMessage = regexp.MustCompile(`(?i)^\x{7ED1}\x{5B9A}\s*([A-Z0-9]{4,32})$`)

func (s *Bindings) HandleGroupMessage(ctx context.Context, event model.BindingGroupMessage) (model.BindingMessageResult, error) {
	match := bindMessage.FindStringSubmatch(strings.TrimSpace(event.Text))
	if match == nil {
		return model.BindingMessageResult{}, nil
	}
	if strings.TrimSpace(event.PlatformType) == "" || strings.TrimSpace(event.AccountID) == "" {
		return model.BindingMessageResult{Handled: true, Message: "\u7ed1\u5b9a\u5931\u8d25\uff1a\u65e0\u6cd5\u8bc6\u522b\u5e73\u53f0\u8d26\u53f7\u3002"}, nil
	}
	binding, err := s.repository.ConfirmBinding(ctx, match[1], event.PlatformType, event.AccountID, event.DisplayName, event.GroupID)
	if err != nil {
		if errors.Is(err, model.ErrCrossPlatformConfirmationRequired) {
			return model.BindingMessageResult{Handled: true, Message: "\u8be5\u73a9\u5bb6\u5df2\u7ed1\u5b9a\u5176\u4ed6\u5e73\u53f0\uff0c\u8bf7\u4f7f\u7528\u539f\u7ed1\u5b9a\u5e73\u53f0\u5b8c\u6210\u8de8\u5e73\u53f0\u786e\u8ba4\u3002"}, nil
		}
		if errors.Is(err, model.ErrBindingLimit) {
			return model.BindingMessageResult{Handled: true, Message: "\u8be5\u5e73\u53f0\u8d26\u53f7\u5df2\u8fbe\u5230\u53ef\u7ed1\u5b9a\u6e38\u620f\u73a9\u5bb6\u6570\u91cf\u4e0a\u9650\u3002"}, nil
		}
		return model.BindingMessageResult{Handled: true, Message: "\u7ed1\u5b9a\u5931\u8d25\uff1a\u9a8c\u8bc1\u7801\u65e0\u6548\u3001\u5df2\u8fc7\u671f\u6216\u8d26\u53f7\u5df2\u7ed1\u5b9a\u3002"}, nil
	}
	return model.BindingMessageResult{Handled: true, Message: "\u7ed1\u5b9a\u6210\u529f\u3002", Binding: &binding}, nil
}
func (s *Bindings) Confirm(ctx context.Context, code, platformType, accountID, displayName, groupID string) (model.Binding, error) {
	return s.repository.ConfirmBinding(ctx, code, platformType, accountID, displayName, groupID)
}
func (s *Bindings) GetSocialAccount(ctx context.Context, playerName string) (model.Binding, error) {
	identity := "name:" + strings.ToLower(strings.TrimSpace(playerName))
	items, err := s.repository.ListBindings(ctx)
	if err != nil {
		return model.Binding{}, err
	}
	for _, item := range items {
		if strings.EqualFold(item.PlayerUUID, identity) || strings.EqualFold(item.PlayerName, strings.TrimSpace(playerName)) {
			return item, nil
		}
	}
	return model.Binding{}, model.ErrBindingNotFound
}

func (s *Bindings) List(ctx context.Context) ([]model.Binding, error) {
	return s.repository.ListBindings(ctx)
}
func (s *Bindings) Delete(ctx context.Context, id string) error {
	return s.repository.DeleteBinding(ctx, id)
}
func (s *Bindings) Audit(ctx context.Context, limit int) ([]map[string]any, error) {
	return s.repository.BindingAudit(ctx, limit)
}
