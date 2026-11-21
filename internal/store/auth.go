package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type Admin struct {
	ID       string
	Username string
}

func (s *Store) SetupComplete(ctx context.Context) (bool, error) {
	var complete bool
	err := s.db.QueryRowContext(ctx, `SELECT setup_complete FROM setup_state WHERE id=1`).Scan(&complete)
	return complete, err
}

func (s *Store) CreateInitialAdmin(ctx context.Context, username, password, tokenHash string, expiresAt time.Time) (Admin, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return Admin{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Admin{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, s.query(`UPDATE setup_state SET setup_complete=? WHERE id=? AND setup_complete=?`), true, 1, false)
	if err != nil {
		return Admin{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return Admin{}, err
	}
	if changed != 1 {
		return Admin{}, errors.New("initial setup is already complete")
	}
	user := Admin{ID: uuid.NewString(), Username: username}
	if _, err = tx.ExecContext(ctx, s.query(`INSERT INTO users(id,username,password_hash,created_at) VALUES(?,?,?,?)`), user.ID, user.Username, string(hash), time.Now().UTC()); err != nil {
		return Admin{}, err
	}
	if tokenHash != "" {
		if _, err = tx.ExecContext(ctx, s.query(`INSERT INTO sessions(token_hash,user_id,created_at,expires_at) VALUES(?,?,?,?)`), tokenHash, user.ID, time.Now().UTC(), expiresAt.UTC()); err != nil {
			return Admin{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Admin{}, err
	}
	return user, nil
}

func (s *Store) AuthenticateAdmin(ctx context.Context, username, password string) (Admin, bool, error) {
	var user Admin
	var hash string
	err := s.db.QueryRowContext(ctx, s.query(`SELECT id,username,password_hash FROM users WHERE username=?`), username).Scan(&user.ID, &user.Username, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return Admin{}, false, nil
	}
	if err != nil {
		return Admin{}, false, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return Admin{}, false, nil
	}
	return user, true, nil
}

func (s *Store) CreateSession(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, s.query(`INSERT INTO sessions(token_hash,user_id,created_at,expires_at) VALUES(?,?,?,?)`), tokenHash, userID, time.Now().UTC(), expiresAt.UTC())
	return err
}

// EnsureConfigAdmin keeps the existing internal session identity while using
// the password held by config.json as the source of truth.
func (s *Store) EnsureConfigAdmin(ctx context.Context, password string) (Admin, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return Admin{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Admin{}, err
	}
	defer tx.Rollback()
	var user Admin
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT id,username,password_hash FROM users ORDER BY created_at,id LIMIT 1`).Scan(&user.ID, &user.Username, &previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Admin{}, err
	}
	if err == nil && bcrypt.CompareHashAndPassword([]byte(previous), []byte(password)) == nil {
		return user, tx.Commit()
	}
	if user.ID == "" {
		user = Admin{ID: uuid.NewString(), Username: "admin"}
		_, err = tx.ExecContext(ctx, s.query(`INSERT INTO users(id,username,password_hash,created_at) VALUES(?,?,?,?)`), user.ID, user.Username, string(hash), time.Now().UTC())
	} else {
		_, err = tx.ExecContext(ctx, s.query(`UPDATE users SET password_hash=? WHERE id=?`), string(hash), user.ID)
	}
	if err != nil {
		return Admin{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		return Admin{}, err
	}
	if _, err := tx.ExecContext(ctx, s.query(`UPDATE setup_state SET setup_complete=? WHERE id=1`), true); err != nil {
		return Admin{}, err
	}
	return user, tx.Commit()
}

func (s *Store) SessionAdmin(ctx context.Context, tokenHash string) (Admin, bool, error) {
	var user Admin
	err := s.db.QueryRowContext(ctx, s.query(`SELECT users.id,users.username FROM sessions JOIN users ON users.id=sessions.user_id WHERE sessions.token_hash=? AND sessions.revoked_at IS NULL AND sessions.expires_at>?`), tokenHash, time.Now().UTC()).Scan(&user.ID, &user.Username)
	if errors.Is(err, sql.ErrNoRows) {
		return Admin{}, false, nil
	}
	if err != nil {
		return Admin{}, false, err
	}
	return user, true, nil
}

func (s *Store) RevokeSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, s.query(`UPDATE sessions SET revoked_at=? WHERE token_hash=? AND revoked_at IS NULL`), time.Now().UTC(), tokenHash)
	return err
}
