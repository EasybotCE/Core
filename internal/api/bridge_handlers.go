package api

import (
	"easybot/internal/application"
	"easybot/internal/bridge"
	"errors"
	"net/http"
)

func (s *server) bridgeStatuses(w http.ResponseWriter, _ *http.Request) {
	items, err := s.serverService.Statuses()
	if err != nil {
		writeBridgeStatusError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (s *server) bridgeStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.serverService.Status(r.PathValue("id"))
	if err != nil {
		writeBridgeStatusError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
func (s *server) bridgeSend(w http.ResponseWriter, r *http.Request) {
	if !s.serverService.Available() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": application.ErrBridgeRuntimeUnavailable.Error()})
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	if err := s.serverService.Send(r.PathValue("id"), body.Text); err != nil {
		status := http.StatusServiceUnavailable
		var validation *application.ValidationError
		if errors.As(err, &validation) {
			status = http.StatusBadRequest
		}
		writeJSON(w, status, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}
func (s *server) rotateServerToken(w http.ResponseWriter, r *http.Request) {
	result, err := s.serverService.RotateToken(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, application.ErrServerTokenGeneration) {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		} else {
			writeStoreError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func writeBridgeStatusError(w http.ResponseWriter, err error) {
	if errors.Is(err, application.ErrBridgeRuntimeUnavailable) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": err.Error()})
	} else if errors.Is(err, bridge.ErrUnknownServer) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "server not found"})
	} else {
		http.Error(w, "bridge status unavailable", http.StatusInternalServerError)
	}
}
