package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"easybot/internal/adapter/onebot11"
	"easybot/internal/store"
)

func (s *server) adapterTypes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, []map[string]string{
		{"adapterType": "Onebot11", "displayName": "OneBot 11", "platformName": "onebot11"},
		{"adapterType": "QQBot", "displayName": "QQ 官方 Bot", "platformName": "qqbot"},
	})
}

func (s *server) adapters(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.List(r.Context(), "adapters")
	if err != nil {
		http.Error(w, "database query failed", http.StatusInternalServerError)
		return
	}
	for i := range items {
		items[i] = publicAdapterRecord(items[i])
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *server) createAdapter(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AdapterType string `json:"adapterType"`
		DisplayName string `json:"displayName"`
		ConfigJSON  string `json:"configJson"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.AdapterType != "Onebot11" && body.AdapterType != "QQBot" {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"message": "当前主程序不支持该适配器"})
		return
	}
	if body.DisplayName == "" {
		body.DisplayName = body.AdapterType
	}
	if body.ConfigJSON == "" {
		body.ConfigJSON = "{}"
	}
	var config json.RawMessage
	if !json.Valid([]byte(body.ConfigJSON)) || json.Unmarshal([]byte(body.ConfigJSON), &config) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "adapter config must be valid JSON"})
		return
	}
	if body.AdapterType == "Onebot11" {
		if _, err := onebot11.ParseConfig(config); err != nil {
			writeAdapterError(w, err)
			return
		}
	} else if _, err := onebot11.ParseQQConfig(config); err != nil {
		writeAdapterError(w, err)
		return
	}
	detail, _ := json.Marshal(map[string]any{"adapterType": body.AdapterType, "config": config})
	item, err := s.store.Save(r.Context(), store.Record{Kind: "adapters", Name: body.DisplayName, Detail: string(detail), Enabled: true})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, publicAdapterRecord(item))
}

func (s *server) updateAdapter(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName string `json:"displayName"`
		ConfigJSON  string `json:"configJson"`
	}
	if !decode(w, r, &body) {
		return
	}
	old, err := s.adapterRecord(r.Context(), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	var oldDetail struct {
		AdapterType string          `json:"adapterType"`
		Config      json.RawMessage `json:"config"`
	}
	if err := json.Unmarshal([]byte(old.Detail), &oldDetail); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "stored adapter config is invalid"})
		return
	}
	var config json.RawMessage
	if !json.Valid([]byte(body.ConfigJSON)) || json.Unmarshal([]byte(body.ConfigJSON), &config) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "adapter config must be valid JSON"})
		return
	}
	config = preserveAccessToken(oldDetail.Config, config)
	if oldDetail.AdapterType == "QQBot" {
		config = preserveSecret(oldDetail.Config, config, "AppSecret", "appSecretConfigured")
	}
	if oldDetail.AdapterType == "Onebot11" {
		if _, err := onebot11.ParseConfig(config); err != nil {
			writeAdapterError(w, err)
			return
		}
	} else if oldDetail.AdapterType == "QQBot" {
		if _, err := onebot11.ParseQQConfig(config); err != nil {
			writeAdapterError(w, err)
			return
		}
	}
	detail, _ := json.Marshal(map[string]any{"adapterType": oldDetail.AdapterType, "config": config})
	item, err := s.store.Save(r.Context(), store.Record{ID: old.ID, Kind: "adapters", Name: body.DisplayName, Detail: string(detail), Enabled: old.Enabled})
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if s.adapterManager != nil {
		if status, statusErr := s.adapterManager.Status(item.ID); statusErr == nil && status.State != onebot11.StateStopped {
			if restartErr := s.adapterManager.Restart(r.Context(), item.ID); restartErr != nil {
				s.logger.Warn("适配器 "+item.ID+" 更新配置后重启失败", "error", restartErr)
			}
		}
	}
	writeJSON(w, http.StatusOK, publicAdapterRecord(item))
}

func (s *server) adapterStart(w http.ResponseWriter, r *http.Request) {
	if s.adapterManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "Adapter runtime is unavailable."})
		return
	}
	if err := s.adapterManager.Start(r.Context(), r.PathValue("id")); err != nil {
		writeAdapterError(w, err)
		return
	}
	status, _ := s.adapterManager.Status(r.PathValue("id"))
	writeJSON(w, http.StatusAccepted, status)
}

func (s *server) adapterStop(w http.ResponseWriter, r *http.Request) {
	if s.adapterManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "Adapter runtime is unavailable."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if err := s.adapterManager.Stop(ctx, r.PathValue("id")); err != nil {
		writeAdapterError(w, err)
		return
	}
	status, _ := s.adapterManager.Status(r.PathValue("id"))
	writeJSON(w, http.StatusOK, status)
}

func (s *server) adapterRestart(w http.ResponseWriter, r *http.Request) {
	if s.adapterManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "Adapter runtime is unavailable."})
		return
	}
	if err := s.adapterManager.Restart(r.Context(), r.PathValue("id")); err != nil {
		writeAdapterError(w, err)
		return
	}
	status, _ := s.adapterManager.Status(r.PathValue("id"))
	writeJSON(w, http.StatusAccepted, status)
}

func (s *server) adapterTest(w http.ResponseWriter, r *http.Request) {
	if s.adapterManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "Adapter runtime is unavailable."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := s.adapterManager.Test(ctx, r.PathValue("id"))
	if err != nil {
		writeAdapterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) adapterStatus(w http.ResponseWriter, r *http.Request) {
	if s.adapterManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "Adapter runtime is unavailable."})
		return
	}
	status, err := s.adapterManager.Status(r.PathValue("id"))
	if err != nil {
		writeAdapterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *server) adapterCapabilities(w http.ResponseWriter, r *http.Request) {
	if s.adapterManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "Adapter runtime is unavailable."})
		return
	}
	capabilities, err := s.adapterManager.Capabilities(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAdapterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, capabilities)
}

func (s *server) adapterSend(w http.ResponseWriter, r *http.Request) {
	if s.adapterManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "Adapter runtime is unavailable."})
		return
	}
	var body struct {
		MessageType string `json:"messageType"`
		PeerID      string `json:"peerId"`
		Message     string `json:"message"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.PeerID == "" || strings.TrimSpace(body.Message) == "" || len(body.Message) > 4000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "peerId and a message of 1-4000 bytes are required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	messageID, err := s.adapterManager.Send(ctx, r.PathValue("id"), body.MessageType, body.PeerID, body.Message)
	if err != nil {
		writeAdapterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"messageId": messageID})
}

