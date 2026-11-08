// SPDX-License-Identifier: AGPL-3.0-only
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/EasybotCE/easybotce/internal/pipeline"
)

type RuleGroup struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Rules pipeline.Rules `json:"rules"`
}
type RoutingBinding struct {
	ID               string `json:"id"`
	SourceEndpointID string `json:"source_endpoint_id"`
	TargetEndpointID string `json:"target_endpoint_id"`
	SourceChannel    string `json:"source_channel"`
	TargetChannel    string `json:"target_channel"`
	RuleGroupID      string `json:"rule_group_id"`
	Enabled          bool   `json:"enabled"`
}
type Route struct {
	Binding RoutingBinding
	Rules   pipeline.Rules
}

func (r RuleGroup) Validate() error {
	if !ValidID(r.ID) || !utf8.ValidString(r.Name) || strings.TrimSpace(r.Name) == "" || len([]rune(r.Name)) > 128 || r.Rules.Validate() != nil {
		return ErrInvalid
	}
	return nil
}
func (b RoutingBinding) Validate() error {
	if !ValidID(b.ID) || !ValidID(b.SourceEndpointID) || !ValidID(b.TargetEndpointID) || b.SourceEndpointID == b.TargetEndpointID || !ValidID(b.RuleGroupID) || (!ValidID(b.SourceChannel) && b.SourceChannel != "*") || !ValidID(b.TargetChannel) {
		return ErrInvalid
	}
	return nil
}
func decodeRule(row ruleRow) (RuleGroup, error) {
	r := RuleGroup{ID: row.ID, Name: row.Name}
	if json.Unmarshal([]byte(row.Config), &r.Rules) != nil || r.Validate() != nil {
		return r, errors.New("invalid stored rule group")
	}
	return r, nil
}
func encodeRule(r RuleGroup) ruleRow {
	raw, _ := json.Marshal(r.Rules)
	return ruleRow{ID: r.ID, Name: r.Name, Config: string(raw)}
}
func encodeBinding(b RoutingBinding) bindingRow {
	return bindingRow{ID: b.ID, SourceEndpointID: b.SourceEndpointID, TargetEndpointID: b.TargetEndpointID, SourceChannel: b.SourceChannel, TargetChannel: b.TargetChannel, RuleGroupID: b.RuleGroupID, Enabled: boolInt(b.Enabled)}
}
func decodeBinding(b bindingRow) RoutingBinding {
	return RoutingBinding{ID: b.ID, SourceEndpointID: b.SourceEndpointID, TargetEndpointID: b.TargetEndpointID, SourceChannel: b.SourceChannel, TargetChannel: b.TargetChannel, RuleGroupID: b.RuleGroupID, Enabled: b.Enabled != 0}
}
func safeReadError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return errors.New("cannot read routing resource")
}
func (s *Store) GetRuleGroup(ctx context.Context, id string) (RuleGroup, error) {
	row, err := s.q.getRule(ctx, id)
	if err != nil {
		return RuleGroup{}, safeReadError(err)
	}
	return decodeRule(row)
}
func (s *Store) ListRuleGroups(ctx context.Context, after string, limit int) ([]RuleGroup, error) {
	if limit < 1 || limit > 200 {
		return nil, ErrInvalid
	}
	rows, err := s.q.listRules(ctx, after, int64(limit))
	if err != nil {
		return nil, safeReadError(err)
	}
	out := []RuleGroup{}
	for _, row := range rows {
		r, err := decodeRule(row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *Store) CreateRuleGroup(ctx context.Context, r RuleGroup) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if _, err := s.GetRuleGroup(ctx, r.ID); err == nil {
		return ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := s.q.createRule(ctx, encodeRule(r)); err != nil {
		return errors.New("cannot create rule group")
	}
	return nil
}
func (s *Store) UpdateRuleGroup(ctx context.Context, r RuleGroup) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if _, err := s.GetRuleGroup(ctx, r.ID); err != nil {
		return err
	}
	if _, err := s.q.updateRule(ctx, encodeRule(r)); err != nil {
		return errors.New("cannot update rule group")
	}
	return nil
}
func (s *Store) DeleteRuleGroup(ctx context.Context, id string) error {
	n, err := s.q.ruleUsage(ctx, id)
	if err != nil {
		return safeReadError(err)
	}
	if n > 0 {
		return ErrConflict
	}
	n, err = s.q.deleteRule(ctx, id)
	if err != nil {
		return errors.New("cannot delete rule group")
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Store) GetRoutingBinding(ctx context.Context, id string) (RoutingBinding, error) {
	row, err := s.q.getBinding(ctx, id)
	if err != nil {
		return RoutingBinding{}, safeReadError(err)
	}
	return decodeBinding(row), nil
}
func (s *Store) ListRoutingBindings(ctx context.Context, after string, limit int) ([]RoutingBinding, error) {
	if limit < 1 || limit > 200 {
		return nil, ErrInvalid
	}
	rows, err := s.q.listBindings(ctx, after, int64(limit))
	if err != nil {
		return nil, safeReadError(err)
	}
	out := []RoutingBinding{}
	for _, row := range rows {
		out = append(out, decodeBinding(row))
	}
	return out, nil
}
func (s *Store) validateReferences(ctx context.Context, b RoutingBinding) error {
	if err := b.Validate(); err != nil {
		return err
	}
	for _, id := range []string{b.SourceEndpointID, b.TargetEndpointID} {
		if _, err := s.GetEndpoint(ctx, id); err != nil {
			return err
		}
	}
	_, err := s.GetRuleGroup(ctx, b.RuleGroupID)
	return err
}
func (s *Store) CreateRoutingBinding(ctx context.Context, b RoutingBinding) error {
	if err := s.validateReferences(ctx, b); err != nil {
		return err
	}
	if _, err := s.GetRoutingBinding(ctx, b.ID); err == nil {
		return ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := s.q.createBinding(ctx, encodeBinding(b)); err != nil {
		return errors.New("cannot create routing binding")
	}
	return nil
}
func (s *Store) UpdateRoutingBinding(ctx context.Context, b RoutingBinding) error {
	if err := s.validateReferences(ctx, b); err != nil {
		return err
	}
	if _, err := s.GetRoutingBinding(ctx, b.ID); err != nil {
		return err
	}
	if _, err := s.q.updateBinding(ctx, encodeBinding(b)); err != nil {
		return errors.New("cannot update routing binding")
	}
	return nil
}
func (s *Store) DeleteRoutingBinding(ctx context.Context, id string) error {
	n, err := s.q.deleteBinding(ctx, id)
	if err != nil {
		return errors.New("cannot delete routing binding")
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ResolveRoutes bounds fan-out at 200. Exceeding it fails closed, never silently
// drops the last routes. Disabled sources/targets/bindings are excluded in SQL.
func (s *Store) ResolveRoutes(ctx context.Context, source, channel string) ([]Route, error) {
	rows, err := s.q.routes(ctx, source, channel)
	if err != nil {
		return nil, safeReadError(err)
	}
	if len(rows) > 200 {
		return nil, errors.New("routing fan-out limit exceeded")
	}
	out := []Route{}
	for _, row := range rows {
		r, err := decodeRule(ruleRow{ID: row.RuleGroupID, Name: "route", Config: row.Config})
		if err != nil {
			return nil, err
		}
		out = append(out, Route{Binding: RoutingBinding{ID: row.ID, SourceEndpointID: row.SourceEndpointID, TargetEndpointID: row.TargetEndpointID, SourceChannel: row.SourceChannel, TargetChannel: row.TargetChannel, RuleGroupID: row.RuleGroupID, Enabled: row.Enabled != 0}, Rules: r.Rules})
	}
	return out, nil
}
