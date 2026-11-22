package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"easybot/internal/bridge"
)

func (s *server) bridgeStatuses(w http.ResponseWriter, _ *http.Request) {
	if s.bridgeManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "Minecraft bridge runtime is unavailable."})
		return
	}
	statuses, err := s.bridgeManager.Statuses()
	if err != nil {
		http.Error(w, "bridge status unavailable", http.StatusInternalServerError)
		return
	}
	items := make([]bridge.Status, 0, len(statuses))
	for _, status := range statuses {
		items = append(items, status)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ServerName < items[j].ServerName })
	writeJSON(w, http.StatusOK, items)
}

func (s *server) bridgeStatus(w http.ResponseWriter, r *http.Request) {
	if s.bridgeManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "Minecraft bridge runtime is unavailable."})
		return
	}
	status, err := s.bridgeManager.Status(r.PathValue("id"))
	if err != nil {
		if errors.Is(err, bridge.ErrUnknownServer) {
			writeJSON(w, http.StatusNotFound, map[string]string{"message": "server not found"})
			return
		}
		http.Error(w, "bridge status unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *server) bridgeSend(w http.ResponseWriter, r *http.Request) {
	if s.bridgeManager == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "Minecraft bridge runtime is unavailable."})
		return
	}
	var body struct {
		Text string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" || len(text) > 2000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "text must be 1-2000 bytes"})
		return
	}
	if err := s.bridgeManager.SendToChat(r.PathValue("id"), text); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

func (s *server) rotateServerToken(w http.ResponseWriter, r *http.Request) {
	record, err := s.store.Get(r.Context(), "servers", r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	token, err := newServerToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "could not generate a server token"})
		return
	}
	detail, err := setServerToken(record.Detail, token)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "stored server config is invalid JSON"})
		return
	}
	record.Detail = detail
	if _, err := s.store.Save(r.Context(), record); err != nil {
		writeStoreError(w, err)
		return
	}
	if s.bridgeManager != nil {
		s.bridgeManager.DisconnectServer(record.ID)
	}
	s.logs.New("bridge").Warn("Minecraft server token rotated, active session closed: " + record.Name)
	writeJSON(w, http.StatusOK, map[string]string{"serverId": record.ID, "token": token})
}

func newServerToken() (string, error) {
	value := make([]byte, 24)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func setServerToken(detail, token string) (string, error) {
	values := make(map[string]json.RawMessage)
	if strings.TrimSpace(detail) != "" {
		if err := json.Unmarshal([]byte(detail), &values); err != nil {
			return detail, err
		}
	}
	encoded, err := json.Marshal(token)
	if err != nil {
		return detail, err
	}
	values["token"] = encoded
	result, err := json.Marshal(values)
	if err != nil {
		return detail, err
	}
	return string(result), nil
}

func serverToken(detail string) string {
	var values map[string]json.RawMessage
	if json.Unmarshal([]byte(detail), &values) != nil {
		return ""
	}
	for _, key := range []string{"token", "Token", "serverToken"} {
		raw, ok := values[key]
		if !ok {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil && value != "" {
			return value
		}
	}
	return ""
}

func ensureServerToken(detail string) (string, error) {
	if serverToken(detail) != "" {
		return detail, nil
	}
	token, err := newServerToken()
	if err != nil {
		return detail, err
	}
	return setServerToken(detail, token)
}
