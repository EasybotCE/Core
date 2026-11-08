-- SPDX-License-Identifier: AGPL-3.0-only
CREATE TABLE ce_schema (id INTEGER PRIMARY KEY, version INTEGER NOT NULL);
CREATE TABLE ce_endpoints (id VARCHAR(128) PRIMARY KEY, kind VARCHAR(32) NOT NULL, name TEXT NOT NULL, enabled INTEGER NOT NULL);
CREATE TABLE ce_credentials (id VARCHAR(128) PRIMARY KEY, endpoint_id VARCHAR(128) NOT NULL, token_hash VARCHAR(64) NOT NULL UNIQUE, capabilities TEXT NOT NULL, created_at BIGINT NOT NULL, FOREIGN KEY(endpoint_id) REFERENCES ce_endpoints(id) ON DELETE CASCADE);

CREATE TABLE ce_rule_groups (id VARCHAR(128) PRIMARY KEY, name TEXT NOT NULL, config TEXT NOT NULL);
CREATE TABLE ce_routing_bindings (id VARCHAR(128) PRIMARY KEY, source_endpoint_id VARCHAR(128) NOT NULL, target_endpoint_id VARCHAR(128) NOT NULL, source_channel VARCHAR(128) NOT NULL, target_channel VARCHAR(128) NOT NULL, rule_group_id VARCHAR(128) NOT NULL, enabled INTEGER NOT NULL, FOREIGN KEY(source_endpoint_id) REFERENCES ce_endpoints(id) ON DELETE CASCADE, FOREIGN KEY(target_endpoint_id) REFERENCES ce_endpoints(id) ON DELETE CASCADE, FOREIGN KEY(rule_group_id) REFERENCES ce_rule_groups(id));
CREATE TABLE ce_player_policies (id VARCHAR(128) PRIMARY KEY, endpoint_id VARCHAR(128) NOT NULL, player_id VARCHAR(128) NOT NULL, mode VARCHAR(16) NOT NULL, action VARCHAR(16) NOT NULL, reason TEXT NOT NULL, expires_at BIGINT NOT NULL, created_at BIGINT NOT NULL);

CREATE TABLE ce_audit_events (id VARCHAR(128) PRIMARY KEY, actor VARCHAR(128) NOT NULL, action VARCHAR(128) NOT NULL, resource VARCHAR(256) NOT NULL, created_at BIGINT NOT NULL);




CREATE TABLE ce_commands (id VARCHAR(128) PRIMARY KEY, endpoint_id VARCHAR(128) NOT NULL, name VARCHAR(64) NOT NULL, response TEXT NOT NULL, permission VARCHAR(16) NOT NULL, enabled INTEGER NOT NULL, created_at BIGINT NOT NULL, UNIQUE(endpoint_id,name));


CREATE TABLE ce_account_challenges (code_hash VARCHAR(64) PRIMARY KEY, endpoint_id VARCHAR(128) NOT NULL, player_id VARCHAR(128) NOT NULL, expires_at BIGINT NOT NULL, FOREIGN KEY(endpoint_id) REFERENCES ce_endpoints(id) ON DELETE CASCADE);
CREATE TABLE ce_account_bindings (id VARCHAR(128) PRIMARY KEY, mc_endpoint_id VARCHAR(128) NOT NULL, player_id VARCHAR(128) NOT NULL, chat_endpoint_id VARCHAR(128) NOT NULL, platform_user_id VARCHAR(128) NOT NULL, created_at BIGINT NOT NULL, UNIQUE(mc_endpoint_id,player_id), UNIQUE(chat_endpoint_id,platform_user_id), FOREIGN KEY(mc_endpoint_id) REFERENCES ce_endpoints(id) ON DELETE CASCADE, FOREIGN KEY(chat_endpoint_id) REFERENCES ce_endpoints(id) ON DELETE CASCADE);
CREATE TABLE ce_player_states (endpoint_id VARCHAR(128) NOT NULL, player_id VARCHAR(128) NOT NULL, player_name VARCHAR(128) NOT NULL, online INTEGER NOT NULL, world VARCHAR(128) NOT NULL, x DOUBLE PRECISION NOT NULL, y DOUBLE PRECISION NOT NULL, z DOUBLE PRECISION NOT NULL, updated_at BIGINT NOT NULL, PRIMARY KEY(endpoint_id,player_id), FOREIGN KEY(endpoint_id) REFERENCES ce_endpoints(id) ON DELETE CASCADE);
-- SPDX-License-Identifier: AGPL-3.0-only
CREATE TABLE ce_onebot_actions (id VARCHAR(128) PRIMARY KEY, endpoint_id VARCHAR(128) NOT NULL, status VARCHAR(16) NOT NULL, created_at BIGINT NOT NULL, updated_at BIGINT NOT NULL, retcode INTEGER NULL);
CREATE INDEX ce_onebot_actions_endpoint_created ON ce_onebot_actions(endpoint_id, created_at);
