package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"easybot/internal/store"
)

type logRepositoryStub struct {
	settings                store.LogSettings
	settingsErr, cleanupErr error
	calls                   int
	before                  time.Time
	maxRows                 int
	deadline                bool
	removed                 int64
}

func (r *logRepositoryStub) GetLogSettings(ctx context.Context) (store.LogSettings, error) {
	_, r.deadline = ctx.Deadline()
	return r.settings, r.settingsErr
}
func (r *logRepositoryStub) CleanupLogs(_ context.Context, before time.Time, maxRows int) (int64, error) {
	r.calls++
	r.before, r.maxRows = before, maxRows
	return r.removed, r.cleanupErr
}

func TestLogRetentionUsesStoredSettings(t *testing.T) {
	repo := &logRepositoryStub{settings: store.LogSettings{RetentionDays: 7, MaxRows: 300}, removed: 2}
	now := time.Date(2026, 11, 22, 1, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	var output bytes.Buffer
	cleanupLogs(context.Background(), repo, slog.New(slog.NewTextHandler(&output, nil)), now)
	if repo.calls != 1 || repo.maxRows != 300 || !repo.deadline || !repo.before.Equal(now.UTC().AddDate(0, 0, -7)) {
		t.Fatalf("incorrect cleanup: %+v", repo)
	}
	if !strings.Contains(output.String(), "已清理 2 条过期日志") {
		t.Fatalf("cleanup log = %s", output.String())
	}
}

func TestLogRetentionFailurePaths(t *testing.T) {
	for _, settingsFailure := range []bool{true, false} {
		repo := &logRepositoryStub{}
		want := "历史日志清理失败"
		if settingsFailure {
			repo.settingsErr = errors.New("read failure")
			want = "日志设置读取失败"
		} else {
			repo.cleanupErr = errors.New("cleanup failure")
		}
		var output bytes.Buffer
		cleanupLogs(context.Background(), repo, slog.New(slog.NewTextHandler(&output, nil)), time.Now())
		if !strings.Contains(output.String(), want) {
			t.Fatalf("failure not logged: %s", output.String())
		}
		if settingsFailure && repo.calls != 0 {
			t.Fatal("cleanup continued after settings failure")
		}
	}
}

func TestLogMaintenanceStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); maintainLogs(ctx, &logRepositoryStub{}, slog.Default()) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("maintenance did not stop on process cancellation")
	}
}
