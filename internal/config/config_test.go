// SPDX-License-Identifier: AGPL-3.0-only
package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

const testToken = "0123456789abcdef0123456789abcdef"

func TestLoad(t *testing.T) {
	t.Parallel()
	env := map[string]string{"EASYBOTCE_ADMIN_TOKEN": testToken}
	load := func() (Config, error) {
		return Load(func(key string) (string, bool) { v, ok := env[key]; return v, ok })
	}
	cfg, err := load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "127.0.0.1:8080" || cfg.LogLevel != slog.LevelInfo || cfg.ShutdownTimeout != 10*time.Second || cfg.DatabaseDSN != "easybotce.db" {
		t.Fatalf("incorrect defaults: address=%s dsn=%q", cfg.ListenAddr, cfg.DatabaseDSN)
	}
	env["EASYBOTCE_LISTEN_ADDR"] = "[::1]:9000"
	env["EASYBOTCE_LOG_LEVEL"] = "debug"
	env["EASYBOTCE_SHUTDOWN_TIMEOUT"] = "2s"
	cfg, err = load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "[::1]:9000" || cfg.LogLevel != slog.LevelDebug || cfg.ShutdownTimeout != 2*time.Second {
		t.Fatal("environment overrides not applied")
	}
}

func TestLoadRejectsInvalidSettings(t *testing.T) {
	cases := []struct{ name, key, value string }{
		{"missing token", "EASYBOTCE_ADMIN_TOKEN", ""},
		{"short token", "EASYBOTCE_ADMIN_TOKEN", "short"},
		{"whitespace token", "EASYBOTCE_ADMIN_TOKEN", testToken + " "},
		{"non ascii token", "EASYBOTCE_ADMIN_TOKEN", testToken + "中"},
		{"oversized token", "EASYBOTCE_ADMIN_TOKEN", strings.Repeat("a", 257)},
		{"missing port", "EASYBOTCE_LISTEN_ADDR", "localhost"},
		{"zero port", "EASYBOTCE_LISTEN_ADDR", "localhost:0"},
		{"negative port", "EASYBOTCE_LISTEN_ADDR", "localhost:-1"},
		{"oversized port", "EASYBOTCE_LISTEN_ADDR", ":65536"},
		{"empty address", "EASYBOTCE_LISTEN_ADDR", ""},
		{"invalid level", "EASYBOTCE_LOG_LEVEL", "trace"},
		{"invalid duration", "EASYBOTCE_SHUTDOWN_TIMEOUT", "tomorrow"},
		{"zero duration", "EASYBOTCE_SHUTDOWN_TIMEOUT", "0s"},
		{"large duration", "EASYBOTCE_SHUTDOWN_TIMEOUT", "6m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{"EASYBOTCE_ADMIN_TOKEN": testToken}
			env[tc.key] = tc.value
			_, err := Load(func(key string) (string, bool) { v, ok := env[key]; return v, ok })
			if err == nil {
				t.Fatal("expected configuration error")
			}
			if strings.Contains(err.Error(), testToken) {
				t.Fatal("error disclosed secret")
			}
		})
	}
}

func TestDatabaseDSNSelection(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, driver, dsn string
		wantErr           bool
	}{
		{name: "sqlite default", driver: "sqlite", dsn: "", wantErr: false},
		{name: "sqlite explicit empty", driver: "sqlite", dsn: "", wantErr: true},
		{name: "mysql missing dsn", driver: "mysql", dsn: "", wantErr: true},
		{name: "postgres explicit dsn", driver: "postgres", dsn: "postgres://db/example", wantErr: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := map[string]string{
				"EASYBOTCE_ADMIN_TOKEN":     testToken,
				"EASYBOTCE_DATABASE_DRIVER": tc.driver,
			}
			if tc.name != "sqlite default" {
				env["EASYBOTCE_DATABASE_DSN"] = tc.dsn
			}
			cfg, err := Load(func(key string) (string, bool) { v, ok := env[key]; return v, ok })
			if (err != nil) != tc.wantErr {
				t.Fatalf("Load() err=%v wantErr=%t", err, tc.wantErr)
			}
			if err == nil {
				want := tc.dsn
				if tc.name == "sqlite default" {
					want = "easybotce.db"
				}
				if cfg.DatabaseDSN != want {
					t.Fatalf("DatabaseDSN=%q want %q", cfg.DatabaseDSN, want)
				}
			}
		})
	}
}
