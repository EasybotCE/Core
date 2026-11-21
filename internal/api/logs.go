package api

import (
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"easybot/internal/runtime"
	"easybot/internal/store"
)

func (s *server) recentLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 {
		limit = 100
	}
	if limit < 1 || limit > 500 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "limit must be between 1 and 500"})
		return
	}
	offset := 0
	var err error
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
	}
	if err != nil || offset < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "offset must be a non-negative integer"})
		return
	}
	filter := store.LogFilter{Level: r.URL.Query().Get("level"), Component: r.URL.Query().Get("component"), RequestID: r.URL.Query().Get("requestId"), Query: runtime.NormalizeLogFilterText(r.URL.Query().Get("q")), Limit: limit, Offset: offset}
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
	items, total, err := s.store.ListLogs(r.Context(), filter)
	if err != nil {
		http.Error(w, "log query failed", http.StatusInternalServerError)
		return
	}
	if offset == 0 {
		seen := make(map[string]bool, len(items))
		memoryOnly := make([]string, 0)
		for _, item := range items {
			seen[item.ID] = true
		}
		for _, item := range s.logs.Recent(500) {
			if seen[item.ID] || (filter.Level != "" && item.Level != filter.Level) || (filter.Component != "" && item.Source != filter.Component) || (filter.RequestID != "" && item.RequestID != filter.RequestID) || (filter.Query != "" && !strings.Contains(strings.ToLower(item.Message), strings.ToLower(filter.Query))) || (filter.From != nil && item.Time.Before(*filter.From)) || (filter.To != nil && !item.Time.Before(*filter.To)) {
				continue
			}
			items = append(items, item)
			seen[item.ID] = true
			memoryOnly = append(memoryOnly, item.ID)
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].Time.Equal(items[j].Time) {
				return items[i].ID > items[j].ID
			}
			return items[i].Time.After(items[j].Time)
		})
		if len(items) > limit {
			items = items[:limit]
		}
		persistedMemory, err := s.store.CountLogIDs(r.Context(), memoryOnly)
		if err != nil {
			http.Error(w, "log query failed", http.StatusInternalServerError)
			return
		}
		total += len(memoryOnly) - persistedMemory
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *server) logStatus(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetLogSettings(r.Context())
	if err != nil {
		http.Error(w, "log settings unavailable", http.StatusInternalServerError)
		return
	}
	status := map[string]any{"persistent": settings.Persistent, "retentionDays": settings.RetentionDays, "maxRows": settings.MaxRows, "updatedAt": settings.UpdatedAt, "droppedWrites": uint64(0), "failedWrites": uint64(0)}
	if s.logWriter != nil {
		status["droppedWrites"] = s.logWriter.Dropped()
		status["failedWrites"] = s.logWriter.Failed()
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *server) updateLogSettings(w http.ResponseWriter, r *http.Request) {
	var settings store.LogSettings
	if !decode(w, r, &settings) {
		return
	}
	if settings.RetentionDays < 1 || settings.RetentionDays > 3650 || settings.MaxRows < 1000 || settings.MaxRows > 10000000 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "retentionDays must be 1-3650 and maxRows must be 1000-10000000"})
		return
	}
	if err := s.store.SaveLogSettings(r.Context(), settings); err != nil {
		http.Error(w, "log settings update failed", http.StatusInternalServerError)
		return
	}
	if s.logWriter != nil {
		s.logWriter.SetEnabled(settings.Persistent)
	}
	s.logs.New("settings").Info("Runtime log retention settings updated")
	updated, err := s.store.GetLogSettings(r.Context())
	if err != nil {
		http.Error(w, "log settings unavailable", http.StatusInternalServerError)
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
