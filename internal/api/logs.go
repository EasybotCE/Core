package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"easybot/internal/application"
	"easybot/internal/store"
)

func (s *server) recentLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset := 0
	var err error
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "offset must be a non-negative integer"})
		return
	}
	filter := store.LogFilter{Level: r.URL.Query().Get("level"), Component: r.URL.Query().Get("component"), RequestID: r.URL.Query().Get("requestId"), Query: r.URL.Query().Get("q"), Limit: limit, Offset: offset}
	if raw := r.URL.Query().Get("from"); raw != "" {
		value, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "from must be an RFC3339 timestamp"})
			return
		}
		filter.From = &value
	}
	if raw := r.URL.Query().Get("to"); raw != "" {
		value, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "to must be an RFC3339 timestamp"})
			return
		}
		filter.To = &value
	}
	page, err := s.logService.List(r.Context(), filter)
	if err != nil {
		if errors.Is(err, application.ErrLogLimit) || errors.Is(err, application.ErrLogOffset) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		} else {
			http.Error(w, "log query failed", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (s *server) logStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.logService.Status(r.Context())
	if err != nil {
		http.Error(w, "log settings unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *server) updateLogSettings(w http.ResponseWriter, r *http.Request) {
	var settings store.LogSettings
	if !decode(w, r, &settings) {
		return
	}
	updated, err := s.logService.UpdateSettings(r.Context(), settings)
	if err != nil {
		switch {
		case errors.Is(err, application.ErrLogSettingsInvalid):
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": application.ErrLogSettingsInvalid.Error()})
		case errors.Is(err, application.ErrLogSettingsUnavailable):
			http.Error(w, "log settings unavailable", http.StatusInternalServerError)
		default:
			http.Error(w, "log settings update failed", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func levelFor(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "error":
		return slog.LevelError
	case "warn":
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}
