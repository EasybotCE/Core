// SPDX-License-Identifier: AGPL-3.0-only
package store

import (
	"context"
	"strings"
	"time"
)

type PlayerPolicy struct {
	ID         string `json:"id"`
	EndpointID string `json:"endpoint_id"`
	PlayerID   string `json:"player_id"`
	Mode       string `json:"mode"`
	Action     string `json:"action"`
	Reason     string `json:"reason"`
	ExpiresAt  int64  `json:"expires_at"`
	CreatedAt  int64  `json:"created_at"`
}
type AuditEvent struct {
	ID        string `json:"id"`
	Actor     string `json:"actor"`
	Action    string `json:"action"`
	Resource  string `json:"resource"`
	CreatedAt int64  `json:"created_at"`
}

func (p PlayerPolicy) Validate() error {
	if !ValidID(p.ID) || !(p.EndpointID == "*" || ValidID(p.EndpointID)) || !ValidID(p.PlayerID) || (p.Mode != "allow" && p.Mode != "deny") || !utf8ValidBounded(p.Reason, 512) || p.ExpiresAt < 0 {
		return ErrInvalid
	}
	if p.Mode == "allow" {
		if p.Action != "none" {
			return ErrInvalid
		}
	} else if p.Action != "kick" && p.Action != "ban" && p.Action != "mute" {
		return ErrInvalid
	}
	return nil
}
func utf8ValidBounded(s string, n int) bool { return len([]rune(s)) <= n && strings.TrimSpace(s) != "" }
func (s *Store) PutPlayerPolicy(ctx context.Context, p PlayerPolicy) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = time.Now().Unix()
	}
	_, err := s.db.ExecContext(ctx, s.query("INSERT INTO ce_player_policies(id,endpoint_id,player_id,mode,action,reason,expires_at,created_at) VALUES(?,?,?,?,?,?,?,?)"), p.ID, p.EndpointID, p.PlayerID, p.Mode, p.Action, p.Reason, p.ExpiresAt, p.CreatedAt)
	if err != nil {
		return ErrConflict
	}
	return nil
}
func (s *Store) ListPlayerPolicies(ctx context.Context, endpointID, playerID string) ([]PlayerPolicy, error) {
	rows, err := s.db.QueryContext(ctx, s.query("SELECT id,endpoint_id,player_id,mode,action,reason,expires_at,created_at FROM ce_player_policies WHERE (endpoint_id=? OR endpoint_id='*') AND player_id=? ORDER BY created_at DESC,id LIMIT 256"), endpointID, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PlayerPolicy{}
	for rows.Next() {
		var p PlayerPolicy
		if err = rows.Scan(&p.ID, &p.EndpointID, &p.PlayerID, &p.Mode, &p.Action, &p.Reason, &p.ExpiresAt, &p.CreatedAt); err != nil {
			return nil, err
		}
		if p.ExpiresAt == 0 || p.ExpiresAt > time.Now().Unix() {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}
func (s *Store) DeletePlayerPolicy(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, s.query("DELETE FROM ce_player_policies WHERE id=?"), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) AddAudit(ctx context.Context, actor, action, resource string) error {
	if !ValidID(actor) || !ValidID(action) || resource == "" || len(resource) > 256 {
		return ErrInvalid
	}
	_, err := s.db.ExecContext(ctx, s.query("INSERT INTO ce_audit_events(id,actor,action,resource,created_at) VALUES(?,?,?,?,?)"), NewID(), actor, action, resource, time.Now().Unix())
	return err
}
func (s *Store) ListAudit(ctx context.Context, limit int) ([]AuditEvent, error) {
	if limit < 1 || limit > 500 {
		return nil, ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, s.query("SELECT id,actor,action,resource,created_at FROM ce_audit_events ORDER BY created_at DESC,id DESC LIMIT ?"), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var a AuditEvent
		if err = rows.Scan(&a.ID, &a.Actor, &a.Action, &a.Resource, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
