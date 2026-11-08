-- SPDX-License-Identifier: AGPL-3.0-only
-- name: GetEndpoint :one
SELECT id,kind,name,enabled FROM ce_endpoints WHERE id=sqlc.arg(id);
-- name: CreateEndpoint :exec
INSERT INTO ce_endpoints(id,kind,name,enabled) VALUES(sqlc.arg(id),sqlc.arg(kind),sqlc.arg(name),sqlc.arg(enabled));
-- name: ListEndpoints :many
SELECT id,kind,name,enabled FROM ce_endpoints WHERE id>sqlc.arg(after_id) ORDER BY id LIMIT sqlc.arg(page_size);
-- name: UpdateEndpoint :exec
UPDATE ce_endpoints SET name=sqlc.arg(name),enabled=sqlc.arg(enabled) WHERE id=sqlc.arg(id);
-- name: DeleteEndpoint :execrows
DELETE FROM ce_endpoints WHERE id=sqlc.arg(id);
-- name: IssueCredential :exec
INSERT INTO ce_credentials(id,endpoint_id,token_hash,capabilities,created_at) VALUES(sqlc.arg(id),sqlc.arg(endpoint_id),sqlc.arg(token_hash),sqlc.arg(capabilities),sqlc.arg(created_at));
-- name: ListCredentials :many
SELECT id,endpoint_id,token_hash,capabilities,created_at FROM ce_credentials WHERE endpoint_id=sqlc.arg(endpoint_id) ORDER BY id;
-- name: RevokeCredential :execrows
DELETE FROM ce_credentials WHERE id=sqlc.arg(id) AND endpoint_id=sqlc.arg(endpoint_id);
-- name: Authenticate :one
SELECT e.id,e.kind,e.name,e.enabled,c.id AS credential_id,c.capabilities,c.created_at FROM ce_credentials c JOIN ce_endpoints e ON c.endpoint_id=e.id WHERE c.token_hash=sqlc.arg(token_hash);


-- name: GetRuleGroup :one
SELECT * FROM ce_rule_groups WHERE id=sqlc.arg(id);
-- name: ListRuleGroups :many
SELECT * FROM ce_rule_groups WHERE id>sqlc.arg(after_id) ORDER BY id LIMIT sqlc.arg(page_size);
-- name: CreateRuleGroup :exec
INSERT INTO ce_rule_groups(id,name,config) VALUES(sqlc.arg(id),sqlc.arg(name),sqlc.arg(config));
-- name: UpdateRuleGroup :execrows
UPDATE ce_rule_groups SET name=sqlc.arg(name),config=sqlc.arg(config) WHERE id=sqlc.arg(id);
-- name: DeleteRuleGroup :execrows
DELETE FROM ce_rule_groups WHERE id=sqlc.arg(id);
-- name: RuleGroupUsage :one
SELECT COUNT(*) FROM ce_routing_bindings WHERE rule_group_id=sqlc.arg(id);
-- name: GetRoutingBinding :one
SELECT * FROM ce_routing_bindings WHERE id=sqlc.arg(id);
-- name: ListRoutingBindings :many
SELECT * FROM ce_routing_bindings WHERE id>sqlc.arg(after_id) ORDER BY id LIMIT sqlc.arg(page_size);
-- name: CreateRoutingBinding :exec
INSERT INTO ce_routing_bindings(id,source_endpoint_id,target_endpoint_id,source_channel,target_channel,rule_group_id,enabled) VALUES(sqlc.arg(id),sqlc.arg(source_endpoint_id),sqlc.arg(target_endpoint_id),sqlc.arg(source_channel),sqlc.arg(target_channel),sqlc.arg(rule_group_id),sqlc.arg(enabled));
-- name: UpdateRoutingBinding :execrows
UPDATE ce_routing_bindings SET source_endpoint_id=sqlc.arg(source_endpoint_id),target_endpoint_id=sqlc.arg(target_endpoint_id),source_channel=sqlc.arg(source_channel),target_channel=sqlc.arg(target_channel),rule_group_id=sqlc.arg(rule_group_id),enabled=sqlc.arg(enabled) WHERE id=sqlc.arg(id);
-- name: DeleteRoutingBinding :execrows
DELETE FROM ce_routing_bindings WHERE id=sqlc.arg(id);
-- name: ResolveRoutes :many
SELECT b.id,b.source_endpoint_id,b.target_endpoint_id,b.source_channel,b.target_channel,b.rule_group_id,b.enabled,r.config FROM ce_routing_bindings b JOIN ce_rule_groups r ON r.id=b.rule_group_id JOIN ce_endpoints source ON source.id=b.source_endpoint_id JOIN ce_endpoints target ON target.id=b.target_endpoint_id WHERE b.source_endpoint_id=sqlc.arg(source_id) AND (b.source_channel=sqlc.arg(channel_id) OR b.source_channel='*') AND b.enabled=1 AND source.enabled=1 AND target.enabled=1 ORDER BY b.id LIMIT 201;
