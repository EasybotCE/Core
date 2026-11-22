package api

import (
	"easybot/internal/application"
	"errors"
	"net/http"
)

func (s *server) listRecords(w http.ResponseWriter, r *http.Request) {
	items, err := s.recordService.List(r.Context(), r.URL.Query().Get("kind"))
	if err != nil {
		var validation *application.ValidationError
		if errors.As(err, &validation) {
			writeStoreError(w, err)
		} else {
			http.Error(w, "database query failed", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, items)
}
func (s *server) createRecord(w http.ResponseWriter, r *http.Request) {
	var item application.Record
	if !decode(w, r, &item) {
		return
	}
	item, err := s.recordService.Create(r.Context(), item)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}
func (s *server) updateRecord(w http.ResponseWriter, r *http.Request) {
	var item application.Record
	if !decode(w, r, &item) {
		return
	}
	item.ID, item.Kind = r.PathValue("id"), r.PathValue("kind")
	item, err := s.recordService.Update(r.Context(), item)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *server) deleteRecord(w http.ResponseWriter, r *http.Request) {
	if err := s.recordService.Delete(r.Context(), r.PathValue("kind"), r.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
