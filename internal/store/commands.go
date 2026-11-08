// SPDX-License-Identifier: AGPL-3.0-only
package store

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type Command struct {
	ID         string `json:"id"`
	EndpointID string `json:"endpoint_id"`
	Name       string `json:"name"`
	Response   string `json:"response"`
	Permission string `json:"permission"`
	Enabled    bool   `json:"enabled"`
	CreatedAt  int64  `json:"created_at"`
}

var commandName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func (c Command) Validate() error {
	if !ValidID(c.ID) || !(c.EndpointID == "*" || ValidID(c.EndpointID)) || !commandName.MatchString(c.Name) || !utf8.ValidString(c.Response) || len([]rune(c.Response)) > 2048 || strings.TrimSpace(c.Response) == "" || (c.Permission != "console" && c.Permission != "player") {
		return ErrInvalid
	}
	return nil
}
func (s *Store) PutCommand(ctx context.Context, c Command) error {
	if err := c.Validate(); err != nil {
		return err
	}
	var exists int
	err := s.db.QueryRowContext(ctx, s.query("SELECT COUNT(*) FROM ce_commands WHERE endpoint_id=? AND name=?"), c.EndpointID, c.Name).Scan(&exists)
	if err != nil {
		return err
	}
	if exists > 0 {
		return ErrConflict
	}
	if c.CreatedAt == 0 {
		c.CreatedAt = time.Now().Unix()
	}
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	_, err = s.db.ExecContext(ctx, s.query("INSERT INTO ce_commands(id,endpoint_id,name,response,permission,enabled,created_at) VALUES(?,?,?,?,?,?,?)"), c.ID, c.EndpointID, c.Name, c.Response, c.Permission, enabled, c.CreatedAt)
	if err != nil {
		return ErrConflict
	}
	return nil
}
func (s *Store) ListCommands(ctx context.Context, endpointID string) ([]Command, error) {
	rows, err := s.db.QueryContext(ctx, s.query("SELECT id,endpoint_id,name,response,permission,enabled,created_at FROM ce_commands WHERE endpoint_id=? OR endpoint_id='*' ORDER BY CASE WHEN endpoint_id=? THEN 0 ELSE 1 END,name,id LIMIT 512"), endpointID, endpointID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Command{}
	for rows.Next() {
		var c Command
		var enabled int
		if err = rows.Scan(&c.ID, &c.EndpointID, &c.Name, &c.Response, &c.Permission, &enabled, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Enabled = enabled != 0
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) DeleteCommand(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, s.query("DELETE FROM ce_commands WHERE id=?"), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
