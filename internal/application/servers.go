package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"strings"

	"easybot/internal/bridge"
)

var (
	ErrBridgeRuntimeUnavailable = errors.New("Minecraft bridge runtime is unavailable.")
	ErrServerTokenGeneration    = errors.New("could not generate a server token")
)

// BridgeRuntime 是服务器用例需要的连接管理与发送端口。
type BridgeRuntime interface {
	Statuses() (map[string]bridge.Status, error)
	Status(string) (bridge.Status, error)
	SendToChat(string, string) error
	DisconnectServer(string)
}

type Servers struct {
	repository RecordRepository
	runtime    BridgeRuntime
	logger     *slog.Logger
}

func NewServers(repository RecordRepository, runtime BridgeRuntime, logger *slog.Logger) *Servers {
	return &Servers{repository: repository, runtime: runtime, logger: logger}
}
func (s *Servers) Available() bool { return s.runtime != nil }

func (s *Servers) Statuses() ([]bridge.Status, error) {
	if !s.Available() {
		return nil, ErrBridgeRuntimeUnavailable
	}
	statuses, err := s.runtime.Statuses()
	if err != nil {
		return nil, err
	}
	items := make([]bridge.Status, 0, len(statuses))
	for _, status := range statuses {
		items = append(items, status)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ServerName < items[j].ServerName })
	return items, nil
}
func (s *Servers) Status(id string) (bridge.Status, error) {
	if !s.Available() {
		return bridge.Status{}, ErrBridgeRuntimeUnavailable
	}
	return s.runtime.Status(id)
}
func (s *Servers) Send(id, text string) error {
	if !s.Available() {
		return ErrBridgeRuntimeUnavailable
	}
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 2000 {
		return &ValidationError{Message: "text must be 1-2000 bytes"}
	}
	return s.runtime.SendToChat(id, text)
}

type ServerToken struct {
	ServerID string `json:"serverId"`
	Token    string `json:"token"`
}

func (s *Servers) RotateToken(ctx context.Context, id string) (ServerToken, error) {
	record, err := s.repository.Get(ctx, "servers", id)
	if err != nil {
		return ServerToken{}, err
	}
	token, err := newServerToken()
	if err != nil {
		return ServerToken{}, ErrServerTokenGeneration
	}
	detail, err := setServerToken(record.Detail, token)
	if err != nil {
		return ServerToken{}, &ValidationError{Message: "stored server config is invalid JSON"}
	}
	record.Detail = detail
	if _, err := s.repository.Save(ctx, record); err != nil {
		return ServerToken{}, err
	}
	// 只有新令牌成功保存后才关闭旧连接，防止存储失败导致有效会话被误踢出。
	if s.runtime != nil {
		s.runtime.DisconnectServer(record.ID)
	}
	if s.logger != nil {
		s.logger.Warn("Minecraft server token rotated, active session closed: " + record.Name)
	}
	return ServerToken{ServerID: record.ID, Token: token}, nil
}

func newServerToken() (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func setServerToken(detail, token string) (string, error) {
	values := make(map[string]json.RawMessage)
	if strings.TrimSpace(detail) != "" {
		if err := json.Unmarshal([]byte(detail), &values); err != nil {
			return detail, err
		}
	}
	if values == nil {
		values = make(map[string]json.RawMessage)
	}
	encoded, err := json.Marshal(token)
	if err != nil {
		return detail, err
	}
	values["token"] = encoded
	result, err := json.Marshal(values)
	if err != nil {
		return detail, err
	}
	return string(result), nil
}

func serverToken(detail string) string {
	var values map[string]json.RawMessage
	if json.Unmarshal([]byte(detail), &values) != nil {
		return ""
	}
	for _, key := range []string{"token", "Token", "serverToken"} {
		raw, ok := values[key]
		if !ok {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil && value != "" {
			return value
		}
	}
	return ""
}

func ensureServerToken(detail string) (string, error) {
	if serverToken(detail) != "" {
		return detail, nil
	}
	token, err := newServerToken()
	if err != nil {
		return detail, err
	}
	return setServerToken(detail, token)
}
