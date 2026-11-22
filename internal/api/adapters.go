package api

import (
	"easybot/internal/application"
	"net/http"
)

func (s *server) adapterTypes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.adapterService.Types())
}

func (s *server) adapters(w http.ResponseWriter, r *http.Request) {
	items, err := s.adapterService.List(r.Context())
	if err != nil {
		http.Error(w, "database query failed", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *server) createAdapter(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AdapterType string `json:"adapterType"`
		DisplayName string `json:"displayName"`
		ConfigJSON  string `json:"configJson"`
	}
	if !decode(w, r, &body) {
		return
	}
	item, err := s.adapterService.Create(r.Context(), body.AdapterType, body.DisplayName, body.ConfigJSON)
	if err != nil {
		writeAdapterConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *server) updateAdapter(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DisplayName string `json:"displayName"`
		ConfigJSON  string `json:"configJson"`
	}
	if !decode(w, r, &body) {
		return
	}
	item, err := s.adapterService.Update(r.Context(), r.PathValue("id"), body.DisplayName, body.ConfigJSON)
	if err != nil {
		writeAdapterConfigError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *server) adapterStart(w http.ResponseWriter, r *http.Request) {
	result, err := s.adapterService.Start(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAdapterError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *server) adapterStop(w http.ResponseWriter, r *http.Request) {
	result, err := s.adapterService.Stop(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAdapterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) adapterRestart(w http.ResponseWriter, r *http.Request) {
	result, err := s.adapterService.Restart(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAdapterError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *server) adapterTest(w http.ResponseWriter, r *http.Request) {
	result, err := s.adapterService.Test(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAdapterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) adapterStatus(w http.ResponseWriter, r *http.Request) {
	result, err := s.adapterService.Status(r.PathValue("id"))
	if err != nil {
		writeAdapterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) adapterCapabilities(w http.ResponseWriter, r *http.Request) {
	result, err := s.adapterService.Capabilities(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAdapterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) adapterGroups(w http.ResponseWriter, r *http.Request) {
	result, err := s.adapterService.Groups(r.Context(), r.PathValue("id"))
	if err != nil {
		writeAdapterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) adapterSend(w http.ResponseWriter, r *http.Request) {
	if !s.adapterService.Available() {
		writeAdapterError(w, application.ErrAdapterRuntimeUnavailable)
		return
	}
	var body struct {
		MessageType string `json:"messageType"`
		PeerID      string `json:"peerId"`
		Message     string `json:"message"`
	}
	if !decode(w, r, &body) {
		return
	}
	messageID, err := s.adapterService.Send(r.Context(), r.PathValue("id"), body.MessageType, body.PeerID, body.Message)
	if err != nil {
		writeAdapterError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"messageId": messageID})
}
