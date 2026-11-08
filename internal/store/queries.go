// SPDX-License-Identifier: AGPL-3.0-only
// The adapter normalizes sqlc's per-engine concrete structs. All business SQL
// lives in sql/; generated code is checked in and reproducible with sqlc.yaml.
package store

import (
	"context"
	"database/sql"
	"github.com/EasybotCE/easybotce/internal/store/sqlgen/mysql"
	"github.com/EasybotCE/easybotce/internal/store/sqlgen/postgres"
	"github.com/EasybotCE/easybotce/internal/store/sqlgen/sqlite"
)

type endpointRow sqlite.CeEndpoint
type credentialRow sqlite.CeCredential
type authRow sqlite.AuthenticateRow
type ruleRow sqlite.CeRuleGroup
type bindingRow sqlite.CeRoutingBinding
type routeRow sqlite.ResolveRoutesRow
type queries struct {
	getRule       func(context.Context, string) (ruleRow, error)
	listRules     func(context.Context, string, int64) ([]ruleRow, error)
	createRule    func(context.Context, ruleRow) error
	updateRule    func(context.Context, ruleRow) (int64, error)
	deleteRule    func(context.Context, string) (int64, error)
	ruleUsage     func(context.Context, string) (int64, error)
	getBinding    func(context.Context, string) (bindingRow, error)
	listBindings  func(context.Context, string, int64) ([]bindingRow, error)
	createBinding func(context.Context, bindingRow) error
	updateBinding func(context.Context, bindingRow) (int64, error)
	deleteBinding func(context.Context, string) (int64, error)
	routes        func(context.Context, string, string) ([]routeRow, error)

	get          func(context.Context, string) (endpointRow, error)
	create       func(context.Context, Endpoint) error
	list         func(context.Context, string, int64) ([]endpointRow, error)
	update       func(context.Context, Endpoint) error
	remove       func(context.Context, string) (int64, error)
	issue        func(context.Context, credentialRow) error
	credentials  func(context.Context, string) ([]credentialRow, error)
	revoke       func(context.Context, string, string) (int64, error)
	authenticate func(context.Context, string) (authRow, error)
}

