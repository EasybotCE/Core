package api

import (
	"net/http"

	"easybot/internal/store"
)

func (s *server) listRecords(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if !store.Kinds[kind] {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "unknown record kind"})
		return
	}
	items, err := s.store.List(r.Context(), kind)
	if err != nil {
		http.Error(w, "database query failed", http.StatusInternalServerError)
		return
	}
	if kind == "adapters" {
		for i := range items {
			items[i] = publicAdapterRecord(items[i])
		}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *server) createRecord(w http.ResponseWriter, r *http.Request) {
	var item store.Record
	if !decode(w, r, &item) {
		return
	}
	if item.Kind == "servers" {
		detail, err := ensureServerToken(item.Detail)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "server config must be valid JSON"})
			return
		}
		item.Detail = detail
	}
	item, err := s.store.Save(r.Context(), item)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if item.Kind == "adapters" {
		item = publicAdapterRecord(item)
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *server) updateRecord(w http.ResponseWriter, r *http.Request) {
	var item store.Record
	if !decode(w, r, &item) {
		return
	}
	item.ID, item.Kind = r.PathValue("id"), r.PathValue("kind")
	if item.Kind == "servers" && serverToken(item.Detail) == "" {
		if existing, err := s.store.Get(r.Context(), "servers", item.ID); err == nil {
			if token := serverToken(existing.Detail); token != "" {
				if detail, err := setServerToken(item.Detail, token); err == nil {
					item.Detail = detail
				}
			}
		}
	}
	item, err := s.store.Save(r.Context(), item)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if item.Kind == "adapters" {
		item = publicAdapterRecord(item)
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *server) deleteRecord(w http.ResponseWriter, r *http.Request) {
	err := s.store.Delete(r.Context(), r.PathValue("kind"), r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
