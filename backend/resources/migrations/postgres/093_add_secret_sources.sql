-- +goose Up
CREATE TABLE secret_sources (
    id TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ,
    name TEXT NOT NULL,
    provider TEXT NOT NULL,
    site_url TEXT NOT NULL,
    client_id TEXT NOT NULL,
    client_secret TEXT NOT NULL,
    organization_slug TEXT NOT NULL DEFAULT '',
    last_tested_at TIMESTAMPTZ,
    last_test_error TEXT
);

CREATE UNIQUE INDEX idx_secret_sources_name ON secret_sources(name);

CREATE TABLE project_secret_bindings (
    id TEXT PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    source_id TEXT NOT NULL REFERENCES secret_sources(id) ON DELETE RESTRICT,
    remote_project_id TEXT NOT NULL,
    environment TEXT NOT NULL,
    secret_path TEXT NOT NULL DEFAULT '/',
    include_imports BOOLEAN NOT NULL DEFAULT TRUE,
    expand_references BOOLEAN NOT NULL DEFAULT TRUE,
    required BOOLEAN NOT NULL DEFAULT TRUE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    hash_salt TEXT NOT NULL,
    deployed_hash TEXT,
    deployed_at TIMESTAMPTZ,
    last_fetched_at TIMESTAMPTZ,
    last_fetch_error TEXT
);

CREATE UNIQUE INDEX idx_project_secret_bindings_project ON project_secret_bindings(project_id);
CREATE INDEX idx_project_secret_bindings_source ON project_secret_bindings(source_id);

-- +goose Down
DROP TABLE IF EXISTS project_secret_bindings;
DROP TABLE IF EXISTS secret_sources;
