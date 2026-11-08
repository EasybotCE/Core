// SPDX-License-Identifier: AGPL-3.0-only
package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/EasybotCE/easybotce/api"
	"github.com/EasybotCE/easybotce/internal/buildinfo"
	"github.com/EasybotCE/easybotce/internal/config"
	"github.com/EasybotCE/easybotce/internal/protocol"
	"github.com/EasybotCE/easybotce/internal/session"
	"github.com/EasybotCE/easybotce/internal/store"
)

type APIError struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type SystemInfo struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	Milestone       string `json:"milestone"`
	ProtocolVersion string `json:"protocol_version"`
}

// NewHandler rejects an invalid configuration even when embedded by another caller.
func NewHandler(cfg config.Config, logger *slog.Logger) (http.Handler, error) {
	return NewRuntimeHandler(cfg, logger, nil, nil)
}

// NewRuntimeHandler attaches persistent management and endpoint transport.
// A nil store/manager pair serves only the bootstrap diagnostic API.
func NewRuntimeHandler(cfg config.Config, logger *slog.Logger, db *store.Store, sessions *session.Manager) (http.Handler, error) {
	if (db == nil) != (sessions == nil) {
		return nil, fmt.Errorf("store and session manager must be supplied together")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	expected := sha256.Sum256([]byte(cfg.AdminToken))
	metrics := newHTTPMetrics()
	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		// Deliberately omit paths, query strings, headers and bodies: even a
		// malformed request can contain a credential in one of those places.
		defer func() { logger.Debug("http request", "method", r.Method, "duration", time.Since(started)) }()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")

		if r.URL.Path == "/v1/gateway" && sessions != nil {
			sessions.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/onebot/v11/ws" && sessions != nil {
			sessions.ServeOneBotHTTP(w, r)
			return
		}
		if r.URL.Path == "/metrics" {
			if !authorized(r, expected) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="easybotce"`)
				writeError(w, r, http.StatusUnauthorized, "unauthenticated", "a valid administrator bearer token is required")
				return
			}
			if r.Method != http.MethodGet {
				w.Header().Set("Allow", "GET")
				writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
				return
			}
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(metrics.render())
			return
		}
		if db != nil && (servePluginPlayerPolicy(w, r, db) || servePluginCommand(w, r, db) || servePluginAccounts(w, r, db)) {
			return
		}
		if r.URL.Path == "/healthz" {
			if !readMethod(w, r) {
				return
			}
			writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
			return
		}
		if r.URL.Path == "/v1" || strings.HasPrefix(r.URL.Path, "/v1/") {
			if !authorized(r, expected) {
				w.Header().Set("WWW-Authenticate", `Bearer realm="easybotce"`)
				writeError(w, r, http.StatusUnauthorized, "unauthenticated", "a valid administrator bearer token is required")
				return
			}
			if sessions != nil && manageOneBotAction(w, r, sessions) {
				return
			}
			if db != nil && manage(w, r, db, sessions) {
				return
			}
			switch r.URL.Path {
			case "/v1/system/info":
				if !readMethod(w, r) {
					return
				}
				writeJSON(w, r, http.StatusOK, SystemInfo{"EasybotCE", buildinfo.Version, buildinfo.Milestone, protocol.Version})
				return
			case "/v1/spec/openapi.json":
				if !readMethod(w, r) {
					return
				}
				writeDocument(w, r, "application/json", api.OpenAPI)
				return
			case "/v1/spec/protocol.schema.json":
				if !readMethod(w, r) {
					return
				}
				writeDocument(w, r, "application/schema+json", api.EndpointSchema)
				return
			}
		}
		writeError(w, r, http.StatusNotFound, "not_found", "resource not found")
	})
	return instrumentHTTP(metrics, base), nil
}

func authorized(r *http.Request, expected [32]byte) bool {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return false
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return false
	}
	actual := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(actual[:], expected[:]) == 1
}

func readMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	return false
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	writeJSON(w, r, status, APIError{ErrorDetail{code, message}})
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	} // Only fixed, serializable response types are passed here.
	writeBytes(w, r, status, "application/json", append(data, '\n'))
}

func writeDocument(w http.ResponseWriter, r *http.Request, mediaType string, data []byte) {
	writeBytes(w, r, http.StatusOK, mediaType, data)
}

func writeBytes(w http.ResponseWriter, r *http.Request, status int, mediaType string, data []byte) {
	w.Header().Set("Content-Type", mediaType)
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}
