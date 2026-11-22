package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"easybot/internal/store"
)

type logRepository interface {
	GetLogSettings(context.Context) (store.LogSettings, error)
	CleanupLogs(context.Context, time.Time, int) (int64, error)
}

func maintainLogs(ctx context.Context, db logRepository, logger *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			cleanupLogs(ctx, db, logger, now)
		}
	}
}

func cleanupLogs(parent context.Context, db logRepository, logger *slog.Logger, now time.Time) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	settings, err := db.GetLogSettings(ctx)
	if err != nil {
		logger.Error("日志设置读取失败", "error", err)
		return
	}
	removed, err := db.CleanupLogs(ctx, now.UTC().AddDate(0, 0, -settings.RetentionDays), settings.MaxRows)
	if err != nil {
		logger.Error("历史日志清理失败", "error", err)
		return
	}
	if removed > 0 {
		logger.Info(fmt.Sprintf("已清理 %d 条过期日志", removed))
	}
}
