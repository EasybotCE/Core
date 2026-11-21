package api

import (
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"easybot/internal/adapter/onebot11"
	"easybot/internal/bridge"
	"easybot/internal/config"
	"easybot/internal/runtime"
	"easybot/internal/store"
)

type server struct {
	loginPassword  string
	loginUserID    string
	store          *store.Store
	logger         *slog.Logger
	logs           *runtime.Logs
	logWriter      *runtime.AsyncLogWriter
	adapterManager *onebot11.Manager
	bridgeManager  *bridge.Manager
	loginMu        sync.Mutex
	loginAttempts  map[string]loginAttempt
}

type loginAttempt struct {
	start time.Time
	count int
}

const sessionCookie = "easybot_session"
const sessionLifetime = 7 * 24 * time.Hour

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
	s := &server{store: db, logger: logger, logs: logs, adapterManager: adapters, bridgeManager: bridges, loginAttempts: make(map[string]loginAttempt)}
	s.loginPassword = password
	s.loginUserID = userID
	if len(writers) > 0 {
		s.logWriter = writers[0]
	}
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

func staticDirectory() string {
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		for _, base := range []string{dir, filepath.Dir(dir)} {
			candidate := filepath.Join(base, "web", "dist")
			if info, err := os.Stat(filepath.Join(candidate, "index.html")); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	// go run 的临时可执行文件不与前端资源放在一起。
	return "web/dist"
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
