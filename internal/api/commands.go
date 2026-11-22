package api

import (
	"net/http"
	"sort"

	"easybot/internal/command"
	"easybot/internal/store"
)

// diagnoseCommands evaluates saved commands independently. It never chooses a
// winner: conflicting matches need an explicit future runtime policy.
func (s *server) diagnoseCommands(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Message string          `json:"message"`
		Context command.Context `json:"context"`
	}
	if !decode(w, r, &request) {
		return
	}
	if err := command.ValidateInput(request.Message, request.Context); err != nil {
		writeStoreError(w, err)
		return
	}
	items, err := s.store.List(r.Context(), "commands")
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if len(items) > 500 {
		writeStoreError(w, &store.ValidationError{Field: "commands", Message: "诊断最多支持 500 条命令，请先精简配置"})
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	type evaluation struct {
		ID     string          `json:"id"`
		Name   string          `json:"name"`
		Result *command.Result `json:"result,omitempty"`
		Error  string          `json:"error,omitempty"`
	}
	results := make([]evaluation, 0, len(items))
	matched, allowed, invalid, disabled := 0, 0, 0, 0
	for _, item := range items {
		if err := r.Context().Err(); err != nil {
			return
		}
		if !item.Enabled {
			disabled++
			continue
		}
		result, err := command.Simulate(item.Detail, true, request.Message, request.Context)
		entry := evaluation{ID: item.ID, Name: item.Name}
		if err != nil {
			entry.Error = err.Error()
			invalid++
		} else {
			entry.Result = &result
			if result.Matched {
				matched++
			}
			if result.Allowed {
				allowed++
			}
		}
		results = append(results, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"simulated": true, "executed": false, "runtimeAvailable": false, "items": results, "matched": matched, "allowed": allowed, "invalid": invalid, "disabled": disabled, "conflict": matched > 1})
}

func (s *server) simulateCommand(w http.ResponseWriter, r *http.Request) {
	var request struct {
		// Use an ID for persisted configuration, or detail+enabled for a draft.
		ID      string          `json:"id"`
		Detail  *string         `json:"detail"`
		Enabled *bool           `json:"enabled"`
		Message string          `json:"message"`
		Context command.Context `json:"context"`
	}
	if !decode(w, r, &request) {
		return
	}
	detail, enabled := "", false
	if request.ID != "" {
		if request.Detail != nil || request.Enabled != nil {
			writeStoreError(w, &store.ValidationError{Field: "id", Message: "模拟已保存命令时不能同时覆盖草稿配置"})
			return
		}
		item, err := s.store.Get(r.Context(), "commands", request.ID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		detail, enabled = item.Detail, item.Enabled
	} else {
		if request.Detail == nil || request.Enabled == nil {
			writeStoreError(w, &store.ValidationError{Field: "detail", Message: "模拟草稿必须提供 detail 和 enabled"})
			return
		}
		detail, enabled = *request.Detail, *request.Enabled
	}
	result, err := command.Simulate(detail, enabled, request.Message, request.Context)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) validateCommand(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Detail string `json:"detail"`
	}
	if !decode(w, r, &request) {
		return
	}
	if _, err := store.ValidateCommandConfig(request.Detail); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "persisted": false, "runtimeAvailable": false})
}
