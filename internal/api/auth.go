package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"easybot/internal/application"
)

func (s *server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") && r.Method != http.MethodOptions {
			switch r.URL.Path {
			case "/api/v1/health", "/api/v1/setup/status", "/api/v1/auth/session", "/api/v1/auth/login":
			default:
				cookie, err := r.Cookie(sessionCookie)
				if err != nil {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "authentication required"})
					return
				}
				if ok, err := s.auth.Authenticated(r.Context(), cookie.Value); err != nil || !ok {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "authentication required"})
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) setupStatus(w http.ResponseWriter, r *http.Request) {
	complete, err := s.auth.SetupComplete(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "setup status unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"initialized": complete})
}

func (s *server) authSession(w http.ResponseWriter, r *http.Request) {
	authenticated := false
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		authenticated, _ = s.auth.Authenticated(r.Context(), cookie.Value)
	}
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": authenticated, "authRequired": true})
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if !s.allowLogin(r.RemoteAddr) {
		w.Header().Set("Retry-After", strconv.Itoa(int(application.LoginRetryAfter.Seconds())))
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"message": "尝试次数过多，请稍后重试"})
		return
	}
	var body struct {
		Password string `json:"password"`
		Token    string `json:"token"`
	}
	if !decode(w, r, &body) {
		return
	}
	credential := body.Password
	if credential == "" {
		// Older clients called the configured management credential a token.
		credential = body.Token
	}
	session, err := s.auth.Login(r.Context(), r.RemoteAddr, credential)
	if err != nil {
		status, message := http.StatusInternalServerError, "无法保存登录会话"
		switch {
		case errors.Is(err, application.ErrInvalidPassword):
			status, message = http.StatusUnauthorized, "登录密码错误"
		case errors.Is(err, application.ErrSessionCreate):
			message = "无法创建登录会话"
		}
		writeJSON(w, status, map[string]string{"message": message})
		return
	}
	setSessionCookie(w, r, session.Token, session.Expires)
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if err := s.auth.Logout(r.Context(), cookie.Value); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "could not end session"})
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

// Keep the package-local entry point used by existing tests. All rate policy and
// state live in application.Auth; the lock only supports a zero-value test server.
func (s *server) allowLogin(remote string) bool {
	s.loginMu.Lock()
	if s.auth == nil {
		s.auth = application.NewAuth(s.store, "", "")
	}
	auth := s.auth
	s.loginMu.Unlock()
	return auth.AllowLogin(remote)
}

// Compatibility helper for existing API session fixtures.
func sessionHash(token string) string { return application.SessionHash(token) }

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: expires, MaxAge: int(time.Until(expires).Seconds()), HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
}
