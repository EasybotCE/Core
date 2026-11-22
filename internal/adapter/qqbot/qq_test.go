package qqbot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseQQConfigDefaultsAndValidation(t *testing.T) {
	c, err := ParseQQConfig(json.RawMessage(`{"AppID":"app","AppSecret":"secret"}`))
	if err != nil || c.APIBase != qqAPIBase || c.TokenURL != qqTokenURL || c.Intents == 0 || c.Heartbeat != 30000 {
		t.Fatalf("unexpected QQ config: %#v, %v", c, err)
	}
	if _, err := ParseQQConfig(json.RawMessage(`{"AppID":"","AppSecret":"secret"}`)); err == nil {
		t.Fatal("expected missing app id to be rejected")
	}
	if _, err := ParseQQConfig(json.RawMessage(`{"AppID":"app","AppSecret":"secret","HeartbeatInterval":10}`)); err != nil {
		t.Fatalf("invalid low heartbeat should use safe default: %v", err)
	}
}

func TestQQHTTPStatusClassification(t *testing.T) {
	cases := []struct {
		status    int
		code      string
		retryable bool
	}{
		{http.StatusUnauthorized, "adapter_auth_failed", false},
		{http.StatusForbidden, "adapter_permission_denied", false},
		{http.StatusTooManyRequests, "adapter_rate_limited", true},
		{http.StatusBadRequest, "adapter_api_failed", false},
		{http.StatusBadGateway, "adapter_api_failed", true},
	}
	for _, test := range cases {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			}))
			defer server.Close()

			runtime := NewQQRuntime("test", QQConfig{}, nil)
			err := runtime.request(context.Background(), http.MethodGet, server.URL, nil, nil, "")
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected APIError, got %v", err)
			}
			if apiErr.Code != test.code || apiErr.Retryable != test.retryable {
				t.Fatalf("APIError = %#v, want code %q retryable=%t", apiErr, test.code, test.retryable)
			}
		})
	}
}
