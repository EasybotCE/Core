package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base32"
	"errors"
	"strings"
	"time"

	"easybot/internal/model"
	"github.com/google/uuid"
)

var (
	ErrCrossPlatformConfirmationRequired = model.ErrCrossPlatformConfirmationRequired
	ErrBindingLimit                      = model.ErrBindingLimit
	ErrChallengeInvalid                  = model.ErrChallengeInvalid
	ErrBindingConflict                   = model.ErrBindingConflict
	ErrBindingNotFound                   = model.ErrBindingNotFound
)

type Binding = model.Binding
type BindingChallenge = model.BindingChallenge

func (s *Store) CreateBindingChallenge(ctx context.Context, playerUUID, serverID, groupID string, ttl time.Duration, maxAttempts int) (BindingChallenge, error) {
	return s.StartBinding(ctx, playerUUID, "", serverID, ttl, maxAttempts)
}

// StartBinding reuses a live per-player challenge, matching the plugin workflow.
func (s *Store) StartBinding(ctx context.Context, playerUUID, playerName, serverID string, ttl time.Duration, maxAttempts int) (BindingChallenge, error) {
	playerUUID, playerName, serverID = strings.TrimSpace(playerUUID), strings.TrimSpace(playerName), strings.TrimSpace(serverID)
	if playerUUID == "" && playerName != "" {
		playerUUID = "name:" + strings.ToLower(playerName)
	}
	if playerUUID == "" || len(playerUUID) > 128 || ttl < time.Minute || ttl > 30*time.Minute || maxAttempts < 1 || maxAttempts > 10 {
		return BindingChallenge{}, errors.New("invalid binding challenge parameters")
	}
	now := time.Now().UTC()
	var result BindingChallenge
	err := s.db.QueryRowContext(ctx, s.query(`SELECT code,player_uuid,server_id,group_id,expires_at FROM binding_challenges WHERE player_uuid=? AND consumed_at IS NULL ORDER BY created_at DESC LIMIT 1`), playerUUID).Scan(&result.Code, &result.PlayerUUID, &result.ServerID, &result.GroupID, &result.ExpiresAt)
	if err == nil && now.Before(result.ExpiresAt) {
		result.PlayerName = playerName
		return result, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return BindingChallenge{}, err
	}
	for i := 0; i < 10; i++ {
		var random [3]byte
		if _, err = rand.Read(random[:]); err != nil {
			return BindingChallenge{}, err
		}
		code := strings.ToUpper(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(random[:]))[:4]
		expires := now.Add(ttl)
		_, err = s.db.ExecContext(ctx, s.query(`INSERT INTO binding_challenges(code,player_uuid,server_id,group_id,expires_at,max_attempts,attempts,created_at) VALUES(?,?,?,?,?,?,0,?)`), code, playerUUID, serverID, "", expires, maxAttempts, now)
		if err == nil {
			return BindingChallenge{Code: code, PlayerUUID: playerUUID, PlayerName: playerName, ServerID: serverID, ExpiresAt: expires}, nil
		}
	}
	return BindingChallenge{}, err
}

