// SPDX-License-Identifier: AGPL-3.0-only
package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/EasybotCE/easybotce/internal/session"
	"github.com/EasybotCE/easybotce/internal/store"
)

func manageOneBotAction(w http.ResponseWriter, r *http.Request, sessions *session.Manager) bool {
	if r.URL.Path == "/v1/onebot/actions" {
		if !methods(w, r, "GET, HEAD") {
			return true
		}
		q := r.URL.Query()
		endpointID := q.Get("endpoint_id")
		if endpointID != "" && !store.ValidID(endpointID) {
			resourceError(w, r, store.ErrInvalid)
			return true
		}
		limit := 20
		if raw := q.Get("limit"); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > 100 {
				resourceError(w, r, store.ErrInvalid)
				return true
			}
			limit = n
		}
		before := int64(0)
		beforeID := q.Get("before_id")
		if raw := q.Get("before"); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || n < 1 {
				resourceError(w, r, store.ErrInvalid)
				return true
			}
			before = n
		}
		if beforeID != "" && (!store.ValidID(beforeID) || before == 0) {
			resourceError(w, r, store.ErrInvalid)
			return true
		}
		actions, err := sessions.ListOneBotActions(endpointID, before, beforeID, limit)
		if err != nil {
			resourceError(w, r, err)
			return true
		}
		response := map[string]any{"items": actions, "limit": limit}
		if len(actions) == limit {
			last := actions[len(actions)-1]
			response["next_before"] = last.CreatedAt
			response["next_before_id"] = last.ID
		}
		writeJSON(w, r, http.StatusOK, response)
		return true
	}
	const prefix = "/v1/onebot/actions/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		return false
	}
	id := strings.TrimPrefix(r.URL.Path, prefix)
	if strings.Contains(id, "/") || !store.ValidID(id) {
		resourceError(w, r, store.ErrNotFound)
		return true
	}
	if !methods(w, r, "GET, HEAD") {
		return true
	}
	action, ok := sessions.OneBotAction(id)
	if !ok {
		resourceError(w, r, store.ErrNotFound)
		return true
	}
	writeJSON(w, r, http.StatusOK, action)
	return true
}
