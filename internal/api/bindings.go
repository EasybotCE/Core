package api

import (
	"easybot/internal/application"
	"errors"
	"net/http"
	"strconv"
	"time"
)

func (s *server) bindingChallenges(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PlayerUUID      string `json:"playerUuid"`
		ServerID        string `json:"serverId"`
		GroupID         string `json:"groupId"`
		LifetimeSeconds int    `json:"lifetimeSeconds"`
		MaxAttempts     int    `json:"maxAttempts"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.LifetimeSeconds == 0 {
		body.LifetimeSeconds = 300
	}
	if body.MaxAttempts == 0 {
		body.MaxAttempts = 5
	}
	challenge, err := s.bindings.CreateChallenge(r.Context(), body.PlayerUUID, body.ServerID, body.GroupID, time.Duration(body.LifetimeSeconds)*time.Second, body.MaxAttempts)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, challenge)
}

func (s *server) confirmBinding(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code         string `json:"code"`
		PlatformType string `json:"platformType"`
		AccountID    string `json:"accountId"`
		DisplayName  string `json:"displayName"`
		GroupID      string `json:"groupId"`
	}
	if !decode(w, r, &body) {
		return
	}
	binding, err := s.bindings.Confirm(r.Context(), body.Code, body.PlatformType, body.AccountID, body.DisplayName, body.GroupID)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, application.ErrBindingConflict) {
			status = http.StatusConflict
		}
		writeJSON(w, status, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, binding)
}

func (s *server) listBindings(w http.ResponseWriter, r *http.Request) {
	items, err := s.bindings.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "binding query failed"})
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (s *server) deleteBinding(w http.ResponseWriter, r *http.Request) {
	if err := s.bindings.Delete(r.Context(), r.PathValue("id")); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, application.ErrBindingNotFound) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]string{"message": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *server) bindingAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.bindings.Audit(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "binding audit query failed"})
		return
	}
	writeJSON(w, http.StatusOK, items)
}
