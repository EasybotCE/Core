package api

import (
	"database/sql"
	"easybot/internal/store"
	"encoding/json"
	"errors"
	"net/http"
)

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid JSON body"})
		return false
	}
	return true
}

func writeStoreError(w http.ResponseWriter, err error) {
	var validation *store.ValidationError
	if errors.As(err, &validation) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "validation_error", "field": validation.Field, "message": validation.Message})
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": "record not found"})
		return
	}
	writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
