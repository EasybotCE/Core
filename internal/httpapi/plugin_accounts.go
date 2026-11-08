// SPDX-License-Identifier: AGPL-3.0-only
package httpapi

import (
	"github.com/EasybotCE/easybotce/internal/domain"
	"github.com/EasybotCE/easybotce/internal/store"
	"net/http"
	"strings"
	"time"
)

func servePluginAccounts(w http.ResponseWriter, r *http.Request, db *store.Store) bool {
	if !strings.HasPrefix(r.URL.Path, "/plugin/v1/accounts/") && !strings.HasPrefix(r.URL.Path, "/plugin/v1/players/") {
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
	endpoint, cred, err := db.Authenticate(r.Context(), token)
	if err != nil {
		writeError(w, r, 401, "unauthenticated", "endpoint credential required")
		return true
	}
	switch r.URL.Path {
	case "/plugin/v1/accounts/challenge":
		if endpoint.Type != domain.MCServer || !contains(cred.Capabilities, "accounts.challenge") {
			writeError(w, r, 403, "capability_denied", "mc-server endpoint with accounts.challenge is required")
			return true
		}
		var req struct {
			PlayerID string `json:"player_id"`
		}
		if !decodeBody(w, r, &req, "player_id") {
			return true
		}
		code, exp, err := db.CreateAccountChallenge(r.Context(), endpoint.ID, req.PlayerID, 5*time.Minute)
		if err != nil {
			resourceError(w, r, err)
			return true
		}
		writeJSON(w, r, 200, map[string]any{"code": code, "expires_at": exp, "expires_in": 300})
		return true
	case "/plugin/v1/accounts/claim":
		if endpoint.Type != domain.ChatBot || !contains(cred.Capabilities, "accounts.bind") {
			writeError(w, r, 403, "capability_denied", "chat-bot endpoint with accounts.bind is required")
			return true
		}
		var req struct {
			Code           string `json:"code"`
			PlatformUserID string `json:"platform_user_id"`
		}
		if !decodeBody(w, r, &req, "code", "platform_user_id") {
			return true
		}
		b, err := db.ClaimAccountChallenge(r.Context(), req.Code, endpoint.ID, req.PlatformUserID)
		if err != nil {
			resourceError(w, r, err)
			return true
		}
		writeJSON(w, r, 201, b)
		return true
	case "/plugin/v1/players/resolve":
		if endpoint.Type != domain.MCServer || !contains(cred.Capabilities, "players.state") {
			writeError(w, r, 403, "capability_denied", "mc-server endpoint with players.state is required")
			return true
		}
		var req struct {
			PlayerID string `json:"player_id"`
		}
		if !decodeBody(w, r, &req, "player_id") {
			return true
		}
		state, err := db.GetPlayerState(r.Context(), endpoint.ID, req.PlayerID)
		if err != nil {
			resourceError(w, r, err)
			return true
		}
		bindings, err := db.ListAccountBindings(r.Context(), endpoint.ID, req.PlayerID)
		if err != nil {
			resourceError(w, r, err)
			return true
		}
		vars := map[string]any{"player.id": state.PlayerID, "player.name": state.PlayerName, "player.online": state.Online, "player.world": state.World, "player.x": state.X, "player.y": state.Y, "player.z": state.Z, "server.id": endpoint.ID, "server.name": endpoint.Name}
		if len(bindings) > 0 {
			vars["account.platform_user_id"] = bindings[0].PlatformUserID
			vars["account.chat_endpoint_id"] = bindings[0].ChatEndpointID
		}
		writeJSON(w, r, 200, map[string]any{"state": state, "placeholders": vars})
		return true
	case "/plugin/v1/players/state":
		if endpoint.Type != domain.MCServer || !contains(cred.Capabilities, "players.state") {
			writeError(w, r, 403, "capability_denied", "mc-server endpoint with players.state is required")
			return true
		}
		var body struct {
			PlayerID   string  `json:"player_id"`
			PlayerName string  `json:"player_name"`
			Online     bool    `json:"online"`
			World      string  `json:"world"`
			X          float64 `json:"x"`
			Y          float64 `json:"y"`
			Z          float64 `json:"z"`
		}
		if !decodeBody(w, r, &body, "player_id", "player_name", "online", "world", "x", "y", "z") {
			return true
		}
		req := store.PlayerState{EndpointID: endpoint.ID, PlayerID: body.PlayerID, PlayerName: body.PlayerName, Online: body.Online, World: body.World, X: body.X, Y: body.Y, Z: body.Z}
		if err := db.PutPlayerState(r.Context(), req); err != nil {
			resourceError(w, r, err)
			return true
		}
		writeJSON(w, r, 200, map[string]any{"accepted": true, "updated_at": time.Now().Unix()})
		return true
	}
	return false
}
