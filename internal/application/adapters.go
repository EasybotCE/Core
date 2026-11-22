package application

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"easybot/internal/adapter/onebot11"
	"easybot/internal/adapter/qqbot"
)

var (
	ErrUnsupportedAdapter        = errors.New("当前主程序不支持该适配器")
	ErrAdapterRuntimeUnavailable = errors.New("Adapter runtime is unavailable.")
)

// AdapterRuntime 是适配器用例所需的运行时端口，不负责持久化配置。
type AdapterRuntime interface {
	Start(context.Context, string) error
	Stop(context.Context, string) error
	Restart(context.Context, string) error
	Status(string) (onebot11.Status, error)
	Test(context.Context, string) (map[string]any, error)
	Capabilities(context.Context, string) ([]string, error)
	Send(context.Context, string, string, string, string) (string, error)
	Groups(context.Context, string) ([]map[string]any, error)
}

type Adapters struct {
	repository RecordRepository
	runtime    AdapterRuntime
	logger     *slog.Logger
}

func NewAdapters(repository RecordRepository, runtime AdapterRuntime, logger *slog.Logger) *Adapters {
	return &Adapters{repository: repository, runtime: runtime, logger: logger}
}

func (s *Adapters) Available() bool { return s.runtime != nil }

func (s *Adapters) Types() []map[string]string {
	return []map[string]string{
		{"adapterType": "Onebot11", "displayName": "OneBot 11", "platformName": "onebot11"},
		{"adapterType": "QQBot", "displayName": "QQ 官方 Bot", "platformName": "qqbot"},
	}
}

func (s *Adapters) List(ctx context.Context) ([]Record, error) {
	items, err := s.repository.List(ctx, "adapters")
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i] = publicAdapterRecord(items[i])
	}
	return items, nil
}

func (s *Adapters) Create(ctx context.Context, adapterType, displayName, configJSON string) (Record, error) {
	if adapterType != "Onebot11" && adapterType != "QQBot" {
		return Record{}, ErrUnsupportedAdapter
	}
	if displayName == "" {
		displayName = adapterType
	}
	if configJSON == "" {
		configJSON = "{}"
	}
	config, err := parseAdapterJSON(configJSON)
	if err != nil {
		return Record{}, err
	}
	if err := validateAdapterConfig(adapterType, config); err != nil {
		return Record{}, err
	}
	detail, _ := json.Marshal(map[string]any{"adapterType": adapterType, "config": config})
	item, err := s.repository.Save(ctx, Record{Kind: "adapters", Name: displayName, Detail: string(detail), Enabled: true})
	if err != nil {
		return Record{}, err
	}
	return publicAdapterRecord(item), nil
}

