-- IC 初始 schema（SQLite / Postgres 兼容子集）
-- 设计依据：docs/design/02-domain-model.md §3

CREATE TABLE IF NOT EXISTS users (
  id          TEXT PRIMARY KEY,
  email       TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL DEFAULT '',
  avatar_ref  TEXT,
  password_hash TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workspaces (
  id          TEXT PRIMARY KEY,
  name        TEXT NOT NULL,
  slug        TEXT NOT NULL,
  owner_id    TEXT NOT NULL,
  plan        TEXT NOT NULL DEFAULT 'free',
  settings    TEXT NOT NULL DEFAULT '{}',
  created_at  TEXT NOT NULL,
  deleted_at  TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_workspaces_slug ON workspaces(slug);

CREATE TABLE IF NOT EXISTS workspace_members (
  workspace_id TEXT NOT NULL,
  user_id      TEXT NOT NULL,
  role         TEXT NOT NULL DEFAULT 'editor',
  created_at   TEXT NOT NULL,
  PRIMARY KEY (workspace_id, user_id)
);

CREATE TABLE IF NOT EXISTS sessions (
  token       TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL,
  workspace_id TEXT,
  scopes      TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL,
  expires_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);

CREATE TABLE IF NOT EXISTS api_keys (
  id          TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  user_id     TEXT NOT NULL,
  name        TEXT NOT NULL DEFAULT '',
  hash        TEXT NOT NULL UNIQUE,
  scopes      TEXT NOT NULL DEFAULT '',
  last_used_at TEXT,
  expires_at  TEXT,
  revoked_at  TEXT,
  created_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS projects (
  id          TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  name        TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  cover_asset_id TEXT,
  visibility  TEXT NOT NULL DEFAULT 'private',
  created_by  TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  deleted_at  TEXT
);
CREATE INDEX IF NOT EXISTS idx_projects_ws ON projects(workspace_id, updated_at);

CREATE TABLE IF NOT EXISTS canvases (
  id          TEXT PRIMARY KEY,
  project_id  TEXT NOT NULL,
  name        TEXT NOT NULL DEFAULT '',
  version     INTEGER NOT NULL DEFAULT 1,
  settings    TEXT NOT NULL DEFAULT '{}',
  doc         TEXT NOT NULL DEFAULT '{}',
  thumb_asset_id TEXT,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  deleted_at  TEXT
);
CREATE INDEX IF NOT EXISTS idx_canvases_project ON canvases(project_id, updated_at);

CREATE TABLE IF NOT EXISTS canvas_ops (
  canvas_id   TEXT NOT NULL,
  seq         INTEGER NOT NULL,
  version     INTEGER NOT NULL,
  actor_id    TEXT NOT NULL,
  op          TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  PRIMARY KEY (canvas_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_canvas_ops_version ON canvas_ops(canvas_id, version);

CREATE TABLE IF NOT EXISTS canvas_docs (
  canvas_id   TEXT NOT NULL,
  version     INTEGER NOT NULL,
  doc         TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  PRIMARY KEY (canvas_id, version)
);

CREATE TABLE IF NOT EXISTS assets (
  id           TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  kind         TEXT NOT NULL,
  hash         TEXT NOT NULL,
  size         INTEGER NOT NULL,
  mime         TEXT NOT NULL,
  name         TEXT NOT NULL DEFAULT '',
  meta         TEXT NOT NULL DEFAULT '{}',
  origin       TEXT NOT NULL DEFAULT 'upload',
  source_run_id TEXT,
  source_step_id TEXT,
  created_at   TEXT NOT NULL,
  deleted_at   TEXT
);
-- 内容寻址去重：同工作区同 hash 只存一份（ATK-16 前提）
CREATE UNIQUE INDEX IF NOT EXISTS idx_assets_hash ON assets(workspace_id, hash);
CREATE INDEX IF NOT EXISTS idx_assets_list ON assets(workspace_id, created_at DESC);

CREATE TABLE IF NOT EXISTS asset_refs (
  asset_id   TEXT NOT NULL,
  ref_type   TEXT NOT NULL,
  ref_id     TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (asset_id, ref_type, ref_id)
);
CREATE INDEX IF NOT EXISTS idx_asset_refs_target ON asset_refs(ref_type, ref_id);

CREATE TABLE IF NOT EXISTS asset_derivations (
  child_asset_id  TEXT NOT NULL,
  parent_asset_id TEXT NOT NULL,
  op              TEXT NOT NULL,
  created_at      TEXT NOT NULL,
  PRIMARY KEY (child_asset_id, parent_asset_id, op)
);

CREATE TABLE IF NOT EXISTS providers (
  id           TEXT NOT NULL,
  workspace_id TEXT NOT NULL,
  kind         TEXT NOT NULL DEFAULT 'builtin',
  name         TEXT NOT NULL,
  capabilities TEXT NOT NULL DEFAULT '',
  base_url     TEXT NOT NULL DEFAULT '',
  auth_kind    TEXT NOT NULL DEFAULT 'bearer',
  script       TEXT,
  enabled      INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL,
  PRIMARY KEY (workspace_id, id)
);

CREATE TABLE IF NOT EXISTS provider_credentials (
  id           TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  provider_id  TEXT NOT NULL,
  name         TEXT NOT NULL DEFAULT '',
  secret_ref   TEXT NOT NULL,
  masked       TEXT NOT NULL DEFAULT '',
  priority     INTEGER NOT NULL DEFAULT 0,
  limits       TEXT NOT NULL DEFAULT '{}',
  enabled      INTEGER NOT NULL DEFAULT 1,
  created_at   TEXT NOT NULL,
  created_by   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_credentials_provider ON provider_credentials(workspace_id, provider_id, priority);

CREATE TABLE IF NOT EXISTS runs (
  id           TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  canvas_id    TEXT NOT NULL DEFAULT '',
  trigger      TEXT NOT NULL DEFAULT 'manual',
  status       TEXT NOT NULL DEFAULT 'pending',
  targets      TEXT NOT NULL DEFAULT '[]',
  params       TEXT NOT NULL DEFAULT '{}',
  inputs       TEXT NOT NULL DEFAULT '[]',
  usage        TEXT NOT NULL DEFAULT '{}',
  error        TEXT,
  actor_id     TEXT NOT NULL DEFAULT '',
  idempotency_key TEXT,
  started_at   TEXT NOT NULL,
  finished_at  TEXT
);
CREATE INDEX IF NOT EXISTS idx_runs_canvas ON runs(canvas_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_runs_ws ON runs(workspace_id, started_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS idx_runs_idem ON runs(workspace_id, idempotency_key);

CREATE TABLE IF NOT EXISTS run_steps (
  id           TEXT PRIMARY KEY,
  run_id       TEXT NOT NULL,
  node_id      TEXT NOT NULL DEFAULT '',
  kind         TEXT NOT NULL DEFAULT 'generate',
  status       TEXT NOT NULL DEFAULT 'pending',
  depends_on   TEXT NOT NULL DEFAULT '[]',
  outputs      TEXT NOT NULL DEFAULT '[]',
  text         TEXT NOT NULL DEFAULT '',
  error        TEXT,
  started_at   TEXT NOT NULL,
  finished_at  TEXT
);
CREATE INDEX IF NOT EXISTS idx_steps_run ON run_steps(run_id);

CREATE TABLE IF NOT EXISTS run_attempts (
  id           TEXT PRIMARY KEY,
  step_id      TEXT NOT NULL,
  idx          INTEGER NOT NULL,
  provider_id  TEXT NOT NULL DEFAULT '',
  model_id     TEXT NOT NULL DEFAULT '',
  request_id   TEXT NOT NULL,
  status       TEXT NOT NULL DEFAULT 'running',
  http_status  INTEGER NOT NULL DEFAULT 0,
  latency_ms   INTEGER NOT NULL DEFAULT 0,
  tokens_in    INTEGER NOT NULL DEFAULT 0,
  tokens_out   INTEGER NOT NULL DEFAULT 0,
  cost_micros  INTEGER NOT NULL DEFAULT 0,
  remote_task_id TEXT,
  remote_provider TEXT,
  error        TEXT,
  created_at   TEXT NOT NULL
);
-- INV-3：同一 request_id 上游只计费一次
CREATE UNIQUE INDEX IF NOT EXISTS idx_attempts_request ON run_attempts(request_id);
CREATE INDEX IF NOT EXISTS idx_attempts_step ON run_attempts(step_id, idx);

CREATE TABLE IF NOT EXISTS prompt_sources (
  id           TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  name         TEXT NOT NULL,
  url          TEXT NOT NULL,
  format       TEXT NOT NULL DEFAULT 'json',
  refresh_interval TEXT NOT NULL DEFAULT '6h',
  enabled      INTEGER NOT NULL DEFAULT 1,
  status       TEXT NOT NULL DEFAULT 'idle',
  last_error   TEXT,
  last_synced_at TEXT,
  created_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS prompts (
  id           TEXT PRIMARY KEY,
  source_id    TEXT NOT NULL,
  workspace_id TEXT NOT NULL,
  external_id  TEXT NOT NULL,
  title        TEXT NOT NULL DEFAULT '',
  tags         TEXT NOT NULL DEFAULT '',
  content      TEXT NOT NULL DEFAULT '',
  variables    TEXT NOT NULL DEFAULT '[]',
  cover_url    TEXT,
  hash         TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_prompts_ext ON prompts(source_id, external_id);
CREATE INDEX IF NOT EXISTS idx_prompts_ws ON prompts(workspace_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS prompt_sync_logs (
  id           TEXT PRIMARY KEY,
  source_id    TEXT NOT NULL,
  status       TEXT NOT NULL,
  added        INTEGER NOT NULL DEFAULT 0,
  updated      INTEGER NOT NULL DEFAULT 0,
  removed      INTEGER NOT NULL DEFAULT 0,
  total        INTEGER NOT NULL DEFAULT 0,
  error        TEXT,
  created_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS plugins (
  workspace_id TEXT NOT NULL,
  plugin_key   TEXT NOT NULL,
  version      TEXT NOT NULL,
  manifest     TEXT NOT NULL DEFAULT '{}',
  permissions  TEXT NOT NULL DEFAULT '',
  enabled      INTEGER NOT NULL DEFAULT 0,
  trusted      INTEGER NOT NULL DEFAULT 0,
  builtin      INTEGER NOT NULL DEFAULT 0,
  bundle       BLOB,
  config       TEXT NOT NULL DEFAULT '{}',
  installed_by TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  PRIMARY KEY (workspace_id, plugin_key)
);

CREATE TABLE IF NOT EXISTS agent_sessions (
  id           TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  canvas_id    TEXT NOT NULL DEFAULT '',
  backend      TEXT NOT NULL DEFAULT 'http',
  thread_id    TEXT,
  title        TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS agent_turns (
  id           TEXT PRIMARY KEY,
  session_id   TEXT NOT NULL,
  seq          INTEGER NOT NULL,
  status       TEXT NOT NULL DEFAULT 'pending',
  usage        TEXT NOT NULL DEFAULT '{}',
  -- input / pending / error 必须持久化：审批与回放跨请求、跨端、跨重启
  input        TEXT NOT NULL DEFAULT '',
  pending      TEXT,
  error        TEXT,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_turns_session ON agent_turns(session_id, seq);

-- INV-7：实时事件与历史快照合并后不重复不丢失
CREATE TABLE IF NOT EXISTS agent_items (
  turn_id      TEXT NOT NULL,
  item_id      TEXT NOT NULL,
  seq          INTEGER NOT NULL,
  kind         TEXT NOT NULL,
  payload      TEXT NOT NULL,
  source       TEXT NOT NULL DEFAULT 'live',
  created_at   TEXT NOT NULL,
  PRIMARY KEY (turn_id, item_id)
);
CREATE INDEX IF NOT EXISTS idx_items_turn ON agent_items(turn_id, seq);

CREATE TABLE IF NOT EXISTS audit_logs (
  id           TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL DEFAULT '',
  actor_id     TEXT NOT NULL DEFAULT '',
  action       TEXT NOT NULL,
  target_type  TEXT NOT NULL DEFAULT '',
  target_id    TEXT NOT NULL DEFAULT '',
  detail       TEXT NOT NULL DEFAULT '{}',
  ip           TEXT NOT NULL DEFAULT '',
  trace_id     TEXT NOT NULL DEFAULT '',
  created_at   TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_ws ON audit_logs(workspace_id, created_at DESC);

CREATE TABLE IF NOT EXISTS idempotency_keys (
  scope        TEXT NOT NULL,
  key          TEXT NOT NULL,
  response     TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  PRIMARY KEY (scope, key)
);

CREATE TABLE IF NOT EXISTS schema_migrations (
  version      TEXT PRIMARY KEY,
  applied_at   TEXT NOT NULL
);
