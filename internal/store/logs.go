package store

import (
	"context"
	"strings"
	"time"

	"easybot/internal/runtime"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type LogSettings struct {
	Persistent    bool      `json:"persistent"`
	RetentionDays int       `json:"retentionDays"`
	MaxRows       int       `json:"maxRows"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func (s *Store) GetLogSettings(ctx context.Context) (LogSettings, error) {
	var settings LogSettings
	err := s.db.QueryRowContext(ctx, `SELECT persistent,retention_days,max_rows,updated_at FROM runtime_log_settings WHERE id=1`).Scan(&settings.Persistent, &settings.RetentionDays, &settings.MaxRows, &settings.UpdatedAt)
	return settings, err
}

func (s *Store) SaveLogSettings(ctx context.Context, settings LogSettings) error {
	_, err := s.db.ExecContext(ctx, s.query(`UPDATE runtime_log_settings SET persistent=?,retention_days=?,max_rows=?,updated_at=? WHERE id=1`), settings.Persistent, settings.RetentionDays, settings.MaxRows, time.Now().UTC())
	return err
}

type LogFilter struct {
	Level     string
	Component string
	RequestID string
	Query     string
	From      *time.Time
	To        *time.Time
	Limit     int
	Offset    int
}

func (s *Store) WriteLog(ctx context.Context, entry runtime.LogEntry) error {
	_, err := s.db.ExecContext(ctx, s.query(`INSERT INTO runtime_logs(id,occurred_at,level,component,message,request_id) VALUES(?,?,?,?,?,?)`), entry.ID, entry.Time.UTC(), entry.Level, entry.Source, runtime.Redact(entry.Message), entry.RequestID)
	return err
}

func (s *Store) ListLogs(ctx context.Context, filter LogFilter) ([]runtime.LogEntry, int, error) {
	where := []string{"1=1"}
	args := make([]any, 0, 6)
	if filter.Level != "" {
		where = append(where, "level=?")
		args = append(args, filter.Level)
	}
	if filter.Component != "" {
		where = append(where, "component=?")
		args = append(args, filter.Component)
	}
	if filter.RequestID != "" {
		where = append(where, "request_id=?")
		args = append(args, filter.RequestID)
	}
	if filter.From != nil {
		where = append(where, "occurred_at>=?")
		args = append(args, filter.From.UTC())
	}
	if filter.To != nil {
		where = append(where, "occurred_at<?")
		args = append(args, filter.To.UTC())
	}
	if filter.Query != "" {
		where = append(where, "message LIKE ?")
		args = append(args, "%"+filter.Query+"%")
	}
	clause := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, s.query("SELECT COUNT(*) FROM runtime_logs WHERE "+clause), args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if filter.Limit < 1 || filter.Limit > 500 {
		filter.Limit = 100
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.db.QueryContext(ctx, s.query("SELECT id,occurred_at,level,component,message,request_id FROM runtime_logs WHERE "+clause+" ORDER BY occurred_at DESC,id DESC LIMIT ? OFFSET ?"), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]runtime.LogEntry, 0)
	for rows.Next() {
		var entry runtime.LogEntry
		if err := rows.Scan(&entry.ID, &entry.Time, &entry.Level, &entry.Source, &entry.Message, &entry.RequestID); err != nil {
			return nil, 0, err
		}
		items = append(items, entry)
	}
	return items, total, rows.Err()
}

func (s *Store) CleanupLogs(ctx context.Context, before time.Time, maxRows int) (int64, error) {
	result, err := s.db.ExecContext(ctx, s.query(`DELETE FROM runtime_logs WHERE occurred_at<?`), before.UTC())
	if err != nil {
		return 0, err
	}
	removed, err := result.RowsAffected()
	if err != nil || maxRows < 1 {
		return removed, err
	}
	for {
		rows, queryErr := s.db.QueryContext(ctx, s.query(`SELECT id FROM runtime_logs ORDER BY occurred_at DESC,id DESC LIMIT ? OFFSET ?`), 500, maxRows)
		if queryErr != nil {
			return removed, queryErr
		}
		ids := make([]string, 0, 500)
		for rows.Next() {
			var id string
			if scanErr := rows.Scan(&id); scanErr != nil {
				rows.Close()
				return removed, scanErr
			}
			ids = append(ids, id)
		}
		queryErr = rows.Err()
		rows.Close()
		if queryErr != nil {
			return removed, queryErr
		}
		if len(ids) == 0 {
			return removed, nil
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		result, err = s.db.ExecContext(ctx, s.query(`DELETE FROM runtime_logs WHERE id IN (`+placeholders+`)`), stringArgs(ids)...)
		if err != nil {
			return removed, err
		}
		count, err := result.RowsAffected()
		removed += count
		if err != nil || len(ids) < 500 {
			return removed, err
		}
	}
}

func stringArgs(values []string) []any {
	args := make([]any, len(values))
	for i, value := range values {
		args[i] = value
	}
	return args
}
