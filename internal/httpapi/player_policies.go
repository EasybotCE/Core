// SPDX-License-Identifier: AGPL-3.0-only
package httpapi

import (
	"github.com/EasybotCE/easybotce/internal/store"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func managePlayerPolicies(w http.ResponseWriter, r *http.Request, db *store.Store) bool {
	if r.URL.Path == "/v1/account-bindings" {
		if !methods(w, r, "GET, HEAD") {
			return true
		}
		endpoint, player := r.URL.Query().Get("endpoint_id"), r.URL.Query().Get("player_id")
		if endpoint != "" && !store.ValidID(endpoint) || player != "" && !store.ValidID(player) {
			resourceError(w, r, store.ErrInvalid)
			return true
		}
		items, err := db.ListAccountBindings(r.Context(), endpoint, player)
		if err != nil {
			resourceError(w, r, err)
		} else {
			writeJSON(w, r, 200, map[string]any{"items": items})
		}
		return true
	}
	const bindingPrefix = "/v1/account-bindings/"
	if strings.HasPrefix(r.URL.Path, bindingPrefix) {
		id := strings.TrimPrefix(r.URL.Path, bindingPrefix)
		if strings.Contains(id, "/") || !store.ValidID(id) {
			resourceError(w, r, store.ErrNotFound)
			return true
		}
		if !methods(w, r, "DELETE") {
			return true
		}
		if err := db.DeleteAccountBinding(r.Context(), id); err != nil {
			resourceError(w, r, err)
		} else {
			_ = db.AddAudit(r.Context(), "admin", "account_binding.delete", id)
			w.WriteHeader(204)
		}
		return true
	}
	if r.URL.Path == "/v1/audit/events" {
		if !methods(w, r, "GET, HEAD") {
			return true
		}
		limit := 100
		if q := r.URL.Query().Get("limit"); q != "" {
			if v, e := strconv.Atoi(q); e != nil {
				resourceError(w, r, store.ErrInvalid)
				return true
			} else {
				limit = v
			}
		}
		items, err := db.ListAudit(r.Context(), limit)
		if err != nil {
			resourceError(w, r, err)
		} else {
			writeJSON(w, r, 200, map[string]any{"items": items})
		}
		return true
	}
	if r.URL.Path == "/v1/player-policies" {
		if !methods(w, r, "GET, HEAD, POST") {
			return true
		}
		if r.Method != "POST" {
			endpointID, playerID := r.URL.Query().Get("endpoint_id"), r.URL.Query().Get("player_id")
			if !store.ValidID(endpointID) || !store.ValidID(playerID) {
				resourceError(w, r, store.ErrInvalid)
				return true
			}
			items, err := db.ListPlayerPolicies(r.Context(), endpointID, playerID)
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
			PlayerID   string `json:"player_id"`
			Mode       string `json:"mode"`
			Action     string `json:"action"`
			Reason     string `json:"reason"`
			ExpiresAt  int64  `json:"expires_at"`
		}
		if !decodeBody(w, r, &body, "id", "endpoint_id", "player_id", "mode", "action", "reason", "expires_at") {
			return true
		}
		p := store.PlayerPolicy{ID: body.ID, EndpointID: body.EndpointID, PlayerID: body.PlayerID, Mode: body.Mode, Action: body.Action, Reason: body.Reason, ExpiresAt: body.ExpiresAt, CreatedAt: time.Now().Unix()}
		if err := db.PutPlayerPolicy(r.Context(), p); err != nil {
			resourceError(w, r, err)
			return true
		}
		_ = db.AddAudit(r.Context(), "admin", "player_policy.create", p.ID)
		w.Header().Set("Location", "/v1/player-policies/"+p.ID)
		writeJSON(w, r, 201, p)
		return true
	}
	const prefix = "/v1/player-policies/"
	if strings.HasPrefix(r.URL.Path, prefix) {
		id := strings.TrimPrefix(r.URL.Path, prefix)
		if strings.Contains(id, "/") || !store.ValidID(id) {
			resourceError(w, r, store.ErrNotFound)
			return true
		}
		if !methods(w, r, "DELETE") {
			return true
		}
		err := db.DeletePlayerPolicy(r.Context(), id)
		if err != nil {
			resourceError(w, r, err)
		} else {
			_ = db.AddAudit(r.Context(), "admin", "player_policy.delete", id)
			w.WriteHeader(204)
		}
		return true
	}
	return false
}

func servePluginPlayerPolicy(w http.ResponseWriter, r *http.Request, db *store.Store) bool {
	if r.URL.Path != "/plugin/v1/player-policy" {
		return false
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, r, 405, "method_not_allowed", "method not allowed")
		return true
	}
	value := r.Header.Values("Authorization")
	if len(value) != 1 {
		writeError(w, r, 401, "unauthenticated", "endpoint credential required")
		return true
	}
	scheme, token, ok := strings.Cut(value[0], " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		writeError(w, r, 401, "unauthenticated", "endpoint credential required")
		return true
	}
	endpoint, credential, err := db.Authenticate(r.Context(), token)
	if err != nil {
		writeError(w, r, 401, "unauthenticated", "endpoint credential required")
		return true
	}
	if !endpoint.Enabled || !contains(credential.Capabilities, "players.state") {
		writeError(w, r, 403, "capability_denied", "players.state capability required")
		return true
	}
	var req struct {
		PlayerID string `json:"player_id"`
	}
	if !decodeBody(w, r, &req, "player_id") {
		return true
	}
	if !store.ValidID(req.PlayerID) {
		resourceError(w, r, store.ErrInvalid)
		return true
	}
	policies, err := db.ListPlayerPolicies(r.Context(), endpoint.ID, req.PlayerID)
	if err != nil {
		resourceError(w, r, err)
		return true
	}
	applicable := []store.PlayerPolicy{}
	whitelist := false
	denied := false
	var decision *store.PlayerPolicy
	for i := range policies {
		p := &policies[i]
		applicable = append(applicable, *p)
		if p.Mode == "allow" {
			whitelist = true
		} else {
			denied = true
			if decision == nil {
				decision = p
			}
		}
	}
	allowed := !denied
	if whitelist {
		allowed = false
		for i := range policies {
			if policies[i].Mode == "allow" {
				allowed = true
				if decision == nil {
					decision = &policies[i]
				}
			}
		}
	}
	if denied {
		allowed = false
	}
	result := map[string]any{"endpoint_id": endpoint.ID, "player_id": req.PlayerID, "allowed": allowed, "policies": applicable}
	if decision != nil {
		result["action"] = decision.Action
		result["reason"] = decision.Reason
	}
	writeJSON(w, r, 200, result)
	return true
}
func contains(items []string, v string) bool {
	for _, item := range items {
		if item == v {
			return true
		}
	}
	return false
}
