package store

import (
	"context"

	"database/sql"
	"easybot/internal/model"
	"errors"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Record and Kinds remain aliases for existing storage callers.
type Record = model.Record

var Kinds = model.Kinds

func (s *Store) Get(ctx context.Context, kind, id string) (Record, error) {
	var item Record
	err := s.db.QueryRowContext(ctx, s.query(`SELECT id,kind,name,detail,enabled,created_at,updated_at FROM records WHERE kind=? AND id=?`), kind, id).
		Scan(&item.ID, &item.Kind, &item.Name, &item.Detail, &item.Enabled, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return Record{}, err
	}
	return item, nil
}

func (s *Store) List(ctx context.Context, kind string) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx, s.query(`SELECT id,kind,name,detail,enabled,created_at,updated_at FROM records WHERE kind=? ORDER BY updated_at DESC`), kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Record, 0)
	for rows.Next() {
		var item Record
		if err := rows.Scan(&item.ID, &item.Kind, &item.Name, &item.Detail, &item.Enabled, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) Save(ctx context.Context, item Record) (Record, error) {
	if !Kinds[item.Kind] {
		return item, errors.New("unsupported record kind")
	}
	if strings.TrimSpace(item.Name) == "" {
		return item, errors.New("name is required")
	}
	now := time.Now().UTC()
	if item.ID == "" {
		item.ID = uuid.NewString()
		item.CreatedAt, item.UpdatedAt = now, now
		_, err := s.db.ExecContext(ctx, s.query(`INSERT INTO records(id,kind,name,detail,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`), item.ID, item.Kind, item.Name, item.Detail, item.Enabled, item.CreatedAt, item.UpdatedAt)
		return item, err
	}
	item.UpdatedAt = now
	result, err := s.db.ExecContext(ctx, s.query(`UPDATE records SET name=?,detail=?,enabled=?,updated_at=? WHERE id=? AND kind=?`), item.Name, item.Detail, item.Enabled, item.UpdatedAt, item.ID, item.Kind)
	if err != nil {
		return item, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return item, err
	}
	if count == 0 {
		return item, sql.ErrNoRows
	}
	return item, nil
}

func (s *Store) Delete(ctx context.Context, kind, id string) error {
	result, err := s.db.ExecContext(ctx, s.query(`DELETE FROM records WHERE kind=? AND id=?`), kind, id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (s *Store) Count(ctx context.Context, kind string) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, s.query(`SELECT COUNT(*) FROM records WHERE kind=?`), kind).Scan(&count)
	return count, err
}