func (s *Adapters) Update(ctx context.Context, id, displayName, configJSON string) (Record, error) {
	old, err := s.repository.Get(ctx, "adapters", id)
	if err != nil {
		return Record{}, err
	}
	var oldDetail struct {
		AdapterType string          `json:"adapterType"`
		Config      json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal([]byte(old.Detail), &oldDetail); err != nil {
		return Record{}, &ValidationError{Message: "stored adapter config is invalid"}
	}
	config, err := parseAdapterJSON(configJSON)
	if err != nil {
		return Record{}, err
	}
	config = preserveAccessToken(oldDetail.Config, config)
	if oldDetail.AdapterType == "QQBot" {
		config = preserveSecret(oldDetail.Config, config, "AppSecret", "appSecretConfigured")
	}
	if err := validateAdapterConfig(oldDetail.AdapterType, config); err != nil {
		return Record{}, err
	}
	detail, _ := json.Marshal(map[string]any{"adapterType": oldDetail.AdapterType, "config": config})
	item, err := s.repository.Save(ctx, Record{ID: old.ID, Kind: "adapters", Name: displayName, Detail: string(detail), Enabled: old.Enabled})
	if err != nil {
		return Record{}, err
	}
	// 配置已持久化后重启失败不回滚；保留原有成功响应并记录运行时故障。
	if s.runtime != nil {
		if status, err := s.runtime.Status(item.ID); err == nil && status.State != onebot11.StateStopped {
			if err := s.runtime.Restart(ctx, item.ID); err != nil && s.logger != nil {
				s.logger.Warn("适配器 "+item.ID+" 更新配置后重启失败", "error", err)
			}
		}
	}
	return publicAdapterRecord(item), nil
}

func parseAdapterJSON(value string) (json.RawMessage, error) {
	var config json.RawMessage
	if !json.Valid([]byte(value)) || json.Unmarshal([]byte(value), &config) != nil {
		return nil, &ValidationError{Message: "adapter config must be valid JSON"}
	}
	return config, nil
}

func validateAdapterConfig(adapterType string, config json.RawMessage) error {
	switch adapterType {
	case "Onebot11":
		_, err := onebot11.ParseConfig(config)
		return err
	case "QQBot":
		_, err := qqbot.ParseQQConfig(config)
		return err
	default:
		return nil // 保留旧记录更新的兼容行为。
	}
}

func (s *Adapters) Start(ctx context.Context, id string) (onebot11.Status, error) {
	if !s.Available() {
		return onebot11.Status{}, ErrAdapterRuntimeUnavailable
	}
	if err := s.runtime.Start(ctx, id); err != nil {
		return onebot11.Status{}, err
	}
	status, _ := s.runtime.Status(id)
	return status, nil
}
func (s *Adapters) Stop(ctx context.Context, id string) (onebot11.Status, error) {
	if !s.Available() {
		return onebot11.Status{}, ErrAdapterRuntimeUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.runtime.Stop(ctx, id); err != nil {
		return onebot11.Status{}, err
	}
	status, _ := s.runtime.Status(id)
	return status, nil
}
func (s *Adapters) Restart(ctx context.Context, id string) (onebot11.Status, error) {
	if !s.Available() {
		return onebot11.Status{}, ErrAdapterRuntimeUnavailable
	}
	if err := s.runtime.Restart(ctx, id); err != nil {
		return onebot11.Status{}, err
	}
	status, _ := s.runtime.Status(id)
	return status, nil
}
func (s *Adapters) Test(ctx context.Context, id string) (map[string]any, error) {
	if !s.Available() {
		return nil, ErrAdapterRuntimeUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.runtime.Test(ctx, id)
}
func (s *Adapters) Status(id string) (onebot11.Status, error) {
	if !s.Available() {
		return onebot11.Status{}, ErrAdapterRuntimeUnavailable
	}
	return s.runtime.Status(id)
}
func (s *Adapters) Capabilities(ctx context.Context, id string) ([]string, error) {
	if !s.Available() {
		return nil, ErrAdapterRuntimeUnavailable
	}
	return s.runtime.Capabilities(ctx, id)
}
func (s *Adapters) Send(ctx context.Context, id, messageType, peerID, message string) (string, error) {
	if !s.Available() {
		return "", ErrAdapterRuntimeUnavailable
	}
	if peerID == "" || strings.TrimSpace(message) == "" || len(message) > 4000 {
		return "", &ValidationError{Message: "peerId and a message of 1-4000 bytes are required"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.runtime.Send(ctx, id, messageType, peerID, message)
}
func (s *Adapters) Groups(ctx context.Context, id string) ([]map[string]any, error) {
	if !s.Available() {
		return nil, ErrAdapterRuntimeUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return s.runtime.Groups(ctx, id)
}

func publicAdapterRecord(item Record) Record {
	var detail struct {
		AdapterType string          `json:"adapterType"`
		Config      json.RawMessage `json:"config"`
	}
	if json.Unmarshal([]byte(item.Detail), &detail) != nil {
		item.Detail = `{"adapterType":"unknown","config":{},"configError":true}`
		return item
	}
	var config map[string]json.RawMessage
	if json.Unmarshal(detail.Config, &config) != nil {
		publicDetail, _ := json.Marshal(map[string]any{"adapterType": detail.AdapterType, "config": map[string]any{}, "configError": true})
		item.Detail = string(publicDetail)
		return item
	}
	redactSecret(config, "AccessToken", "accessTokenConfigured")
	redactSecret(config, "AppSecret", "appSecretConfigured")
	publicConfig, _ := json.Marshal(config)
	publicDetail, _ := json.Marshal(map[string]any{"adapterType": detail.AdapterType, "config": json.RawMessage(publicConfig)})
	item.Detail = string(publicDetail)
	return item
}

func redactSecret(config map[string]json.RawMessage, canonical, configured string) {
	legacy := strings.ToLower(canonical[:1]) + canonical[1:]
	var secret string
	_ = json.Unmarshal(config[canonical], &secret)
	if secret == "" {
		_ = json.Unmarshal(config[legacy], &secret)
	}
	if secret != "" {
		config[canonical] = json.RawMessage(`""`)
		delete(config, legacy)
		config[configured] = json.RawMessage(`true`)
	}
}

func preserveAccessToken(oldConfig, newConfig json.RawMessage) json.RawMessage {
	return preserveSecret(oldConfig, newConfig, "AccessToken", "accessTokenConfigured")
}

func preserveSecret(oldConfig, newConfig json.RawMessage, canonical, configuredKey string) json.RawMessage {
	var oldValues, newValues map[string]json.RawMessage
	if json.Unmarshal(oldConfig, &oldValues) != nil || json.Unmarshal(newConfig, &newValues) != nil || newValues == nil {
		return newConfig
	}
	legacy := strings.ToLower(canonical[:1]) + canonical[1:]
	var oldToken, newToken string
	_ = json.Unmarshal(oldValues[canonical], &oldToken)
	if oldToken == "" {
		_ = json.Unmarshal(oldValues[legacy], &oldToken)
	}
	_ = json.Unmarshal(newValues[canonical], &newToken)
	if newToken == "" {
		_ = json.Unmarshal(newValues[legacy], &newToken)
	}
	var configured bool
	_ = json.Unmarshal(newValues[configuredKey], &configured)
	if oldToken != "" && (newToken == "" || newToken == "********" || configured) {
		newValues[canonical] = oldValues[canonical]
		if len(newValues[canonical]) == 0 {
			newValues[canonical] = oldValues[legacy]
		}
		delete(newValues, legacy)
	}
	delete(newValues, configuredKey)
	result, err := json.Marshal(newValues)
	if err != nil {
		return newConfig
	}
	return result
}
