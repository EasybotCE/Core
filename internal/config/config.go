// SPDX-License-Identifier: AGPL-3.0-only
// Package config loads bootstrap settings, not domain configuration.
package config

import (
	"errors"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseDriver  string
	DatabaseDSN     string
	ListenAddr      string
	AdminToken      string
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
}

// Load uses a supplied environment lookup to keep tests independent of the host.
// A token is mandatory; the service never starts with authentication disabled.
func Load(lookup func(string) (string, bool)) (Config, error) {
	return LoadFile("", lookup)
}

// LoadFile applies defaults, an explicitly selected YAML file, then environment
// overrides. An empty path disables file loading; there is no auto-discovery.
func LoadFile(path string, lookup func(string) (string, bool)) (Config, error) {
	values := map[string]string{
		"listen_addr":      "127.0.0.1:8080",
		"database_driver":  "sqlite",
		"database_dsn":     "",
		"log_level":        "info",
		"shutdown_timeout": "10s",
	}
	dsnExplicit := false
	if path != "" {
		fileValues, err := readFile(path)
		if err != nil {
			return Config{}, err
		}
		for key, value := range fileValues {
			values[key] = value
		}
		_, dsnExplicit = fileValues["database_dsn"]
	}
	for key := range bootstrapKeys {
		if value, ok := lookup("EASYBOTCE_" + strings.ToUpper(key)); ok {
			values[key] = value
			if key == "database_dsn" {
				dsnExplicit = true
			}
		}
	}
	if !dsnExplicit && values["database_driver"] == "sqlite" {
		values["database_dsn"] = "easybotce.db"
	}
	if dsnExplicit && strings.TrimSpace(values["database_dsn"]) == "" {
		return Config{}, errors.New("EASYBOTCE_DATABASE_DSN must not be empty when explicitly configured")
	}
	cfg := Config{ListenAddr: values["listen_addr"], AdminToken: values["admin_token"], DatabaseDriver: values["database_driver"], DatabaseDSN: values["database_dsn"]}
	switch strings.ToLower(values["log_level"]) {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "info":
		cfg.LogLevel = slog.LevelInfo
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		return Config{}, errors.New("EASYBOTCE_LOG_LEVEL must be debug, info, warn or error")
	}
	duration, err := time.ParseDuration(values["shutdown_timeout"])
	if err != nil {
		return Config{}, errors.New("EASYBOTCE_SHUTDOWN_TIMEOUT must be a duration such as 10s")
	}
	cfg.ShutdownTimeout = duration
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Database returns defaults for programmatic callers as well as bootstrap loading.
func (cfg Config) Database() (string, string) {
	driver, dsn := cfg.DatabaseDriver, cfg.DatabaseDSN
	if driver == "" {
		driver = "sqlite"
	}
	if dsn == "" && driver == "sqlite" {
		dsn = "easybotce.db"
	}
	return driver, dsn
}

func (cfg Config) Validate() error {
	driver, dsn := cfg.Database()
	switch driver {
	case "sqlite", "mysql", "postgres":
	default:
		return errors.New("EASYBOTCE_DATABASE_DRIVER must be sqlite, mysql or postgres")
	}
	if strings.TrimSpace(dsn) == "" {
		return errors.New("EASYBOTCE_DATABASE_DSN is required")
	}

	_, port, err := net.SplitHostPort(cfg.ListenAddr)
	if err != nil {
		return errors.New("EASYBOTCE_LISTEN_ADDR must be host:port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("EASYBOTCE_LISTEN_ADDR port must be between 1 and 65535")
	}
	if len(cfg.AdminToken) < 32 || len(cfg.AdminToken) > 256 {
		return errors.New("EASYBOTCE_ADMIN_TOKEN must contain 32 to 256 visible ASCII characters")
	}
	for _, c := range cfg.AdminToken {
		if c < 33 || c > 126 {
			return errors.New("EASYBOTCE_ADMIN_TOKEN must contain only visible ASCII characters, without whitespace")
		}
	}
	if cfg.ShutdownTimeout <= 0 || cfg.ShutdownTimeout > 5*time.Minute {
		return errors.New("EASYBOTCE_SHUTDOWN_TIMEOUT must be greater than zero and at most 5m")
	}
	return nil
}
