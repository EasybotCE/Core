// SPDX-License-Identifier: AGPL-3.0-only
package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/EasybotCE/easybotce/api"
	"github.com/EasybotCE/easybotce/internal/config"
)

const testToken = "0123456789abcdef0123456789abcdef"

func handlerForTest(t *testing.T, output io.Writer) http.Handler {
	t.Helper()
	handler, err := NewHandler(config.Config{ListenAddr: "127.0.0.1:8080", AdminToken: testToken, ShutdownTimeout: time.Second}, slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: slog.LevelDebug})))
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func TestRoutes(t *testing.T) {
	t.Parallel()
	handler := handlerForTest(t, io.Discard)
	for _, tc := range []struct {
		method, path, auth string
		status             int
		code               string
	}{
		{"GET", "/healthz", "", 200, ""},
		{"HEAD", "/healthz", "", 200, ""},
		{"POST", "/healthz", "", 405, "method_not_allowed"},
		{"GET", "/v1/system/info", "", 401, "unauthenticated"},
		{"GET", "/v1/system/info", "Bearer wrong", 401, "unauthenticated"},
		{"GET", "/v1/system/info", "Basic " + testToken, 401, "unauthenticated"},
		{"GET", "/v1/system/info?token=" + testToken, "", 401, "unauthenticated"},
		{"GET", "/v1/system/info", "Bearer " + testToken, 200, ""},
		{"GET", "/v1/system/info", "bearer " + testToken, 200, ""},
		{"POST", "/v1/system/info", "Bearer " + testToken, 405, "method_not_allowed"},
		{"GET", "/v1/endpoints", "Bearer " + testToken, 404, "not_found"},
		{"GET", "/v1/unknown", "", 401, "unauthenticated"},
		{"GET", "/unknown", "", 404, "not_found"},
		{"HEAD", "/v1/system/info", "Bearer " + testToken, 200, ""},
		{"HEAD", "/v1/system/info", "", 401, "unauthenticated"},
		{"GET", "/metrics", "", 401, "unauthenticated"},
	} {
		t.Run(tc.method+tc.path+tc.auth[:min(len(tc.auth), 6)], func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.auth != "" {
				r.Header.Set("Authorization", tc.auth)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d, want %d; %s", w.Code, tc.status, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing security headers")
			}
			if tc.status == 401 && w.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("missing authentication challenge")
			}
			if tc.status == 405 && w.Header().Get("Allow") != "GET, HEAD" {
				t.Fatal("missing method list")
			}
			if tc.method == "HEAD" {
				if w.Body.Len() != 0 {
					t.Fatal("HEAD returned body")
				}
				return
			}
			if !json.Valid(w.Body.Bytes()) {
				t.Fatal("invalid JSON response")
			}
			if tc.code != "" {
				var body APIError
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Error.Code != tc.code {
					t.Fatalf("error=%q, want %q", body.Error.Code, tc.code)
				}
			}
		})
	}
}

func TestAmbiguousCredentialsAndSecretLogging(t *testing.T) {
	var logs bytes.Buffer
	handler := handlerForTest(t, &logs)
	for _, headers := range [][]string{
		{"Bearer " + testToken, "Bearer " + testToken},
		{"Bearer  " + testToken},
		{"Bearer " + testToken + " "},
	} {
		r := httptest.NewRequest("GET", "/v1/system/info?token="+testToken, nil)
		for _, h := range headers {
			r.Header.Add("Authorization", h)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("ambiguous credentials accepted: %d", w.Code)
		}
	}
	if strings.Contains(logs.String(), testToken) {
		t.Fatal("logs disclosed token")
	}
}

func TestMetricsRequiresAdminAndRendersPrometheus(t *testing.T) {
	handler := handlerForTest(t, io.Discard)
	for _, tc := range []struct {
		method, auth string
		status       int
	}{
		{"GET", "Bearer " + testToken, http.StatusOK},
		{"POST", "Bearer " + testToken, http.StatusMethodNotAllowed},
		{"GET", "", http.StatusUnauthorized},
	} {
		r := httptest.NewRequest(tc.method, "/metrics", nil)
		if tc.auth != "" {
			r.Header.Set("Authorization", tc.auth)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: status=%d want=%d body=%s", tc.method, w.Code, tc.status, w.Body.String())
		}
		if tc.status == http.StatusOK && (!strings.Contains(w.Body.String(), "easybotce_http_requests_total") || !strings.Contains(w.Header().Get("Content-Type"), "text/plain")) {
			t.Fatalf("invalid metrics response: %q", w.Body.String())
		}
	}
}
func TestPublishedContractsMatchBinary(t *testing.T) {
	handler := handlerForTest(t, io.Discard)
	for path, want := range map[string][]byte{"/v1/spec/openapi.json": api.OpenAPI, "/v1/spec/protocol.schema.json": api.EndpointSchema} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", "Bearer "+testToken)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), want) {
			t.Fatalf("contract mismatch: %s", path)
		}
	}
	var spec struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(api.OpenAPI, &spec); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/healthz", "/v1/system/info", "/v1/spec/openapi.json", "/v1/spec/protocol.schema.json"} {
		operations, ok := spec.Paths[path]
		if !ok {
			t.Fatalf("missing diagnostic contract %s", path)
		}
		for method := range operations {
			r := httptest.NewRequest(strings.ToUpper(method), path, nil)
			r.Header.Set("Authorization", "Bearer "+testToken)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatalf("documented %s %s returned %d", method, path, w.Code)
			}
		}
	}
}

func TestInvalidConfigCannotDisableAuthentication(t *testing.T) {
	if _, err := NewHandler(config.Config{}, nil); err == nil {
		t.Fatal("handler accepted invalid config")
	}
}
