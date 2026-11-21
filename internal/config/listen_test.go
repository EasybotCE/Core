package config

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestLoadFileSecuresExistingPortableTemplate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	template, err := os.ReadFile("config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	// Include trailing whitespace to ensure rewriting also truncates the file.
	template = append(template, []byte(strings.Repeat(" ", 4096))...)
	if err := os.WriteFile(path, template, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	settings, generated, err := LoadFile(path)
	if err != nil || !generated || !regexp.MustCompile(`^\d{12}$`).MatchString(settings.Password) {
		t.Fatalf("load: generated=%v err=%v", generated, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Errorf("password file permissions = %o, want 600", info.Mode().Perm())
	}
	again, generated, err := LoadFile(path)
	if err != nil || generated || again.Password != settings.Password {
		t.Fatalf("reload: generated=%v err=%v", generated, err)
	}
}

func TestLoadFileCreatesAndPersistsDefaultPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	settings, created, err := LoadFile(path)
	if err != nil || !created {
		t.Fatalf("load: created=%v err=%v", created, err)
	}
	if !regexp.MustCompile(`^\d{12}$`).MatchString(settings.Password) {
		t.Fatalf("invalid password %q", settings.Password)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Auth struct {
			Password string `json:"password"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.Auth.Password != settings.Password {
		t.Fatalf("password not persisted: %s %v", data, err)
	}
	again, created, err := LoadFile(path)
	if err != nil || created || again.Password != settings.Password {
		t.Fatalf("second load: %#v created=%v err=%v", again, created, err)
	}
}

func TestLoadFileConfigOverridesAndLegacyRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"server":{"address":"localhost:5050","allowedHosts":"localhost"},"bridge":{"address":"127.0.0.1:26991"},"database":{"sqlitePath":"data.db"},"auth":{"password":"secret"},"logging":{"level":"warning"}}`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	settings, created, err := LoadFile(path)
	if err != nil || created || settings.Address != "localhost:5050" || settings.Password != "secret" || settings.LogLevel != slog.LevelWarn {
		t.Fatalf("settings %#v created=%v err=%v", settings, created, err)
	}
	if settings.SQLitePath != filepath.Join(filepath.Dir(path), "data.db") {
		t.Fatalf("path %q", settings.SQLitePath)
	}
	if err := os.WriteFile(path, []byte(`{"Kestrel":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadFile(path); err == nil {
		t.Fatal("legacy .NET config accepted")
	}
}

func TestLoadFileRejectsInvalid(t *testing.T) {
	for _, content := range []string{`{`, `{"server":{"address":"localhost"}}`, `{"logging":{"level":"trace"}}`} {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadFile(path); err == nil {
			t.Fatalf("accepted %s", content)
		}
	}
}

func TestAllowedHost(t *testing.T) {
	for _, test := range []struct {
		host, patterns string
		allowed        bool
	}{{"localhost:5001", "localhost;127.0.0.1", true}, {"[::1]:5001", "[::1]", true}, {"app.example.test:5001", "*.example.test", true}, {"example.test.evil:5001", "*.example.test", false}, {"evil.test:5001", "localhost", false}} {
		if got := AllowedHost(test.host, test.patterns); got != test.allowed {
			t.Errorf("AllowedHost(%q,%q)=%v", test.host, test.patterns, got)
		}
	}
}

func TestValidateListener(t *testing.T) {
	tests := []struct {
		name, address, cert, key string
		wantErr                  bool
	}{{"default", "127.0.0.1:5001", "", "", false}, {"TLS pair", "[::1]:5001", "cert.pem", "key.pem", false}, {"partial TLS", "127.0.0.1:5001", "cert.pem", "", true}, {"invalid", "localhost", "", "", true}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateListener(tt.address, tt.cert, tt.key); (err != nil) != tt.wantErr {
				t.Fatalf("error=%v", err)
			}
		})
	}
}
