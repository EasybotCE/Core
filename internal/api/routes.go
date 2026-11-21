package api

import "net/http"

func (s *server) registerRoutes(mux *http.ServeMux) {
	s.registerSystemRoutes(mux)
	s.registerAuthRoutes(mux)
	s.registerRecordRoutes(mux)
	s.registerAdapterRoutes(mux)
	s.registerLogRoutes(mux)
	s.registerBridgeRoutes(mux)
	if s.bridgeManager != nil {
		mux.Handle("/bridge", s.bridgeManager)
	}
	mux.Handle("/", http.FileServer(http.Dir(staticDirectory())))
}

func (s *server) registerSystemRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/app/info", s.info)
	mux.HandleFunc("GET /api/v1/dashboard/data", s.dashboard)
}

func (s *server) registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/setup/status", s.setupStatus)
	mux.HandleFunc("GET /api/v1/auth/session", s.authSession)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	mux.HandleFunc("POST /api/v1/auth/logout", s.logout)
}

func (s *server) registerRecordRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/records", s.listRecords)
	mux.HandleFunc("POST /api/v1/records", s.createRecord)
	mux.HandleFunc("PUT /api/v1/records/{kind}/{id}", s.updateRecord)
	mux.HandleFunc("DELETE /api/v1/records/{kind}/{id}", s.deleteRecord)
}

func (s *server) registerAdapterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/adapter/types", s.adapterTypes)
	mux.HandleFunc("GET /api/v1/adapter/list", s.adapters)
	mux.HandleFunc("POST /api/v1/adapter/create", s.createAdapter)
	mux.HandleFunc("PUT /api/v1/adapter/{id}", s.updateAdapter)
	mux.HandleFunc("POST /api/v1/adapter/{id}/start", s.adapterStart)
	mux.HandleFunc("POST /api/v1/adapter/{id}/stop", s.adapterStop)
	mux.HandleFunc("POST /api/v1/adapter/{id}/test", s.adapterTest)
	mux.HandleFunc("POST /api/v1/adapter/{id}/restart", s.adapterRestart)
	mux.HandleFunc("GET /api/v1/adapter/{id}/status", s.adapterStatus)
	mux.HandleFunc("GET /api/v1/adapter/{id}/capabilities", s.adapterCapabilities)
	mux.HandleFunc("POST /api/v1/adapter/{id}/send", s.adapterSend)
	mux.HandleFunc("GET /api/v1/adapter/{id}/groups", s.adapterGroups)
}

func (s *server) registerLogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/logs", s.recentLogs)
	mux.HandleFunc("GET /api/v1/logs/status", s.logStatus)
	mux.HandleFunc("PUT /api/v1/logs/settings", s.updateLogSettings)
}

func (s *server) registerBridgeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/bridge/status", s.bridgeStatuses)
	mux.HandleFunc("GET /api/v1/bridge/{id}/status", s.bridgeStatus)
	mux.HandleFunc("POST /api/v1/bridge/{id}/send", s.bridgeSend)
	mux.HandleFunc("POST /api/v1/servers/{id}/token", s.rotateServerToken)
}
