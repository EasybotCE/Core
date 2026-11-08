// SPDX-License-Identifier: AGPL-3.0-only
package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/EasybotCE/easybotce/internal/domain"
	"github.com/EasybotCE/easybotce/internal/session"
	"github.com/EasybotCE/easybotce/internal/store"
)

// decodeBody requires an exact top-level JSON object, forbids duplicate or
// case-mismatched keys, and never returns decoder/source diagnostics to clients.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any, fields ...string) bool {
	media := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0])
	if !strings.EqualFold(media, "application/json") {
		writeError(w, r, 415, "unsupported_media_type", "Content-Type must be application/json")
		return false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	invalid := func() bool {
		writeError(w, r, 400, "invalid_request", "body must be a valid JSON object with the documented fields")
		return false
	}
	if err != nil || !utf8.Valid(raw) || !validJSONShape(raw, reflect.TypeOf(dst).Elem()) {
		return invalid()
	}
	allowed := map[string]bool{}
	for _, f := range fields {
		allowed[f] = true
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	first, err := dec.Token()
	if err != nil || first != json.Delim('{') {
		return invalid()
	}
	seen := map[string]bool{}
	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return invalid()
		}
		key, ok := token.(string)
		if !ok || !allowed[key] || seen[key] {
			return invalid()
		}
		seen[key] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return invalid()
		}
	}
	if _, err := dec.Token(); err != nil {
		return invalid()
	}
	if _, err := dec.Token(); err != io.EOF {
		return invalid()
	}
	dec = json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return invalid()
	}
	return true
}
func methods(w http.ResponseWriter, r *http.Request, allowed string) bool {
	for _, m := range strings.Split(allowed, ", ") {
		if r.Method == m {
			return true
		}
	}
	w.Header().Set("Allow", allowed)
	writeError(w, r, 405, "method_not_allowed", "method not allowed")
	return false
}
func resourceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, r, 404, "not_found", "resource not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, r, 409, "conflict", "resource already exists")
	case errors.Is(err, store.ErrInvalid):
		writeError(w, r, 400, "invalid_request", "resource fields are invalid")
	default:
		writeError(w, r, 503, "unavailable", "persistent service unavailable")
	}
}
func manage(w http.ResponseWriter, r *http.Request, db *store.Store, sessions *session.Manager) bool {
	path := r.URL.Path
	if manageCommands(w, r, db) {
		return true
	}
	if managePlayerPolicies(w, r, db) {
		return true
	}
	if manageRouting(w, r, db, sessions) {
		return true
	}
	if path == "/v1/system/readiness" {
		if !readMethod(w, r) {
			return true
		}
		if err := db.Ping(r.Context()); err != nil {
			resourceError(w, r, err)
			return true
		}
		writeJSON(w, r, 200, map[string]string{"status": "ready"})
		return true
	}
	if path == "/v1/sessions" {
		if !readMethod(w, r) {
			return true
		}
		writeJSON(w, r, 200, map[string]any{"items": sessions.List()})
		return true
	}
	if path != "/v1/endpoints" && !strings.HasPrefix(path, "/v1/endpoints/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/v1/endpoints"), "/")
	if path == "/v1/endpoints" {
		if !methods(w, r, "GET, HEAD, POST") {
			return true
		}
		if r.Method != "POST" {
			after, limit, err := page(r)
			if err != nil {
				resourceError(w, r, err)
				return true
			}
			items, err := db.ListEndpoints(r.Context(), after, limit)
			if err != nil {
				resourceError(w, r, err)
				return true
			}
			cursor := ""
			if len(items) == limit {
				cursor = items[len(items)-1].ID
			}
			writeJSON(w, r, 200, map[string]any{"items": items, "next_after": cursor})
			return true
		}
		var body struct {
			ID      string              `json:"id"`
			Type    domain.EndpointType `json:"type"`
			Name    string              `json:"name"`
			Enabled *bool               `json:"enabled"`
		}
		if !decodeBody(w, r, &body, "id", "type", "name", "enabled") {
			return true
		}
		if body.Enabled == nil {
			resourceError(w, r, store.ErrInvalid)
			return true
		}
		e := store.Endpoint{ID: body.ID, Type: body.Type, Name: body.Name, Enabled: *body.Enabled}
		err := sessions.Mutate(func() error { return db.CreateEndpoint(r.Context(), e) }, nil)
		if err != nil {
			resourceError(w, r, err)
			return true
		}
		w.Header().Set("Location", "/v1/endpoints/"+e.ID)
		writeJSON(w, r, 201, e)
		return true
	}
	if len(parts) < 2 || !store.ValidID(parts[1]) {
		writeError(w, r, 404, "not_found", "resource not found")
		return true
	}
	id := parts[1]
	if len(parts) == 2 {
		if !methods(w, r, "GET, HEAD, PUT, DELETE") {
			return true
		}
		switch r.Method {
		case "GET", "HEAD":
			e, err := db.GetEndpoint(r.Context(), id)
			if err != nil {
				resourceError(w, r, err)
			} else {
				writeJSON(w, r, 200, e)
			}
		case "PUT":
			var body struct {
				Name    string `json:"name"`
				Enabled *bool  `json:"enabled"`
			}
			if !decodeBody(w, r, &body, "name", "enabled") {
				return true
			}
			if body.Enabled == nil {
				resourceError(w, r, store.ErrInvalid)
				return true
			}
			var updated store.Endpoint
			err := sessions.Mutate(func() error {
				var err error
				updated, err = db.GetEndpoint(r.Context(), id)
				if err != nil {
					return err
				}
				updated.Name = body.Name
				updated.Enabled = *body.Enabled
				return db.UpdateEndpoint(r.Context(), updated)
			}, func(info session.Info) bool { return info.EndpointID == id && !*body.Enabled })
			if err != nil {
				resourceError(w, r, err)
			} else {
				writeJSON(w, r, 200, updated)
			}
		case "DELETE":
			err := sessions.Mutate(func() error { return db.DeleteEndpoint(r.Context(), id) }, func(info session.Info) bool { return info.EndpointID == id })
			if err != nil {
				resourceError(w, r, err)
			} else {
				w.WriteHeader(204)
			}
		}
		return true
	}
	if len(parts) == 3 && parts[2] == "credentials" {
		if !methods(w, r, "GET, HEAD, POST") {
			return true
		}
		if r.Method != "POST" {
			items, err := db.ListCredentials(r.Context(), id)
			if err != nil {
				resourceError(w, r, err)
			} else {
				writeJSON(w, r, 200, map[string]any{"items": items})
			}
			return true
		}
		var body struct {
			Capabilities []string `json:"capabilities"`
		}
		if !decodeBody(w, r, &body, "capabilities") {
			return true
		}
		var issued store.IssuedCredential
		err := sessions.Mutate(func() error {
			// A hard cap also bounds credential-list response size and forgotten keys.
			items, err := db.ListCredentials(r.Context(), id)
			if err != nil {
				return err
			}
			if len(items) >= 64 {
				return store.ErrConflict
			}
			issued, err = db.IssueCredential(r.Context(), id, body.Capabilities)
			return err
		}, nil)
		if err != nil {
			resourceError(w, r, err)
		} else {
			w.Header().Set("Location", "/v1/endpoints/"+id+"/credentials/"+issued.ID)
			writeJSON(w, r, 201, issued)
		}
		return true
	}
	if len(parts) == 4 && parts[2] == "credentials" && store.ValidID(parts[3]) {
		if !methods(w, r, "DELETE") {
			return true
		}
		credentialID := parts[3]
		err := sessions.Mutate(func() error { return db.RevokeCredential(r.Context(), id, credentialID) }, func(info session.Info) bool { return info.CredentialID == credentialID })
		if err != nil {
			resourceError(w, r, err)
		} else {
			w.WriteHeader(204)
		}
		return true
	}
	writeError(w, r, 404, "not_found", "resource not found")
	return true
}

// validJSONShape recursively rejects duplicate, unknown, case-folded, missing
// and null fields in request objects, including rule transforms. All request
// fields are required; PUT is a full replacement, not a partial PATCH.
func validJSONShape(raw []byte, t reflect.Type) bool {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		allowed := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
			allowed[name] = t.Field(i).Type
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		first, err := dec.Token()
		if err != nil || first != json.Delim('{') {
			return false
		}
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return false
			}
			name, ok := key.(string)
			field, exists := allowed[name]
			if !ok || !exists || seen[name] {
				return false
			}
			seen[name] = true
			var value json.RawMessage
			if dec.Decode(&value) != nil || !validJSONShape(value, field) {
				return false
			}
		}
		if len(seen) != len(allowed) {
			return false
		}
		_, err = dec.Token()
		return err == nil
	case reflect.Slice:
		if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
			return false
		}
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return false
		}
		for _, value := range values {
			if !validJSONShape(value, t.Elem()) {
				return false
			}
		}
		return true
	default:
		return true // encoding/json checks primitive types during decodeBody.
	}
}