func (s *server) adapterGroups(w http.ResponseWriter, r *http.Request) {
	if s.adapterManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "Adapter runtime is unavailable."})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	groups, err := s.adapterManager.Groups(ctx, r.PathValue("id"))
	if err != nil {
		writeAdapterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, groups)
}

func (s *server) adapterRecord(ctx context.Context, id string) (store.Record, error) {
	items, err := s.store.List(ctx, "adapters")
	if err != nil {
		return store.Record{}, err
	}
	for _, item := range items {
		if item.ID == id {
			return item, nil
		}
	}
	return store.Record{}, sql.ErrNoRows
}

func publicAdapterRecord(item store.Record) store.Record {
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
	if json.Unmarshal(oldConfig, &oldValues) != nil || json.Unmarshal(newConfig, &newValues) != nil {
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

func writeAdapterError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	var apiErr *onebot11.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "adapter_config_invalid", "adapter_invalid_target":
			status = http.StatusBadRequest
		case "adapter_auth_failed":
			status = http.StatusUnauthorized
		case "adapter_rate_limited":
			status = http.StatusTooManyRequests
		case "adapter_offline":
			status = http.StatusServiceUnavailable
		case "adapter_type_unsupported":
			status = http.StatusNotImplemented
		}
	} else if errors.Is(err, onebot11.ErrAdapterNotFound) || errors.Is(err, sql.ErrNoRows) {
		status = http.StatusNotFound
	}
	code := "adapter_error"
	if errors.As(err, &apiErr) {
		code = apiErr.Code
	}
	writeJSON(w, status, map[string]any{"message": err.Error(), "code": code})
}
