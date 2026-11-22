package api

import (
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"easybot/internal/adapter/onebot11"
	"easybot/internal/application"
	"easybot/internal/bridge"
	"easybot/internal/config"
	"easybot/internal/runtime"
	"easybot/internal/store"
)

type server struct {
	adapterService *application.Adapters
	recordService  *application.Records
	serverService  *application.Servers
	auth           *application.Auth
	logService     *application.Logs
	store          *store.Store
	bindings       *application.Bindings
	logger         *slog.Logger
	logs           *runtime.Logs
	adapterManager *onebot11.Manager
	bridgeManager  *bridge.Manager
	loginMu        sync.Mutex
	loginAttempts  map[string]loginAttempt
}

// Retained only for existing package-local test fixtures; throttling state is owned by application.Auth.
type loginAttempt struct {
	start time.Time
	count int
}

const sessionCookie = "easybot_session"

func New(db *store.Store, logger *slog.Logger, logs *runtime.Logs, writers ...*runtime.AsyncLogWriter) http.Handler {
	return NewWithAdapterManager(db, logger, logs, nil, writers...)
}

func NewWithAdapterManager(db *store.Store, logger *slog.Logger, logs *runtime.Logs, adapters *onebot11.Manager, writers ...*runtime.AsyncLogWriter) http.Handler {
	return NewForListener(db, logger, logs, adapters, "", "", "", writers...)
}

func NewForListener(db *store.Store, logger *slog.Logger, logs *runtime.Logs, adapters *onebot11.Manager, address string, password string, userID string, writers ...*runtime.AsyncLogWriter) http.Handler {
	return NewForListenerWithRuntimes(db, logger, logs, adapters, nil, address, password, userID, writers...)
}

func NewForListenerWithRuntimes(db *store.Store, logger *slog.Logger, logs *runtime.Logs, adapters *onebot11.Manager, bridges *bridge.Manager, address string, password string, userID string, writers ...*runtime.AsyncLogWriter) http.Handler {
	s := &server{store: db, bindings: application.NewBindings(db), logger: logger, logs: logs, adapterManager: adapters, bridgeManager: bridges}
	var adapterRuntime application.AdapterRuntime
	if adapters != nil {
		adapterRuntime = adapters
	}
	var bridgeRuntime application.BridgeRuntime
	if bridges != nil {
		bridgeRuntime = bridges
	}
	s.adapterService = application.NewAdapters(db, adapterRuntime, logger)
	s.recordService = application.NewRecords(db)
	s.serverService = application.NewServers(db, bridgeRuntime, logs.New("bridge"))
	s.auth = application.NewAuth(db, password, userID)
	var writer application.LogWriteController
	if len(writers) > 0 && writers[0] != nil {
		writer = writers[0]
	}
	s.logService = application.NewLogs(db, logs, writer)
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	handler := s.security(s.cors(mux))
	allowedHosts := os.Getenv("EASYBOT_ALLOWED_HOSTS")
	return s.observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !config.AllowedHost(r.Host, allowedHosts) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "host not allowed"})
			return
		}
		handler.ServeHTTP(w, r)
	}))
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"name": "EasybotCE", "status": "running", "version": "0.1.0-dev", "database": s.store.Dialect()})
}

func (s *server) info(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"data": map[string]any{"app": map[string]string{"version": "0.1.0-dev", "type": "development"}, "database": s.store.Dialect(), "address": os.Getenv("EASYBOT_ADDR")}})
}

func (s *server) dashboard(w http.ResponseWriter, r *http.Request) {
	counts := make(map[string]int)
	for _, kind := range []string{"adapters", "servers", "players", "social-accounts", "whitelist", "blacklist", "commands"} {
		count, err := s.store.Count(r.Context(), kind)
		if err != nil {
			http.Error(w, "database query failed", http.StatusInternalServerError)
			return
		}
		counts[kind] = count
	}
	writeJSON(w, http.StatusOK, map[string]any{"counts": counts, "recent": s.logs.Recent(8), "uptime": time.Since(startedAt).Round(time.Second).String()})
}
