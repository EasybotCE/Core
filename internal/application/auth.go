package application

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"easybot/internal/model"
)

const SessionLifetime = 7 * 24 * time.Hour
const LoginRetryAfter = 5 * time.Minute

var (
	ErrInvalidPassword = errors.New("登录密码错误")
	ErrSessionCreate   = errors.New("无法创建登录会话")
	ErrSessionSave     = errors.New("无法保存登录会话")
)

// AuthRepository is the persistence boundary for authentication use cases.
type AuthRepository interface {
	SetupComplete(context.Context) (bool, error)
	SessionAdmin(context.Context, string) (model.Admin, bool, error)
	CreateSession(context.Context, string, string, time.Time) error
	RevokeSession(context.Context, string) error
}

type loginAttempt struct {
	start time.Time
	count int
}

type Session struct {
	Token   string
	Expires time.Time
}

// Auth owns credential verification, session lifecycle and the per-peer login budget.
// Peer addresses must come from the transport, never from untrusted forwarded headers.
type Auth struct {
	repository AuthRepository
	password   string
	userID     string
	mu         sync.Mutex
	attempts   map[string]loginAttempt
	now        func() time.Time
	newToken   func() (string, error)
}

func NewAuth(repository AuthRepository, password, userID string) *Auth {
	return &Auth{repository: repository, password: password, userID: userID,
		attempts: make(map[string]loginAttempt), now: time.Now, newToken: newSessionToken}
}

func (s *Auth) SetupComplete(ctx context.Context) (bool, error) {
	return s.repository.SetupComplete(ctx)
}

func (s *Auth) Authenticated(ctx context.Context, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	_, ok, err := s.repository.SessionAdmin(ctx, SessionHash(token))
	return ok && err == nil, err
}

// AllowLogin reserves one attempt before decoding credentials so malformed requests
// consume the same budget. A successful Login resets the peer's budget.
func (s *Auth) AllowLogin(remote string) bool {
	key := loginPeer(remote)
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for address, current := range s.attempts {
		if now.Sub(current.start) >= LoginRetryAfter {
			delete(s.attempts, address)
		}
	}
	if len(s.attempts) >= 4096 {
		if _, exists := s.attempts[key]; !exists {
			return false
		}
	}
	attempt := s.attempts[key]
	if now.Sub(attempt.start) >= LoginRetryAfter {
		attempt = loginAttempt{start: now}
	}
	if attempt.count >= 10 {
		return false
	}
	attempt.count++
	s.attempts[key] = attempt
	return true
}

// Login completes an attempt reserved with AllowLogin. Only a persisted session
// resets throttling; token generation or persistence failures retain the attempt.
func (s *Auth) Login(ctx context.Context, remote, password string) (Session, error) {
	if s.password == "" || subtle.ConstantTimeCompare([]byte(password), []byte(s.password)) != 1 {
		return Session{}, ErrInvalidPassword
	}
	token, err := s.newToken()
	if err != nil {
		return Session{}, fmt.Errorf("%w: %w", ErrSessionCreate, err)
	}
	session := Session{Token: token, Expires: s.now().UTC().Add(SessionLifetime)}
	if err := s.repository.CreateSession(ctx, s.userID, SessionHash(token), session.Expires); err != nil {
		return Session{}, fmt.Errorf("%w: %w", ErrSessionSave, err)
	}
	s.mu.Lock()
	delete(s.attempts, loginPeer(remote))
	s.mu.Unlock()
	return session, nil
}

func (s *Auth) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.repository.RevokeSession(ctx, SessionHash(token))
}

func loginPeer(remote string) string {
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return host
	}
	return remote
}

func newSessionToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// SessionHash is shared with session provisioning; raw tokens are never persisted.
func SessionHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