func (s *Store) ConfirmBinding(ctx context.Context, code, platformType, accountID, displayName, groupID string) (Binding, error) {
	code, platformType, accountID = strings.TrimSpace(strings.ToUpper(code)), strings.TrimSpace(strings.ToLower(platformType)), strings.TrimSpace(accountID)
	displayName = strings.TrimSpace(displayName)
	if code == "" || platformType == "" || accountID == "" || len(platformType) > 48 || len(accountID) > 128 || len(displayName) > 255 {
		return Binding{}, ErrChallengeInvalid
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Binding{}, err
	}
	defer tx.Rollback()
	var playerUUID, serverID, allowedGroup string
	var expires time.Time
	var attempts, maxAttempts int
	var consumed sql.NullTime
	query := `SELECT player_uuid,server_id,group_id,expires_at,attempts,max_attempts,consumed_at FROM binding_challenges WHERE code=?`
	if s.dialect != "sqlite" {
		query += ` FOR UPDATE`
	}
	err = tx.QueryRowContext(ctx, s.query(query), code).Scan(&playerUUID, &serverID, &allowedGroup, &expires, &attempts, &maxAttempts, &consumed)
	if err != nil || consumed.Valid || !time.Now().UTC().Before(expires) || attempts >= maxAttempts {
		return Binding{}, ErrChallengeInvalid
	}
	// Increment attempts for every well-formed attempt. A uniqueness conflict does
	// not reveal which identity already owns the platform account.
	if _, err = tx.ExecContext(ctx, s.query(`UPDATE binding_challenges SET attempts=attempts+1 WHERE code=? AND consumed_at IS NULL`), code); err != nil {
		return Binding{}, err
	}
	reject := func(reason error) (Binding, error) {
		if commitErr := tx.Commit(); commitErr != nil {
			return Binding{}, commitErr
		}
		return Binding{}, reason
	}
	var samePlayerPlatform int
	err = tx.QueryRowContext(ctx, s.query(`SELECT COUNT(*) FROM player_bindings WHERE player_uuid=? AND platform_type=?`), playerUUID, platformType).Scan(&samePlayerPlatform)
	if err != nil {
		return Binding{}, err
	}
	if samePlayerPlatform > 0 {
		return reject(ErrBindingConflict)
	}
	var platformCount int
	err = tx.QueryRowContext(ctx, s.query(`SELECT COUNT(DISTINCT platform_type) FROM player_bindings WHERE player_uuid=?`), playerUUID).Scan(&platformCount)
	if err != nil {
		return Binding{}, err
	}
	if platformCount > 0 {
		return reject(ErrCrossPlatformConfirmationRequired)
	}
	var accountPlayers int
	err = tx.QueryRowContext(ctx, s.query(`SELECT COUNT(DISTINCT player_uuid) FROM player_bindings WHERE platform_type=? AND account_id=?`), platformType, accountID).Scan(&accountPlayers)
	if err != nil {
		return Binding{}, err
	}
	if accountPlayers >= 4 {
		return reject(ErrBindingLimit)
	}
	now := time.Now().UTC()
	id := uuid.NewString()
	if _, err = tx.ExecContext(ctx, s.query(`INSERT INTO player_bindings(id,player_uuid,server_id,platform_type,account_id,display_name,bound_at) VALUES(?,?,?,?,?,?,?)`), id, playerUUID, serverID, platformType, accountID, displayName, now); err != nil {
		return Binding{}, ErrBindingConflict
	}
	if _, err = tx.ExecContext(ctx, s.query(`UPDATE binding_challenges SET consumed_at=? WHERE code=? AND consumed_at IS NULL`), now, code); err != nil {
		return Binding{}, err
	}
	if _, err = tx.ExecContext(ctx, s.query(`INSERT INTO binding_audit(id,action,player_uuid,platform_type,account_id,occurred_at,detail) VALUES(?,?,?,?,?,?,?)`), uuid.NewString(), "bind", playerUUID, platformType, accountID, now, ""); err != nil {
		return Binding{}, err
	}
	if err = tx.Commit(); err != nil {
		return Binding{}, err
	}
	return Binding{ID: id, PlayerUUID: playerUUID, ServerID: serverID, PlatformType: platformType, AccountID: accountID, DisplayName: displayName, BoundAt: now}, nil
}

func (s *Store) ListBindings(ctx context.Context) ([]Binding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,player_uuid,server_id,platform_type,account_id,display_name,bound_at FROM player_bindings ORDER BY bound_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Binding{}
	for rows.Next() {
		var b Binding
		if err = rows.Scan(&b.ID, &b.PlayerUUID, &b.ServerID, &b.PlatformType, &b.AccountID, &b.DisplayName, &b.BoundAt); err != nil {
			return nil, err
		}
		items = append(items, b)
	}
	return items, rows.Err()
}

func (s *Store) DeleteBinding(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var b Binding
	err = tx.QueryRowContext(ctx, s.query(`SELECT id,player_uuid,platform_type,account_id FROM player_bindings WHERE id=?`), id).Scan(&b.ID, &b.PlayerUUID, &b.PlatformType, &b.AccountID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrBindingNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.query(`DELETE FROM player_bindings WHERE id=?`), id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, s.query(`INSERT INTO binding_audit(id,action,player_uuid,platform_type,account_id,occurred_at,detail) VALUES(?,?,?,?,?,?,?)`), uuid.NewString(), "unbind", b.PlayerUUID, b.PlatformType, b.AccountID, time.Now().UTC(), ""); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) IsPlayerBound(ctx context.Context, playerUUID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, s.query(`SELECT COUNT(*) FROM player_bindings WHERE player_uuid=?`), playerUUID).Scan(&count)
	return count > 0, err
}

func (s *Store) BindingAudit(ctx context.Context, limit int) ([]map[string]any, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, s.query(`SELECT action,player_uuid,platform_type,account_id,occurred_at,detail FROM binding_audit ORDER BY occurred_at DESC LIMIT ?`), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var action, player, platform, account, detail string
		var at time.Time
		if err = rows.Scan(&action, &player, &platform, &account, &at, &detail); err != nil {
			return nil, err
		}
		items = append(items, map[string]any{"action": action, "playerUuid": player, "platformType": platform, "accountId": account, "occurredAt": at, "detail": detail})
	}
	return items, rows.Err()
}
