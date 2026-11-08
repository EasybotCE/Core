// SPDX-License-Identifier: AGPL-3.0-only
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/EasybotCE/easybotce/internal/domain"
	"strings"
	"time"
)

type AccountBinding struct {
	ID             string `json:"id"`
	MCEndpointID   string `json:"mc_endpoint_id"`
	PlayerID       string `json:"player_id"`
	ChatEndpointID string `json:"chat_endpoint_id"`
	PlatformUserID string `json:"platform_user_id"`
	CreatedAt      int64  `json:"created_at"`
}
type PlayerState struct {
	EndpointID string  `json:"endpoint_id"`
	PlayerID   string  `json:"player_id"`
	PlayerName string  `json:"player_name"`
	Online     bool    `json:"online"`
	World      string  `json:"world"`
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Z          float64 `json:"z"`
	UpdatedAt  int64   `json:"updated_at"`
}

func (s *Store) CreateAccountChallenge(ctx context.Context, endpointID, playerID string, ttl time.Duration) (string, int64, error) {
	if !ValidID(endpointID) || !ValidID(playerID) || ttl < time.Minute || ttl > 15*time.Minute {
		return "", 0, ErrInvalid
	}
	e, err := s.GetEndpoint(ctx, endpointID)
	if err != nil {
		return "", 0, err
	}
	if e.Type != domain.MCServer {
		return "", 0, ErrInvalid
	}
	b := make([]byte, 8)
	if _, err = rand.Read(b); err != nil {
		return "", 0, err
	}
	code := strings.ToUpper(hex.EncodeToString(b))
	sum := sha256.Sum256([]byte(code))
	hash := hex.EncodeToString(sum[:])
	exp := time.Now().Add(ttl).Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, s.query("DELETE FROM ce_account_challenges WHERE endpoint_id=? AND player_id=?"), endpointID, playerID); err != nil {
		return "", 0, err
	}
	if _, err = tx.ExecContext(ctx, s.query("INSERT INTO ce_account_challenges(code_hash,endpoint_id,player_id,expires_at) VALUES(?,?,?,?)"), hash, endpointID, playerID, exp); err != nil {
		return "", 0, err
	}
	if err = tx.Commit(); err != nil {
		return "", 0, err
	}
	return code, exp, nil
}
func (s *Store) ClaimAccountChallenge(ctx context.Context, code, chatEndpointID, userID string) (AccountBinding, error) {
	if len(code) != 16 || !ValidID(chatEndpointID) || !ValidID(userID) {
		return AccountBinding{}, ErrInvalid
	}
	e, err := s.GetEndpoint(ctx, chatEndpointID)
	if err != nil {
		return AccountBinding{}, err
	}
	if e.Type != domain.ChatBot {
		return AccountBinding{}, ErrInvalid
	}
	sum := sha256.Sum256([]byte(strings.ToUpper(code)))
	hash := hex.EncodeToString(sum[:])
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AccountBinding{}, err
	}
	defer tx.Rollback()
	var mc, player string
	var expires int64
	err = tx.QueryRowContext(ctx, s.query("SELECT endpoint_id,player_id,expires_at FROM ce_account_challenges WHERE code_hash=?"), hash).Scan(&mc, &player, &expires)
	if errors.Is(err, sql.ErrNoRows) || err == nil && expires <= time.Now().Unix() {
		return AccountBinding{}, ErrNotFound
	}
	if err != nil {
		return AccountBinding{}, err
	}
	_, err = tx.ExecContext(ctx, s.query("DELETE FROM ce_account_challenges WHERE code_hash=?"), hash)
	if err != nil {
		return AccountBinding{}, err
	}
	b := AccountBinding{ID: NewID(), MCEndpointID: mc, PlayerID: player, ChatEndpointID: chatEndpointID, PlatformUserID: userID, CreatedAt: time.Now().Unix()}
	_, err = tx.ExecContext(ctx, s.query("INSERT INTO ce_account_bindings(id,mc_endpoint_id,player_id,chat_endpoint_id,platform_user_id,created_at) VALUES(?,?,?,?,?,?)"), b.ID, b.MCEndpointID, b.PlayerID, b.ChatEndpointID, b.PlatformUserID, b.CreatedAt)
	if err != nil {
		return AccountBinding{}, ErrConflict
	}
	if err = tx.Commit(); err != nil {
		return AccountBinding{}, err
	}
	return b, nil
}
func (s *Store) ListAccountBindings(ctx context.Context, endpoint, player string) ([]AccountBinding, error) {
	q := "SELECT id,mc_endpoint_id,player_id,chat_endpoint_id,platform_user_id,created_at FROM ce_account_bindings WHERE 1=1"
	args := []any{}
	if endpoint != "" {
		q += " AND (mc_endpoint_id=? OR chat_endpoint_id=?)"
		args = append(args, endpoint, endpoint)
	}
	if player != "" {
		q += " AND player_id=?"
		args = append(args, player)
	}
	q += " ORDER BY created_at DESC,id LIMIT 500"
	rows, err := s.db.QueryContext(ctx, s.query(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountBinding{}
	for rows.Next() {
		var b AccountBinding
		if err = rows.Scan(&b.ID, &b.MCEndpointID, &b.PlayerID, &b.ChatEndpointID, &b.PlatformUserID, &b.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Store) DeleteAccountBinding(ctx context.Context, id string) error {
	r, e := s.db.ExecContext(ctx, s.query("DELETE FROM ce_account_bindings WHERE id=?"), id)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) PutPlayerState(ctx context.Context, p PlayerState) error {
	if !ValidID(p.EndpointID) || !ValidID(p.PlayerID) || len([]rune(p.PlayerName)) < 1 || len([]rune(p.PlayerName)) > 128 || !utf8ValidBounded(p.World, 128) {
		return ErrInvalid
	}
	e, err := s.GetEndpoint(ctx, p.EndpointID)
	if err != nil {
		return err
	}
	if e.Type != domain.MCServer {
		return ErrInvalid
	}
	if p.UpdatedAt == 0 {
		p.UpdatedAt = time.Now().Unix()
	}
	on := 0
	if p.Online {
		on = 1
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, s.query("DELETE FROM ce_player_states WHERE endpoint_id=? AND player_id=?"), p.EndpointID, p.PlayerID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, s.query("INSERT INTO ce_player_states(endpoint_id,player_id,player_name,online,world,x,y,z,updated_at) VALUES(?,?,?,?,?,?,?,?,?)"), p.EndpointID, p.PlayerID, p.PlayerName, on, p.World, p.X, p.Y, p.Z, p.UpdatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) GetPlayerState(ctx context.Context, endpoint, player string) (PlayerState, error) {
	var p PlayerState
	var on int
	err := s.db.QueryRowContext(ctx, s.query("SELECT endpoint_id,player_id,player_name,online,world,x,y,z,updated_at FROM ce_player_states WHERE endpoint_id=? AND player_id=?"), endpoint, player).Scan(&p.EndpointID, &p.PlayerID, &p.PlayerName, &on, &p.World, &p.X, &p.Y, &p.Z, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.Online = on != 0
	return p, err
}
