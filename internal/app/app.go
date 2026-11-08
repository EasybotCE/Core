// SPDX-License-Identifier: AGPL-3.0-only
// Package app owns the daemon lifecycle, not individual feature state.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/EasybotCE/easybotce/internal/config"
	"github.com/EasybotCE/easybotce/internal/httpapi"
	"github.com/EasybotCE/easybotce/internal/session"
	"github.com/EasybotCE/easybotce/internal/store"
)

func Run(ctx context.Context, cfg config.Config, logger *slog.Logger) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	driver, dsn := cfg.Database()
	initCtx, initCancel := context.WithTimeout(ctx, 10*time.Second)
	db, err := store.Open(initCtx, driver, dsn)
	initCancel()
	if err != nil {
		return err
	}
	defer db.Close()
	sessions := session.New(db)
	defer sessions.Close()
	handler, err := httpapi.NewRuntimeHandler(cfg, logger, db, sessions)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	logger.Info("hub listening", "address", listener.Addr().String(), "milestone", "M1")
	return serve(ctx, listener, handler, cfg.ShutdownTimeout, logger)
}

func serve(ctx context.Context, listener net.Listener, handler http.Handler, timeout time.Duration, logger *slog.Logger) error {
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
		logger.Info("hub stopping")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			<-done
			return fmt.Errorf("shutdown: %w", err)
		}
		if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	}
}
