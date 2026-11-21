package runtime

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestModuleLogsRetainDiagnosticContextAndSink(t *testing.T) {
	var console bytes.Buffer
	logs := NewLogs(10, slog.New(slog.NewTextHandler(&console, nil)))
	var persisted []LogEntry
	logs.SetSink(func(entry LogEntry) { persisted = append(persisted, entry) })
	base := logs.New("adapter").With("request_id", "review-request", "adapter", "adapter-1")
	grouped := base.WithGroup("connection").With("server", "survival")
	grouped.WithGroup("attempt").Error("连接失败", "error", errors.New("connection refused"), "count", 2,
		slog.Group("peer", "address", "127.0.0.1:3001"), slog.Attr{})
	base.Info("下一次操作")
	items := logs.Recent(10)
	if len(items) != 2 || len(persisted) != 2 {
		t.Fatalf("history=%+v sink=%+v", items, persisted)
	}
	entry := items[1]
	for _, part := range []string{"连接失败", `"adapter"="adapter-1"`, `"connection.server"="survival"`, `"connection.attempt.error"="connection refused"`, `"connection.attempt.count"="2"`, `"connection.attempt.peer.address"="127.0.0.1:3001"`} {
		if !strings.Contains(entry.Message, part) {
			t.Errorf("missing %q in %s", part, entry.Message)
		}
	}
	if entry.RequestID != "review-request" || entry.Source != "adapter" || entry.Level != "error" || entry != persisted[0] {
		t.Fatalf("unexpected history or sink entry: %+v / %+v", entry, persisted[0])
	}
	if strings.Contains(items[0].Message, "connection.") {
		t.Fatalf("derived logger changed its parent: %+v", items[0])
	}
	if !strings.Contains(console.String(), "connection refused") {
		t.Fatalf("console lost error: %s", console.String())
	}
}

func TestModuleLogsRedactCredentialAttributes(t *testing.T) {
	logs := NewLogs(10, nil)
	logger := logs.New("test").With("password", "inherited secret with spaces")
	logger.WithGroup("auth").Info("访问失败", "accessToken", "token with spaces,;",
		"AppSecret", "private app secret", "dsn", "postgres://user:db-secret@host/db",
		slog.Group("credentials", "api_key", "nested secret"), "error", errors.New("password=error-secret rejected"))
	message := logs.Recent(1)[0].Message
	for _, secret := range []string{"inherited secret", "token with spaces", "private app secret", "db-secret", "nested secret", "error-secret"} {
		if strings.Contains(message, secret) {
			t.Errorf("credential %q leaked: %s", secret, message)
		}
	}
	if !strings.Contains(message, "访问失败") || !strings.Contains(message, "[REDACTED]") {
		t.Fatalf("missing event or redaction: %s", message)
	}
}

func TestLogBufferKeepsRecentEntriesNewestFirst(t *testing.T) {
	logs := NewLogBuffer(2)
	logs.Add("info", "test", "one")
	logs.Add("warn", "test", "two")
	logs.Add("error", "test", "three")
	items := logs.Recent(2)
	if len(items) != 2 || items[0].Message != "three" || items[1].Message != "two" {
		t.Fatalf("unexpected recent logs: %#v", items)
	}
}

func TestRedactSensitiveValues(t *testing.T) {
	got := Redact("Authorization: Bearer abc.def password=hunter2 dsn=postgres://user:pass@host/db challenge: 123456")
	for _, secret := range []string{"abc.def", "hunter2", "user:pass", "123456"} {
		if strings.Contains(got, secret) {
			t.Fatalf("sensitive value %q was not redacted: %s", secret, got)
		}
	}
}

type blockingLogWriter struct {
	release chan struct{}
	started chan struct{}
}

func (w blockingLogWriter) WriteLog(context.Context, LogEntry) error {
	select {
	case <-w.started:
	default:
		close(w.started)
	}
	<-w.release
	return nil
}

func TestAsyncLogWriterQueueIsBoundedAndNonBlocking(t *testing.T) {
	backend := blockingLogWriter{release: make(chan struct{}), started: make(chan struct{})}
	writer := NewAsyncLogWriter(backend, 1)
	writer.TryWrite(LogEntry{ID: "first"})
	<-backend.started
	writer.TryWrite(LogEntry{ID: "queued"})
	writer.TryWrite(LogEntry{ID: "overflow"})
	if writer.Dropped() != 1 {
		t.Fatalf("expected one dropped write, got %d", writer.Dropped())
	}
	close(backend.release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	writer.Close(ctx)
}
