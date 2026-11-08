// SPDX-License-Identifier: AGPL-3.0-only
package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/EasybotCE/easybotce/internal/config"
)

func TestServeShutdown(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	client := http.Client{Timeout: 2 * time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatalf("status=%d", response.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown timed out")
	}
	conn, err := net.DialTimeout("tcp", listener.Addr().String(), 100*time.Millisecond)
	if err == nil {
		conn.Close()
		t.Fatal("listener is still open")
	}
}

func TestServeForcesCloseAfterDeadline(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release }), 20*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	client := http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			response.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("handler not reached")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("want deadline error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("forced shutdown did not complete")
	}
	<-requestDone
}

func TestRunRejectsConfigAndCancelledContext(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := Run(context.Background(), config.Config{}, logger); err == nil {
		t.Fatal("invalid config accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := config.Config{ListenAddr: "127.0.0.1:8080", AdminToken: "0123456789abcdef0123456789abcdef", ShutdownTimeout: time.Second}
	if err := Run(ctx, cfg, logger); !errors.Is(err, context.Canceled) {
		t.Fatalf("want cancelled context, got %v", err)
	}
}
