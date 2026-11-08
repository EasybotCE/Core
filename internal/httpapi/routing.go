// SPDX-License-Identifier: AGPL-3.0-only
package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/EasybotCE/easybotce/internal/session"
	"github.com/EasybotCE/easybotce/internal/store"
)

func page(r *http.Request) (string, int, error) {
	limit := 100
	var err error
	q := r.URL.Query()
	for key, values := range q {
		if (key != "after" && key != "limit") || len(values) != 1 {
			return "", 0, store.ErrInvalid
		}
	}
	if raw := q.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
	}
	after := q.Get("after")
	if err != nil || limit < 1 || limit > 200 || (after != "" && !store.ValidID(after)) {
		return "", 0, store.ErrInvalid
	}
	return after, limit, nil
}
func manageRouting(w http.ResponseWriter, r *http.Request, db *store.Store, sessions *session.Manager) bool {
	var base string
	for _, candidate := range []string{"/v1/rule-groups", "/v1/routing-bindings"} {
		if r.URL.Path == candidate || strings.HasPrefix(r.URL.Path, candidate+"/") {
			base = candidate
			break
		}
	}
	if base == "" {
		return false
	}
	isRule := base == "/v1/rule-groups"
	id := strings.TrimPrefix(r.URL.Path, base)
	collection := id == ""
	id = strings.TrimPrefix(id, "/")
	if !collection && !store.ValidID(id) {
		resourceError(w, r, store.ErrNotFound)
		return true
	}
	allowed := "GET, HEAD, PUT, DELETE"
	if collection {
		allowed = "GET, HEAD, POST"
	}
	if !methods(w, r, allowed) {
		return true
	}
	ctx := r.Context()
	if r.Method == "GET" || r.Method == "HEAD" {
		if !collection {
			var result any
			var err error
			if isRule {
				result, err = db.GetRuleGroup(ctx, id)
			} else {
				result, err = db.GetRoutingBinding(ctx, id)
			}
			if err != nil {
				resourceError(w, r, err)
			} else {
				writeJSON(w, r, 200, result)
			}
			return true
		}
		after, limit, err := page(r)
		if err != nil {
			resourceError(w, r, err)
			return true
		}
		var items any
		next := ""
		if isRule {
			var rows []store.RuleGroup
			rows, err = db.ListRuleGroups(ctx, after, limit)
			items = rows
			if len(rows) == limit {
				next = rows[len(rows)-1].ID
			}
		} else {
			var rows []store.RoutingBinding
			rows, err = db.ListRoutingBindings(ctx, after, limit)
			items = rows
			if len(rows) == limit {
				next = rows[len(rows)-1].ID
			}
		}
		if err != nil {
			resourceError(w, r, err)
		} else {
			writeJSON(w, r, 200, map[string]any{"items": items, "next_after": next})
		}
		return true
	}
	if r.Method == "DELETE" {
		err := sessions.Mutate(func() error {
			if isRule {
				return db.DeleteRuleGroup(ctx, id)
			}
			return db.DeleteRoutingBinding(ctx, id)
		}, nil)
		if err != nil {
			resourceError(w, r, err)
		} else {
			w.WriteHeader(204)
		}
		return true
	}
	var body any
	var mutate func() error
	if isRule {
		var rule store.RuleGroup
		if !decodeBody(w, r, &rule, "id", "name", "rules") {
			return true
		}
		if !collection && rule.ID != id {
			resourceError(w, r, store.ErrInvalid)
			return true
		}
		body = rule
		mutate = func() error {
			if collection {
				return db.CreateRuleGroup(ctx, rule)
			}
			return db.UpdateRuleGroup(ctx, rule)
		}
	} else {
		var binding store.RoutingBinding
		if !decodeBody(w, r, &binding, "id", "source_endpoint_id", "target_endpoint_id", "source_channel", "target_channel", "rule_group_id", "enabled") {
			return true
		}
		if !collection && binding.ID != id {
			resourceError(w, r, store.ErrInvalid)
			return true
		}
		body = binding
		mutate = func() error {
			if collection {
				return db.CreateRoutingBinding(ctx, binding)
			}
			return db.UpdateRoutingBinding(ctx, binding)
		}
	}
	if err := sessions.Mutate(mutate, nil); err != nil {
		resourceError(w, r, err)
		return true
	}
	status := 200
	if collection {
		status = 201
	}
	writeJSON(w, r, status, body)
	return true
}
