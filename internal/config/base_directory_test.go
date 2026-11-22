package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigPathUsesExecutableDirectory(t *testing.T) {
	executableDir := filepath.Join(t.TempDir(), "bin")
	elsewhere := t.TempDir()
	t.Chdir(elsewhere)
	for _, test := range []struct{ name, configured, want string }{
		{"default", "", filepath.Join(executableDir, "config.json")},
		{"relative override", "settings/custom.json", filepath.Join(executableDir, "settings/custom.json")},
		{"absolute override", filepath.Join(elsewhere, "explicit.json"), filepath.Join(elsewhere, "explicit.json")},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := configPath(executableDir, test.configured)
			if path != test.want {
				t.Fatalf("path = %q, want %q", path, test.want)
			}
			settings, _, err := LoadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if settings.Path != test.want {
				t.Fatalf("settings path = %q", settings.Path)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "config.json")); !os.IsNotExist(err) {
		t.Fatalf("working directory must not receive config: %v", err)
	}
}

func TestBaseDirectoryIsExecutableDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got, err := BaseDirectory()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Dir(executable) {
		t.Fatalf("got %q, want executable directory %q", got, filepath.Dir(executable))
	}
}
