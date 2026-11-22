package api

import (
	"database/sql"
	"easybot/internal/adapter/onebot11"
	"easybot/internal/application"
	"encoding/json"
	"errors"
	"net/http"
)

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid JSON body"})
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "record not found"})
		return
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// 配置保存错误延续记录 CRUD 的响应格式，平台校验错误保留 code 字段。
func writeAdapterConfigError(w http.ResponseWriter, err error) {
	if errors.Is(err, application.ErrUnsupportedAdapter) {
		writeJSON(w, http.StatusNotImplemented, map[string]string{"message": err.Error()})
		return
	}
	var platformError *onebot11.APIError
	if errors.As(err, &platformError) {
		writeAdapterError(w, err)
		return
	}
	writeStoreError(w, err)
}

func writeAdapterError(w http.ResponseWriter, err error) {
	if errors.Is(err, application.ErrAdapterRuntimeUnavailable) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": err.Error()})
		return
	}
	var validation *application.ValidationError
	if errors.As(err, &validation) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	status := http.StatusBadGateway
	var apiErr *onebot11.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.Code {
		case "adapter_config_invalid", "adapter_invalid_target":
			status = http.StatusBadRequest
		case "adapter_auth_failed":
			status = http.StatusUnauthorized
		case "adapter_permission_denied":
			status = http.StatusForbidden
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
