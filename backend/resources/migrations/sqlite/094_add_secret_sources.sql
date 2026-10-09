-- +goose Up
CREATE TABLE IF NOT EXISTS secret_sources (
    id TEXT PRIMARY KEY,
    created_at DATETIME NOT NULL,
    updated_at DATETIME,
    name TEXT NOT NULL,
    provider TEXT NOT NULL,
    settings TEXT NOT NULL DEFAULT '{}',
    credential TEXT NOT NULL DEFAULT '',
    setup_credential TEXT NOT NULL DEFAULT '',
    last_tested_at DATETIME,
    last_test_error TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_secret_sources_name ON secret_sources(name);

CREATE TABLE IF NOT EXISTS project_secret_bindings (
    id TEXT PRIMARY KEY,
    created_at DATETIME NOT NULL,
    updated_at DATETIME,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_id TEXT NOT NULL REFERENCES secret_sources(id) ON DELETE RESTRICT,
    target TEXT NOT NULL DEFAULT '{}',
    required BOOLEAN NOT NULL DEFAULT TRUE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    auto_redeploy BOOLEAN NOT NULL DEFAULT FALSE,
    hash_salt TEXT NOT NULL,
    deployed_hash TEXT,
    deployed_at DATETIME,
    last_seen_hash TEXT,
    last_notified_hash TEXT,
    last_checked_at DATETIME,
    last_fetched_at DATETIME,
    last_fetch_error TEXT
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_project_secret_bindings_project ON project_secret_bindings(project_id);
CREATE INDEX IF NOT EXISTS idx_project_secret_bindings_source ON project_secret_bindings(source_id);

-- +goose Down
DROP TABLE IF EXISTS project_secret_bindings;
DROP TABLE IF EXISTS secret_sources;
