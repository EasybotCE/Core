package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"easybot/internal/runtime"
)

// Tests deliberately run serially: the legacy console writes to process stdout.
func captureConsole(t *testing.T, action func()) string {
	t.Helper()
	output, err := os.Create(filepath.Join(t.TempDir(), "console.txt"))
	if err != nil {
		t.Fatal(err)
	}
	previous, colors := os.Stdout, useColors
	os.Stdout, useColors = output, false
	defer func() { os.Stdout, useColors = previous, colors; output.Close() }()
	action()
	if _, err := output.Seek(0, 0); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestConsoleFormatAndLevel(t *testing.T) {
	output := captureConsole(t, func() {
		logger := consoleLogger(slog.LevelWarn).With("module", "api")
		logger.Info("must not appear")
		record := slog.NewRecord(time.Date(2026, 11, 22, 22, 52, 13, 0, time.Local), slog.LevelWarn, "[API] 请求失败\n下一行", 0)
		if err := logger.Handler().Handle(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	})
	if want := "[22:52:13][WARN][API]:请求失败 下一行\n"; output != want {
		t.Fatalf("console output = %q, want %q", output, want)
	}
}

func TestConsoleGroupLabels(t *testing.T) {
	output := captureConsole(t, func() {
		logger := runtime.NewLogs(10, consoleLogger()).New("daemon").WithGroup("DataBase").WithGroup("Migration")
		record := slog.NewRecord(time.Date(2026, 11, 22, 15, 26, 12, 0, time.Local), slog.LevelInfo, "初始化完成", 0)
		if err := logger.Handler().Handle(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	})
	if want := "[15:26:12][INFO][Core][DataBase][Migration]:初始化完成\n"; output != want {
		t.Fatalf("console output = %q, want %q", output, want)
	}
}
func TestConsoleModuleLabels(t *testing.T) {
	for module, want := range map[string]string{"": "Core", "daemon": "Core", "web": "WebUI", "logs": "Log", "config": "Config", "api": "API", "adapter": "Adapter", "bridge": "Bridge", "custom": "custom"} {
		if got := consoleModule(module); got != want {
			t.Errorf("module %q = %q, want %q", module, got, want)
		}
	}
}

func TestStartupURLsAndOneTimePassword(t *testing.T) {
	tests := []struct {
		address, want string
		tls           bool
	}{
		{address: "0.0.0.0:5001", want: "http://127.0.0.1:5001"},
		{address: "[::]:5001", tls: true, want: "https://127.0.0.1:5001"},
		{address: ":5001", want: "http://127.0.0.1:5001"},
		{address: "[2001:db8::1]:5001", want: "http://[2001:db8::1]:5001"},
	}
	const password = "first-run-password-unique"
	for _, tt := range tests {
		t.Run(tt.address, func(t *testing.T) {
			var persisted []runtime.LogEntry
			output := captureConsole(t, func() {
				logs := runtime.NewLogs(10, consoleLogger())
				logs.SetSink(func(entry runtime.LogEntry) { persisted = append(persisted, entry) })
				printStartup(logs.New("web"), tt.address, tt.tls, password, true)
				for _, entry := range logs.Recent(10) {
					if strings.Contains(entry.Message, password) {
						t.Fatal("password leaked into runtime history")
					}
				}
			})
			if !strings.Contains(output, tt.want) || strings.Count(output, password) != 1 {
				t.Fatalf("startup output: %s", output)
			}
			for _, entry := range persisted {
				if strings.Contains(entry.Message, password) {
					t.Fatal("password leaked into persisted logs")
				}
			}
			output = captureConsole(t, func() { printStartup(consoleLogger(), tt.address, tt.tls, password, false) })
			if strings.Contains(output, password) {
				t.Fatal("existing configuration printed password")
			}
		})
	}
}

func TestRunInvalidConfigurationReturnsFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("invalid JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EASYBOT_CONFIG", path)
	output := captureConsole(t, func() {
		if code := Run(); code != 1 {
			t.Fatalf("exit code = %d, want 1", code)
		}
	})
	if !strings.Contains(output, "启动配置读取失败") {
		t.Fatalf("missing startup failure diagnostic: %s", output)
	}
}
