// SPDX-License-Identifier: AGPL-3.0-only
package httpapi

import (
	"github.com/EasybotCE/easybotce/internal/domain"
	"github.com/EasybotCE/easybotce/internal/store"
	"net/http"
	"strings"
	"time"
)

func manageCommands(w http.ResponseWriter, r *http.Request, db *store.Store) bool {
	if r.URL.Path == "/v1/commands" {
		if !methods(w, r, "GET, HEAD, POST") {
			return true
		}
		if r.Method != "POST" {
			endpoint := r.URL.Query().Get("endpoint_id")
			if !(endpoint == "*" || store.ValidID(endpoint)) {
				resourceError(w, r, store.ErrInvalid)
				return true
			}
			items, err := db.ListCommands(r.Context(), endpoint)
			if err != nil {
				resourceError(w, r, err)
			} else {
				writeJSON(w, r, 200, map[string]any{"items": items})
			}
			return true
		}
		var body struct {
			ID         string `json:"id"`
			EndpointID string `json:"endpoint_id"`
			Name       string `json:"name"`
			Response   string `json:"response"`
			Permission string `json:"permission"`
			Enabled    bool   `json:"enabled"`
		}
		if !decodeBody(w, r, &body, "id", "endpoint_id", "name", "response", "permission", "enabled") {
			return true
		}
		c := store.Command{ID: body.ID, EndpointID: body.EndpointID, Name: body.Name, Response: body.Response, Permission: body.Permission, Enabled: body.Enabled, CreatedAt: time.Now().Unix()}
		if err := db.PutCommand(r.Context(), c); err != nil {
			resourceError(w, r, err)
			return true
		}
		_ = db.AddAudit(r.Context(), "admin", "command.create", c.ID)
		w.Header().Set("Location", "/v1/commands/"+c.ID)
		writeJSON(w, r, 201, c)
		return true
	}
	const prefix = "/v1/commands/"
	if strings.HasPrefix(r.URL.Path, prefix) {
		id := strings.TrimPrefix(r.URL.Path, prefix)
		if strings.Contains(id, "/") || !store.ValidID(id) {
			resourceError(w, r, store.ErrNotFound)
			return true
		}
		if !methods(w, r, "DELETE") {
			return true
		}
		err := db.DeleteCommand(r.Context(), id)
		if err != nil {
			resourceError(w, r, err)
		} else {
			_ = db.AddAudit(r.Context(), "admin", "command.delete", id)
			w.WriteHeader(204)
		}
		return true
	}
	return false
}
func servePluginCommand(w http.ResponseWriter, r *http.Request, db *store.Store) bool {
	if r.URL.Path != "/plugin/v1/commands/execute" {
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, r, 405, "method_not_allowed", "method not allowed")
		return true
	}
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		writeError(w, r, 401, "unauthenticated", "endpoint credential required")
		return true
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		writeError(w, r, 401, "unauthenticated", "endpoint credential required")
		return true
	}
	caller, cred, err := db.Authenticate(r.Context(), token)
	if err != nil {
		writeError(w, r, 401, "unauthenticated", "endpoint credential required")
		return true
	}
	if !caller.Enabled || caller.Type != domain.ChatBot || !contains(cred.Capabilities, "commands.execute") {
		writeError(w, r, 403, "capability_denied", "chat-bot endpoint with commands.execute is required")
		return true
	}
	var req struct {
		TargetEndpointID string `json:"target_endpoint_id"`
		Name             string `json:"name"`
		Player           string `json:"player"`
		Args             string `json:"args"`
	}
	if !decodeBody(w, r, &req, "target_endpoint_id", "name", "player", "args") {
		return true
	}
	if !store.ValidID(req.TargetEndpointID) || !commandNameValid(req.Name) || len([]rune(req.Player)) > 64 || len([]rune(req.Args)) > 1024 {
		resourceError(w, r, store.ErrInvalid)
		return true
	}
	target, err := db.GetEndpoint(r.Context(), req.TargetEndpointID)
	if err != nil {
		resourceError(w, r, err)
		return true
	}
	if !target.Enabled || target.Type != domain.MCServer {
		writeError(w, r, 404, "not_found", "target server not found")
		return true
	}
	items, err := db.ListCommands(r.Context(), target.ID)
	if err != nil {
		resourceError(w, r, err)
		return true
	}
	for _, c := range items {
		if c.Name == req.Name && c.Enabled {
			response := strings.NewReplacer("{{player}}", req.Player, "{{server}}", target.Name, "{{args}}", req.Args).Replace(c.Response)
			writeJSON(w, r, 200, map[string]any{"command": c.Name, "permission": c.Permission, "response": response})
			return true
		}
	}
	writeError(w, r, 404, "not_found", "command not found")
	return true
}
func commandNameValid(s string) bool {
	return len(s) > 0 && len(s) <= 32 && s[0] >= 'a' && s[0] <= 'z' && strings.Trim(s, "abcdefghijklmnopqrstuvwxyz0123456789_-") == ""
}