func newQueries(db *sql.DB, driver string) *queries {
	switch driver {
	case "sqlite":
		q := sqlite.New(db)
		return &queries{
			getRule: func(ctx context.Context, id string) (ruleRow, error) {
				r, e := q.GetRuleGroup(ctx, id)
				return ruleRow(r), e
			},
			listRules: func(ctx context.Context, after string, limit int64) ([]ruleRow, error) {
				rows, e := q.ListRuleGroups(ctx, sqlite.ListRuleGroupsParams{AfterID: after, PageSize: limit})
				out := []ruleRow{}
				for _, r := range rows {
					out = append(out, ruleRow(r))
				}
				return out, e
			},
			createRule: func(ctx context.Context, r ruleRow) error {
				return q.CreateRuleGroup(ctx, sqlite.CreateRuleGroupParams(r))
			},
			updateRule: func(ctx context.Context, r ruleRow) (int64, error) {
				return q.UpdateRuleGroup(ctx, sqlite.UpdateRuleGroupParams{ID: r.ID, Name: r.Name, Config: r.Config})
			},
			deleteRule: q.DeleteRuleGroup, ruleUsage: q.RuleGroupUsage,
			getBinding: func(ctx context.Context, id string) (bindingRow, error) {
				r, e := q.GetRoutingBinding(ctx, id)
				return bindingRow(r), e
			},
			listBindings: func(ctx context.Context, after string, limit int64) ([]bindingRow, error) {
				rows, e := q.ListRoutingBindings(ctx, sqlite.ListRoutingBindingsParams{AfterID: after, PageSize: limit})
				out := []bindingRow{}
				for _, r := range rows {
					out = append(out, bindingRow(r))
				}
				return out, e
			},
			createBinding: func(ctx context.Context, r bindingRow) error {
				return q.CreateRoutingBinding(ctx, sqlite.CreateRoutingBindingParams(r))
			},
			updateBinding: func(ctx context.Context, r bindingRow) (int64, error) {
				return q.UpdateRoutingBinding(ctx, sqlite.UpdateRoutingBindingParams{ID: r.ID, SourceEndpointID: r.SourceEndpointID, TargetEndpointID: r.TargetEndpointID, SourceChannel: r.SourceChannel, TargetChannel: r.TargetChannel, RuleGroupID: r.RuleGroupID, Enabled: r.Enabled})
			},
			deleteBinding: q.DeleteRoutingBinding,
			routes: func(ctx context.Context, source, channel string) ([]routeRow, error) {
				rows, e := q.ResolveRoutes(ctx, sqlite.ResolveRoutesParams{SourceID: source, ChannelID: channel})
				out := []routeRow{}
				for _, r := range rows {
					out = append(out, routeRow(r))
				}
				return out, e
			},

			get: func(ctx context.Context, id string) (endpointRow, error) {
				row, err := q.GetEndpoint(ctx, id)
				return endpointRow(row), err
			},
			create: func(ctx context.Context, e Endpoint) error {
				return q.CreateEndpoint(ctx, sqlite.CreateEndpointParams{ID: e.ID, Kind: string(e.Type), Name: e.Name, Enabled: boolInt(e.Enabled)})
			},
			list: func(ctx context.Context, after string, limit int64) ([]endpointRow, error) {
				rows, err := q.ListEndpoints(ctx, sqlite.ListEndpointsParams{AfterID: after, PageSize: limit})
				result := []endpointRow{}
				for _, r := range rows {
					result = append(result, endpointRow(r))
				}
				return result, err
			},
			update: func(ctx context.Context, e Endpoint) error {
				return q.UpdateEndpoint(ctx, sqlite.UpdateEndpointParams{ID: e.ID, Name: e.Name, Enabled: boolInt(e.Enabled)})
			},
			remove: q.DeleteEndpoint,
			issue: func(ctx context.Context, c credentialRow) error {
				return q.IssueCredential(ctx, sqlite.IssueCredentialParams(c))
			},
			credentials: func(ctx context.Context, id string) ([]credentialRow, error) {
				rows, err := q.ListCredentials(ctx, id)
				result := []credentialRow{}
				for _, r := range rows {
					result = append(result, credentialRow(r))
				}
				return result, err
			},
			revoke: func(ctx context.Context, id, endpointID string) (int64, error) {
				return q.RevokeCredential(ctx, sqlite.RevokeCredentialParams{ID: id, EndpointID: endpointID})
			},
			authenticate: func(ctx context.Context, hash string) (authRow, error) {
				row, err := q.Authenticate(ctx, hash)
				return authRow(row), err
			},
		}
	case "mysql":
		q := mysql.New(db)
		return &queries{
			getRule: func(ctx context.Context, id string) (ruleRow, error) {
				r, e := q.GetRuleGroup(ctx, id)
				return ruleRow(r), e
			},
			listRules: func(ctx context.Context, after string, limit int64) ([]ruleRow, error) {
				rows, e := q.ListRuleGroups(ctx, mysql.ListRuleGroupsParams{AfterID: after, Limit: limit})
				out := []ruleRow{}
				for _, r := range rows {
					out = append(out, ruleRow(r))
				}
				return out, e
			},
			createRule: func(ctx context.Context, r ruleRow) error {
				return q.CreateRuleGroup(ctx, mysql.CreateRuleGroupParams(r))
			},
			updateRule: func(ctx context.Context, r ruleRow) (int64, error) {
				return q.UpdateRuleGroup(ctx, mysql.UpdateRuleGroupParams{ID: r.ID, Name: r.Name, Config: r.Config})
			},
			deleteRule: q.DeleteRuleGroup, ruleUsage: q.RuleGroupUsage,
			getBinding: func(ctx context.Context, id string) (bindingRow, error) {
				r, e := q.GetRoutingBinding(ctx, id)
				return bindingRow(r), e
			},
			listBindings: func(ctx context.Context, after string, limit int64) ([]bindingRow, error) {
				rows, e := q.ListRoutingBindings(ctx, mysql.ListRoutingBindingsParams{AfterID: after, Limit: limit})
				out := []bindingRow{}
				for _, r := range rows {
					out = append(out, bindingRow(r))
				}
				return out, e
			},
			createBinding: func(ctx context.Context, r bindingRow) error {
				return q.CreateRoutingBinding(ctx, mysql.CreateRoutingBindingParams(r))
			},
			updateBinding: func(ctx context.Context, r bindingRow) (int64, error) {
				return q.UpdateRoutingBinding(ctx, mysql.UpdateRoutingBindingParams{ID: r.ID, SourceEndpointID: r.SourceEndpointID, TargetEndpointID: r.TargetEndpointID, SourceChannel: r.SourceChannel, TargetChannel: r.TargetChannel, RuleGroupID: r.RuleGroupID, Enabled: r.Enabled})
			},
			deleteBinding: q.DeleteRoutingBinding,
			routes: func(ctx context.Context, source, channel string) ([]routeRow, error) {
				rows, e := q.ResolveRoutes(ctx, mysql.ResolveRoutesParams{SourceID: source, ChannelID: channel})
				out := []routeRow{}
				for _, r := range rows {
					out = append(out, routeRow(r))
				}
				return out, e
			},

			get: func(ctx context.Context, id string) (endpointRow, error) {
				row, err := q.GetEndpoint(ctx, id)
				return endpointRow(row), err
			},
			create: func(ctx context.Context, e Endpoint) error {
				return q.CreateEndpoint(ctx, mysql.CreateEndpointParams{ID: e.ID, Kind: string(e.Type), Name: e.Name, Enabled: boolInt(e.Enabled)})
			},
			list: func(ctx context.Context, after string, limit int64) ([]endpointRow, error) {
				rows, err := q.ListEndpoints(ctx, mysql.ListEndpointsParams{AfterID: after, Limit: limit})
				result := []endpointRow{}
				for _, r := range rows {
					result = append(result, endpointRow(r))
				}
				return result, err
			},
			update: func(ctx context.Context, e Endpoint) error {
				return q.UpdateEndpoint(ctx, mysql.UpdateEndpointParams{ID: e.ID, Name: e.Name, Enabled: boolInt(e.Enabled)})
			},
			remove: q.DeleteEndpoint,
			issue: func(ctx context.Context, c credentialRow) error {
				return q.IssueCredential(ctx, mysql.IssueCredentialParams(c))
			},
			credentials: func(ctx context.Context, id string) ([]credentialRow, error) {
				rows, err := q.ListCredentials(ctx, id)
				result := []credentialRow{}
				for _, r := range rows {
					result = append(result, credentialRow(r))
				}
				return result, err
			},
			revoke: func(ctx context.Context, id, endpointID string) (int64, error) {
				return q.RevokeCredential(ctx, mysql.RevokeCredentialParams{ID: id, EndpointID: endpointID})
			},
			authenticate: func(ctx context.Context, hash string) (authRow, error) {
				row, err := q.Authenticate(ctx, hash)
				return authRow(row), err
			},
		}
	case "postgres":
		q := postgres.New(db)
		return &queries{
			getRule: func(ctx context.Context, id string) (ruleRow, error) {
				r, e := q.GetRuleGroup(ctx, id)
				return ruleRow(r), e
			},
			listRules: func(ctx context.Context, after string, limit int64) ([]ruleRow, error) {
				rows, e := q.ListRuleGroups(ctx, postgres.ListRuleGroupsParams{AfterID: after, PageSize: limit})
				out := []ruleRow{}
				for _, r := range rows {
					out = append(out, ruleRow(r))
				}
				return out, e
			},
			createRule: func(ctx context.Context, r ruleRow) error {
				return q.CreateRuleGroup(ctx, postgres.CreateRuleGroupParams(r))
			},
			updateRule: func(ctx context.Context, r ruleRow) (int64, error) {
				return q.UpdateRuleGroup(ctx, postgres.UpdateRuleGroupParams{ID: r.ID, Name: r.Name, Config: r.Config})
			},
			deleteRule: q.DeleteRuleGroup, ruleUsage: q.RuleGroupUsage,
			getBinding: func(ctx context.Context, id string) (bindingRow, error) {
				r, e := q.GetRoutingBinding(ctx, id)
				return bindingRow(r), e
			},
			listBindings: func(ctx context.Context, after string, limit int64) ([]bindingRow, error) {
				rows, e := q.ListRoutingBindings(ctx, postgres.ListRoutingBindingsParams{AfterID: after, PageSize: limit})
				out := []bindingRow{}
				for _, r := range rows {
					out = append(out, bindingRow(r))
				}
				return out, e
			},
			createBinding: func(ctx context.Context, r bindingRow) error {
				return q.CreateRoutingBinding(ctx, postgres.CreateRoutingBindingParams(r))
			},
			updateBinding: func(ctx context.Context, r bindingRow) (int64, error) {
				return q.UpdateRoutingBinding(ctx, postgres.UpdateRoutingBindingParams{ID: r.ID, SourceEndpointID: r.SourceEndpointID, TargetEndpointID: r.TargetEndpointID, SourceChannel: r.SourceChannel, TargetChannel: r.TargetChannel, RuleGroupID: r.RuleGroupID, Enabled: r.Enabled})
			},
			deleteBinding: q.DeleteRoutingBinding,
			routes: func(ctx context.Context, source, channel string) ([]routeRow, error) {
				rows, e := q.ResolveRoutes(ctx, postgres.ResolveRoutesParams{SourceID: source, ChannelID: channel})
				out := []routeRow{}
				for _, r := range rows {
					out = append(out, routeRow(r))
				}
				return out, e
			},

			get: func(ctx context.Context, id string) (endpointRow, error) {
				row, err := q.GetEndpoint(ctx, id)
				return endpointRow(row), err
			},
			create: func(ctx context.Context, e Endpoint) error {
				return q.CreateEndpoint(ctx, postgres.CreateEndpointParams{ID: e.ID, Kind: string(e.Type), Name: e.Name, Enabled: boolInt(e.Enabled)})
			},
			list: func(ctx context.Context, after string, limit int64) ([]endpointRow, error) {
				rows, err := q.ListEndpoints(ctx, postgres.ListEndpointsParams{AfterID: after, PageSize: limit})
				result := []endpointRow{}
				for _, r := range rows {
					result = append(result, endpointRow(r))
				}
				return result, err
			},
			update: func(ctx context.Context, e Endpoint) error {
				return q.UpdateEndpoint(ctx, postgres.UpdateEndpointParams{ID: e.ID, Name: e.Name, Enabled: boolInt(e.Enabled)})
			},
			remove: q.DeleteEndpoint,
			issue: func(ctx context.Context, c credentialRow) error {
				return q.IssueCredential(ctx, postgres.IssueCredentialParams(c))
			},
			credentials: func(ctx context.Context, id string) ([]credentialRow, error) {
				rows, err := q.ListCredentials(ctx, id)
				result := []credentialRow{}
				for _, r := range rows {
					result = append(result, credentialRow(r))
				}
				return result, err
			},
			revoke: func(ctx context.Context, id, endpointID string) (int64, error) {
				return q.RevokeCredential(ctx, postgres.RevokeCredentialParams{ID: id, EndpointID: endpointID})
			},
			authenticate: func(ctx context.Context, hash string) (authRow, error) {
				row, err := q.Authenticate(ctx, hash)
				return authRow(row), err
			},
		}
	default:
		panic("unsupported sqlc dialect")
	}
}
