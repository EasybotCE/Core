package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.localOnly && requestIsLocal(r) {
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/v1/") && r.Method != http.MethodOptions {
			switch r.URL.Path {
			case "/api/v1/health", "/api/v1/setup/status", "/api/v1/auth/session", "/api/v1/auth/login":
			default:
				cookie, err := r.Cookie(sessionCookie)
				if err != nil {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "authentication required"})
					return
				}
				if _, ok, err := s.store.SessionAdmin(r.Context(), sessionHash(cookie.Value)); err != nil || !ok {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "authentication required"})
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) setupStatus(w http.ResponseWriter, r *http.Request) {
	if s.localOnly && requestIsLocal(r) {
		writeJSON(w, http.StatusOK, map[string]bool{"initialized": true})
		return
	}
	complete, err := s.store.SetupComplete(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "setup status unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"initialized": complete})
}

func (s *server) authSession(w http.ResponseWriter, r *http.Request) {
	if s.localOnly && requestIsLocal(r) {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "authRequired": false})
		return
	}
	cookie, err := r.Cookie(sessionCookie)
	if err == nil {
		if _, ok, lookupErr := s.store.SessionAdmin(r.Context(), sessionHash(cookie.Value)); lookupErr == nil && ok {
			writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "authRequired": true})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": false, "authRequired": true})
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if !s.allowLogin(r.RemoteAddr) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"message": "尝试次数过多，请稍后重试"})
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	if subtle.ConstantTimeCompare([]byte(body.Password), []byte(s.loginPassword)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "登录密码错误"})
		return
	}
	token, err := newSessionToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "无法创建登录会话"})
		return
	}
	expires := time.Now().UTC().Add(sessionLifetime)
	if err := s.store.CreateSession(r.Context(), s.loginUserID, sessionHash(token), expires); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "无法保存登录会话"})
		return
	}
	setSessionCookie(w, r, token, expires)
	writeJSON(w, http.StatusOK, map[string]bool{"authenticated": true})
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if err := s.store.RevokeSession(r.Context(), sessionHash(cookie.Value)); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "could not end session"})
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) allowLogin(remote string) bool {
	key := remote
	if host, _, err := net.SplitHostPort(remote); err == nil {
		key = host
	}
	now := time.Now()
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	for address, current := range s.loginAttempts {
		if now.Sub(current.start) >= 5*time.Minute {
			delete(s.loginAttempts, address)
		}
	}
	if len(s.loginAttempts) >= 4096 {
		if _, exists := s.loginAttempts[key]; !exists {
			return false
		}
	}
	attempt := s.loginAttempts[key]
	if now.Sub(attempt.start) >= 5*time.Minute {
		attempt = loginAttempt{start: now}
	}
	if attempt.count >= 10 {
		return false
	}
	attempt.count++
	s.loginAttempts[key] = attempt
	return true
}

func remoteIsLoopback(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func requestIsLocal(r *http.Request) bool {
	if !remoteIsLoopback(r.RemoteAddr) || !hostIsLoopback(r.Host) {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || !hostIsLoopback(parsed.Host) {
			return false
		}
	}
	return true
}

func hostIsLoopback(value string) bool {
	host := value
	if parsedHost, _, err := net.SplitHostPort(value); err == nil {
		host = parsedHost
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.Trim(host, "[]")
	}
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func newSessionToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func sessionHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", Expires: expires, MaxAge: int(time.Until(expires).Seconds()), HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode})
}
