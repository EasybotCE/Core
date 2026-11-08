// SPDX-License-Identifier: AGPL-3.0-only
package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bootstrap.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func lookupMap(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) { v, ok := env[key]; return v, ok }
}

func TestLoadFilePrecedence(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "listen_addr: '[::1]:9001'\nadmin_token: '"+testToken+"'\nlog_level: WARN\nshutdown_timeout: 3s\n")
	cfg, err := LoadFile(path, lookupMap(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != "[::1]:9001" || cfg.AdminToken != testToken || cfg.LogLevel != slog.LevelWarn || cfg.ShutdownTimeout != 3*time.Second {
		t.Fatal("file values did not override defaults")
	}
	env := map[string]string{
		"EASYBOTCE_LISTEN_ADDR":      "127.0.0.1:9002",
		"EASYBOTCE_ADMIN_TOKEN":      strings.Repeat("b", 32),
		"EASYBOTCE_LOG_LEVEL":        "ERROR",
		"EASYBOTCE_SHUTDOWN_TIMEOUT": "4s",
	}
	cfg, err = LoadFile(path, lookupMap(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != env["EASYBOTCE_LISTEN_ADDR"] || cfg.AdminToken != env["EASYBOTCE_ADMIN_TOKEN"] || cfg.LogLevel != slog.LevelError || cfg.ShutdownTimeout != 4*time.Second {
		t.Fatal("environment did not override file values")
	}
	for key := range env {
		t.Run("empty override "+key, func(t *testing.T) {
			empty := map[string]string{key: ""}
			if _, err := LoadFile(path, lookupMap(empty)); err == nil {
				t.Fatal("present but empty environment must override file and fail validation")
			}
		})
	}
}

func TestLoadFileExampleAndEmptyMapping(t *testing.T) {
	t.Parallel()
	env := lookupMap(map[string]string{"EASYBOTCE_ADMIN_TOKEN": testToken})
	defaults, err := Load(env)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join("..", "..", "config", "example.yaml"), writeConfig(t, "{}\n")} {
		cfg, err := LoadFile(path, env)
		if err != nil {
			t.Fatal(err)
		}
		if cfg != defaults {
			t.Fatal("expected unchanged defaults and environment token")
		}
	}
}

func TestLoadFileRejectsInvalidDocuments(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"empty":                   "",
		"comments only":           "# no mapping\n",
		"bad syntax with secret":  "admin_token: [" + testToken,
		"duplicate":               "log_level: info\nlog_level: debug\n",
		"unknown":                 "database: sqlite\n",
		"wrong case":              "Log_level: info\n",
		"null":                    "log_level: null\n",
		"implicit number":         "admin_token: 12345678901234567890123456789012\n",
		"implicit boolean":        "log_level: true\n",
		"sequence root":           "- log_level\n",
		"scalar root":             "info\n",
		"null root":               "null\n",
		"nested value":            "log_level: {value: info}\n",
		"alias":                   "log_level: *missing\n",
		"anchor":                  "log_level: &level info\n",
		"root anchor":             "&root {}\n",
		"merge":                   "<<: {log_level: info}\n",
		"complex key":             "? [a,b]\n: info\n",
		"custom tag":              "log_level: !secret info\n",
		"tagged root":             "!secret {}\n",
		"multiple documents":      "{}\n---\n{}\n",
		"trailing empty document": "{}\n---\n",
		"invalid UTF-8":           "#\xff\n{}\n",
		"oversize":                "#" + strings.Repeat("x", maxFileBytes),
		"invalid address":         "listen_addr: 'bad'\n",
		"invalid log level":       "log_level: '" + testToken + "'\n",
		"invalid duration":        "shutdown_timeout: '" + testToken + "'\n",
		"zero duration":           "shutdown_timeout: '0s'\n",
		"invalid token":           "admin_token: 'short'\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, content)
			// Environment can replace values, but must not hide malformed YAML.
			env := map[string]string{"EASYBOTCE_ADMIN_TOKEN": testToken}
			if name == "invalid token" {
				env = nil
			}
			cfg, err := LoadFile(path, lookupMap(env))
			if err == nil {
				t.Fatal("expected configuration error")
			}
			if cfg != (Config{}) || strings.Contains(err.Error(), testToken) || strings.Contains(err.Error(), path) {
				t.Fatal("failed loading must not expose partial configuration or secrets")
			}
		})
	}
}

func TestLoadFileMissingAndBoundary(t *testing.T) {
	t.Parallel()
	env := lookupMap(map[string]string{"EASYBOTCE_ADMIN_TOKEN": testToken})
	if _, err := LoadFile(filepath.Join(t.TempDir(), testToken), env); err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatal("missing explicitly requested file must fail without leaking path")
	}
	// The byte limit is inclusive; comments count toward it.
	path := writeConfig(t, "{}\n#"+strings.Repeat("x", maxFileBytes-4))
	if _, err := LoadFile(path, env); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentOverridesInvalidFileValues(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, "log_level: not-a-level\nshutdown_timeout: not-a-duration\n")
	env := lookupMap(map[string]string{
		"EASYBOTCE_ADMIN_TOKEN":      testToken,
		"EASYBOTCE_LOG_LEVEL":        "info",
		"EASYBOTCE_SHUTDOWN_TIMEOUT": "10s",
	})
	if _, err := LoadFile(path, env); err != nil {
		t.Fatal("validate effective values only:", err)
	}
}
