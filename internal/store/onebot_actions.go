// SPDX-License-Identifier: AGPL-3.0-only
package store

import (
	"context"
	"database/sql"
	"errors"
)

type OneBotAction struct {
	ID         string `json:"id"`
	EndpointID string `json:"endpoint_id"`
	Status     string `json:"status"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	Retcode    *int   `json:"retcode,omitempty"`
}

// SaveOneBotAction inserts or updates a single action, preserving its creation time.
func (s *Store) SaveOneBotAction(ctx context.Context, a OneBotAction) error {
	if !ValidID(a.ID) || !ValidID(a.EndpointID) || (a.Status != "queued" && a.Status != "sent" && a.Status != "succeeded" && a.Status != "failed" && a.Status != "timeout") {
		return ErrInvalid
	}
	query := `INSERT INTO ce_onebot_actions(id,endpoint_id,status,created_at,updated_at,retcode) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,updated_at=excluded.updated_at,retcode=excluded.retcode`
	if s.dialect == "mysql" {
		query = `INSERT INTO ce_onebot_actions(id,endpoint_id,status,created_at,updated_at,retcode) VALUES(?,?,?,?,?,?) ON DUPLICATE KEY UPDATE status=VALUES(status),updated_at=VALUES(updated_at),retcode=VALUES(retcode)`
	}
	_, err := s.db.ExecContext(ctx, s.query(query), a.ID, a.EndpointID, a.Status, a.CreatedAt, a.UpdatedAt, a.Retcode)
	if err != nil {
		return errors.New("cannot persist OneBot action")
	}
	return nil
}

func (s *Store) GetOneBotAction(ctx context.Context, id string) (OneBotAction, error) {
	var a OneBotAction
	err := s.db.QueryRowContext(ctx, s.query(`SELECT id,endpoint_id,status,created_at,updated_at,retcode FROM ce_onebot_actions WHERE id=?`), id).Scan(&a.ID, &a.EndpointID, &a.Status, &a.CreatedAt, &a.UpdatedAt, &a.Retcode)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	if err != nil {
		return a, errors.New("cannot read OneBot action")
	}
	return a, nil
}

func (s *Store) ListOneBotActions(ctx context.Context, endpointID string, before int64, beforeID string, limit int) ([]OneBotAction, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	if beforeID != "" && (before < 1 || !ValidID(beforeID)) {
		return nil, ErrInvalid
	}
	query := `SELECT id,endpoint_id,status,created_at,updated_at,retcode FROM ce_onebot_actions WHERE (?='' OR endpoint_id=?) AND (?=0 OR created_at<? OR (created_at=? AND id<?)) ORDER BY created_at DESC,id DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, s.query(query), endpointID, endpointID, before, before, before, beforeID, limit)
	if err != nil {
		return nil, errors.New("cannot list OneBot actions")
	}
	defer rows.Close()
	result := make([]OneBotAction, 0)
	for rows.Next() {
		var a OneBotAction
		if err := rows.Scan(&a.ID, &a.EndpointID, &a.Status, &a.CreatedAt, &a.UpdatedAt, &a.Retcode); err != nil {
			return nil, errors.New("cannot list OneBot actions")
		}
		result = append(result, a)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("cannot list OneBot actions")
	}
	return result, nil
}

// RecoverOneBotActions marks work whose connection vanished during a Hub restart
// as timeout: its platform-side outcome is unknowable and must not be retried blindly.
func (s *Store) RecoverOneBotActions(ctx context.Context, now int64) error {
	_, err := s.db.ExecContext(ctx, s.query(`UPDATE ce_onebot_actions SET status='timeout',updated_at=? WHERE status IN ('queued','sent')`), now)
	if err != nil {
		return errors.New("cannot recover OneBot actions")
	}
	return nil
}
