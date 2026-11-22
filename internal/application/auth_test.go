package application

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"easybot/internal/model"
)

type authRepositoryStub struct {
	setup                                              bool
	valid                                              bool
	err                                                error
	createdUser, createdHash, checkedHash, revokedHash string
	expires                                            time.Time
	createCalls                                        int
}

func (r *authRepositoryStub) SetupComplete(context.Context) (bool, error) { return r.setup, r.err }
func (r *authRepositoryStub) SessionAdmin(_ context.Context, hash string) (model.Admin, bool, error) {
	r.checkedHash = hash
	return model.Admin{}, r.valid, r.err
}
func (r *authRepositoryStub) CreateSession(_ context.Context, user, hash string, expires time.Time) error {
	r.createCalls++
	r.createdUser, r.createdHash, r.expires = user, hash, expires
	return r.err
}
func (r *authRepositoryStub) RevokeSession(_ context.Context, hash string) error {
	r.revokedHash = hash
	return r.err
}

func TestAuthSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	repo := &authRepositoryStub{setup: true, valid: true}
	auth := NewAuth(repo, "password", "admin-id")
	now := time.Date(2026, 11, 22, 14, 0, 0, 0, time.FixedZone("local", 3600))
	auth.now = func() time.Time { return now }
	if complete, err := auth.SetupComplete(ctx); err != nil || !complete {
		t.Fatalf("setup: %v, %v", complete, err)
	}
	if !auth.AllowLogin("192.0.2.1:1000") {
		t.Fatal("first attempt rejected")
	}
	session, err := auth.Login(ctx, "192.0.2.1:2000", "password")
	if err != nil {
		t.Fatal(err)
	}
	if len(session.Token) != 43 || repo.createdHash == session.Token || repo.createdHash != SessionHash(session.Token) || repo.createdUser != "admin-id" {
		t.Fatalf("raw token leaked or identity/hash invalid: %+v", repo)
	}
	if !repo.expires.Equal(now.Add(SessionLifetime)) || repo.expires.Location() != time.UTC {
		t.Fatalf("expiry: %v", repo.expires)
	}
	if len(auth.attempts) != 0 {
		t.Fatal("successful login must reset peer budget")
	}
	if ok, err := auth.Authenticated(ctx, session.Token); err != nil || !ok || repo.checkedHash != repo.createdHash {
		t.Fatalf("session lookup: %v %v", ok, err)
	}
	if err := auth.Logout(ctx, session.Token); err != nil || repo.revokedHash != repo.createdHash {
		t.Fatalf("revoke: %v", err)
	}
	repo.valid = false
	if ok, err := auth.Authenticated(ctx, session.Token); err != nil || ok {
		t.Fatalf("revoked/expired session accepted: %v %v", ok, err)
	}
	repo.err = errors.New("database unavailable")
	if ok, err := auth.Authenticated(ctx, session.Token); err == nil || ok {
		t.Fatalf("must fail closed: %v %v", ok, err)
	}
	if err := auth.Logout(ctx, session.Token); !errors.Is(err, repo.err) {
		t.Fatalf("revoke failure: %v", err)
	}
	if ok, err := auth.Authenticated(ctx, ""); err != nil || ok {
		t.Fatalf("empty session: %v %v", ok, err)
	}
	if err := auth.Logout(ctx, ""); err != nil {
		t.Fatalf("empty logout: %v", err)
	}
}

func TestAuthLoginFailuresKeepBudget(t *testing.T) {
	failure := errors.New("failure")
	for _, test := range []struct {
		name, configured, supplied string
		tokenErr, saveErr, want    error
		creates                    int
	}{
		{name: "wrong password", configured: "correct", supplied: "wrong", want: ErrInvalidPassword},
		{name: "empty configuration", supplied: "", want: ErrInvalidPassword},
		{name: "token failure", configured: "correct", supplied: "correct", tokenErr: failure, want: ErrSessionCreate},
		{name: "save failure", configured: "correct", supplied: "correct", saveErr: failure, want: ErrSessionSave, creates: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := &authRepositoryStub{err: test.saveErr}
			auth := NewAuth(repo, test.configured, "admin")
			auth.newToken = func() (string, error) { return "test-token", test.tokenErr }
			auth.AllowLogin("127.0.0.1:1000")
			_, err := auth.Login(context.Background(), "127.0.0.1:1001", test.supplied)
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
			if repo.createCalls != test.creates || auth.attempts["127.0.0.1"].count != 1 {
				t.Fatal("failure reset budget or touched persistence unexpectedly")
			}
			if (test.tokenErr != nil || test.saveErr != nil) && !errors.Is(err, failure) {
				t.Fatalf("cause lost: %v", err)
			}
		})
	}
}

func TestAuthThrottleWindowPeersAndCapacity(t *testing.T) {
	auth := NewAuth(nil, "", "")
	now := time.Date(2026, 11, 22, 14, 0, 0, 0, time.UTC)
	auth.now = func() time.Time { return now }
	for i := 0; i < 10; i++ {
		if !auth.AllowLogin(fmt.Sprintf("[2001:db8::1]:%d", 1000+i)) {
			t.Fatalf("attempt %d rejected", i+1)
		}
	}
	if auth.AllowLogin("[2001:db8::1]:9999") {
		t.Fatal("port change bypassed throttle")
	}
	if !auth.AllowLogin("192.0.2.2:1000") {
		t.Fatal("independent peer rejected")
	}
	now = now.Add(LoginRetryAfter - time.Nanosecond)
	if auth.AllowLogin("[2001:db8::1]:1000") {
		t.Fatal("budget expired early")
	}
	now = now.Add(time.Nanosecond)
	if !auth.AllowLogin("[2001:db8::1]:1000") || len(auth.attempts) != 1 {
		t.Fatal("window not renewed or stale peers retained")
	}
	auth.attempts = make(map[string]loginAttempt)
	for i := 0; i < 4096; i++ {
		auth.attempts[fmt.Sprint(i)] = loginAttempt{start: now}
	}
	if auth.AllowLogin("new-peer") {
		t.Fatal("peer capacity exceeded")
	}
	if !auth.AllowLogin("0") {
		t.Fatal("existing peer should retain budget at capacity")
	}
	now = now.Add(LoginRetryAfter)
	if !auth.AllowLogin("new-peer") || len(auth.attempts) != 1 {
		t.Fatal("expired capacity not reclaimed")
	}
}

func TestAuthConcurrentLoginBudget(t *testing.T) {
	auth := NewAuth(nil, "", "")
	var wg sync.WaitGroup
	accepted := make(chan bool, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); accepted <- auth.AllowLogin("127.0.0.1:1000") }()
	}
	wg.Wait()
	close(accepted)
	count := 0
	for ok := range accepted {
		if ok {
			count++
		}
	}
	if count != 10 {
		t.Fatalf("concurrent budget allowed %d requests", count)
	}
}
