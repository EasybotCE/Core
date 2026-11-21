package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type Store struct {
	db      *sql.DB
	dialect string
}

func Open(dsn string) (*Store, error) {
	dialect, source := "sqlite", dsn
	if source == "" {
		source = os.Getenv("EASYBOT_SQLITE_PATH")
		if source == "" {
			executable, err := os.Executable()
			if err != nil {
				return nil, fmt.Errorf("resolve database directory: %w", err)
			}
			dir := filepath.Dir(executable)
			// 保留开发目录 bin/easybotce 与 appdata 并列的布局。
			if filepath.Base(dir) == "bin" {
				dir = filepath.Dir(dir)
			}
			source = filepath.Join(dir, "appdata", "easybot.db")
		}
		if err := os.MkdirAll(filepath.Dir(source), 0750); err != nil {
			return nil, err
		}
		dialect = "sqlite"
	} else if strings.HasPrefix(source, "mysql://") {
		dialect = "mysql"
		parsed, err := url.Parse(source)
		if err != nil {
			return nil, err
		}
		password, _ := parsed.User.Password()
		source = fmt.Sprintf("%s:%s@tcp(%s)/%s?parseTime=true&charset=utf8mb4", parsed.User.Username(), password, parsed.Host, strings.TrimPrefix(parsed.Path, "/"))
	} else if strings.HasPrefix(source, "postgres://") || strings.HasPrefix(source, "postgresql://") {
		dialect = "postgres"
	} else if strings.Contains(source, "://") && !strings.HasPrefix(source, "file://") {
		return nil, fmt.Errorf("unsupported database URL scheme")
	}
	if dialect == "sqlite" {
		if err := os.MkdirAll(filepath.Dir(source), 0750); err != nil {
			return nil, err
		}
	}
	driver := map[string]string{"sqlite": "sqlite", "mysql": "mysql", "postgres": "pgx"}[dialect]
	db, err := sql.Open(driver, source)
	if err != nil {
		return nil, err
	}
	if dialect == "sqlite" {
		// SQLite 只有一个写入者。固定单连接可让 WAL / busy_timeout 这类
		// 连接级 PRAGMA 对每条语句生效，避免异步日志写入与管理 API 写入
		// 争抢锁产生 SQLITE_BUSY。
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	} else {
		db.SetMaxOpenConns(16)
		db.SetMaxIdleConns(4)
		db.SetConnMaxLifetime(30 * time.Minute)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err == nil {
		if dialect == "sqlite" {
			_, err = db.ExecContext(ctx, `PRAGMA journal_mode=WAL`)
			if err == nil {
				_, err = db.ExecContext(ctx, `PRAGMA busy_timeout=5000`)
			}
		}
	}
	instance := &Store{db: db, dialect: dialect}
	if err == nil {
		err = instance.migrate(ctx)
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return instance, nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY, applied_at TIMESTAMP NOT NULL)`); err != nil {
		return err
	}
	var version int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return err
	}
	if version < 1 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS records (
			id VARCHAR(36) PRIMARY KEY, kind VARCHAR(48) NOT NULL, name VARCHAR(255) NOT NULL,
			detail TEXT NOT NULL, enabled BOOLEAN NOT NULL DEFAULT TRUE,
			created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL)`); err == nil {
			_, err = tx.ExecContext(ctx, s.query(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`), 1, time.Now().UTC())
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		version = 1
	}
	if version < 2 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		statements := []string{
			`CREATE TABLE IF NOT EXISTS users (
				id VARCHAR(36) PRIMARY KEY, username VARCHAR(64) NOT NULL UNIQUE,
				password_hash VARCHAR(255) NOT NULL, created_at TIMESTAMP NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS sessions (
				token_hash VARCHAR(64) PRIMARY KEY, user_id VARCHAR(36) NOT NULL,
				created_at TIMESTAMP NOT NULL, expires_at TIMESTAMP NOT NULL,
				revoked_at TIMESTAMP NULL, FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE)`,
			`CREATE TABLE IF NOT EXISTS setup_state (
				id INTEGER PRIMARY KEY, setup_complete BOOLEAN NOT NULL DEFAULT FALSE)`,
		}
		for _, statement := range statements {
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				tx.Rollback()
				return err
			}
		}
		seed := `INSERT INTO setup_state(id, setup_complete) VALUES(1, FALSE)`
		switch s.dialect {
		case "sqlite":
			seed = `INSERT OR IGNORE INTO setup_state(id, setup_complete) VALUES(1, FALSE)`
		case "mysql":
			seed = `INSERT IGNORE INTO setup_state(id, setup_complete) VALUES(1, FALSE)`
		case "postgres":
			seed = `INSERT INTO setup_state(id, setup_complete) VALUES(1, FALSE) ON CONFLICT(id) DO NOTHING`
		}
		if _, err = tx.ExecContext(ctx, seed); err == nil {
			_, err = tx.ExecContext(ctx, s.query(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`), 2, time.Now().UTC())
		}
		if err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		version = 2
	}
	if version < 3 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		statements := []string{
			`CREATE TABLE IF NOT EXISTS runtime_logs (
				id VARCHAR(64) PRIMARY KEY, occurred_at TIMESTAMP NOT NULL, level VARCHAR(16) NOT NULL,
				component VARCHAR(96) NOT NULL, message TEXT NOT NULL, request_id VARCHAR(128) NOT NULL DEFAULT '')`,
			`CREATE INDEX idx_runtime_logs_occurred ON runtime_logs(occurred_at)`,
			`CREATE INDEX idx_runtime_logs_level_time ON runtime_logs(level, occurred_at)`,
			`CREATE INDEX idx_runtime_logs_component_time ON runtime_logs(component, occurred_at)`,
		}
		for _, statement := range statements {
			if _, err = tx.ExecContext(ctx, statement); err != nil {
				tx.Rollback()
				return err
			}
		}
		if _, err = tx.ExecContext(ctx, s.query(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`), 3, time.Now().UTC()); err != nil {
			tx.Rollback()
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		version = 3
	}
	if version < 4 {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS runtime_log_settings (
			id INTEGER PRIMARY KEY, persistent BOOLEAN NOT NULL, retention_days INTEGER NOT NULL,
			max_rows INTEGER NOT NULL, updated_at TIMESTAMP NOT NULL)`); err != nil {
			tx.Rollback()
			return err
		}
		seed := `INSERT INTO runtime_log_settings(id,persistent,retention_days,max_rows,updated_at) VALUES(1,TRUE,30,100000,?)`
		switch s.dialect {
		case "sqlite":
			seed = `INSERT OR IGNORE INTO runtime_log_settings(id,persistent,retention_days,max_rows,updated_at) VALUES(1,TRUE,30,100000,?)`
		case "mysql":
			seed = `INSERT IGNORE INTO runtime_log_settings(id,persistent,retention_days,max_rows,updated_at) VALUES(1,TRUE,30,100000,?)`
		case "postgres":
			seed = `INSERT INTO runtime_log_settings(id,persistent,retention_days,max_rows,updated_at) VALUES(1,TRUE,30,100000,?) ON CONFLICT(id) DO NOTHING`
		}
		if _, err = tx.ExecContext(ctx, s.query(seed), time.Now().UTC()); err != nil {
			tx.Rollback()
			return err
		}
		if _, err = tx.ExecContext(ctx, s.query(`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`), 4, time.Now().UTC()); err != nil {
			tx.Rollback()
			return err
		}
		return tx.Commit()
	}
	return nil
}

func (s *Store) Dialect() string { return s.dialect }
func (s *Store) Close() error    { return s.db.Close() }

// Get loads a single record by kind and id.
func (s *Store) query(statement string) string {
	if s.dialect != "postgres" {
		return statement
	}
	var out strings.Builder
	arg := 1
	for _, ch := range statement {
		if ch == '?' {
			fmt.Fprintf(&out, "$%d", arg)
			arg++
		} else {
			out.WriteRune(ch)
		}
	}
	return out.String()
}
