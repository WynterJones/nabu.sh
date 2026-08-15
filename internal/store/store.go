// Package store provides Nabu's durable SQLite-backed operational state.
package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound          = errors.New("store: not found")
	ErrInvalidTransition = errors.New("store: invalid state transition")
	ErrClaimLost         = errors.New("store: claim lost")
)

// Store is safe for concurrent use. SQLite access is serialized to keep
// transaction behavior deterministic while WAL still permits other processes
// to read the database.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open opens path, enables SQLite safety/concurrency pragmas, and applies all
// outstanding migrations. The parent directory is created when needed.
func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("store: database path is empty")
	}
	if path != ":memory:" && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("store: create database directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open sqlite: %w", err)
	}
	// Pragmas such as foreign_keys are connection-local. A single underlying
	// connection guarantees that every operation observes the same settings.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, pragma := range []string{
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := db.ExecContext(ctx, pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("store: apply %q: %w", pragma, err)
		}
	}

	s := &Store{db: db, now: func() time.Time { return time.Now().UTC() }}
	if err := s.Migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Ping verifies the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

type migration struct {
	version int
	up      string
}

var migrations = []migration{
	{1, `
CREATE TABLE settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    display_name TEXT NOT NULL DEFAULT 'Nabu',
    setup_complete INTEGER NOT NULL DEFAULT 0 CHECK (setup_complete IN (0, 1)),
    paused INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1)),
    mission_started INTEGER NOT NULL DEFAULT 0 CHECK (mission_started IN (0, 1)),
    codex_path TEXT NOT NULL DEFAULT '',
    git_path TEXT NOT NULL DEFAULT '',
    server_address TEXT NOT NULL DEFAULT '127.0.0.1:7777',
    orientation_queued INTEGER NOT NULL DEFAULT 0 CHECK (orientation_queued IN (0, 1)),
    last_orientation_at TEXT
);
INSERT INTO settings (id) VALUES (1);

CREATE TABLE missions (
    id TEXT PRIMARY KEY,
    statement TEXT NOT NULL,
    context TEXT NOT NULL DEFAULT '',
    active INTEGER NOT NULL DEFAULT 0 CHECK (active IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX missions_one_active ON missions(active) WHERE active = 1;

CREATE TABLE workspaces (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    path TEXT NOT NULL UNIQUE,
    default_branch TEXT NOT NULL DEFAULT '',
    allowed INTEGER NOT NULL DEFAULT 0 CHECK (allowed IN (0, 1)),
    created_at TEXT NOT NULL
);

CREATE TABLE tasks (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    purpose TEXT NOT NULL DEFAULT '',
    why TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('idea','ready','running','waiting','needs_approval','completed','failed','cancelled')),
    priority TEXT NOT NULL CHECK (priority IN ('high','normal','low')),
    definition_of_done TEXT NOT NULL DEFAULT '[]',
    workspace_id TEXT REFERENCES workspaces(id) ON DELETE SET NULL,
    created_by TEXT NOT NULL DEFAULT '',
    parent_task_id TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    current_run_id TEXT,
    result TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT
);

CREATE TABLE runs (
    id TEXT PRIMARY KEY,
    task_id TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    type TEXT NOT NULL CHECK (type IN ('orient','execute','review')),
    status TEXT NOT NULL CHECK (status IN ('pending','running','completed','failed','cancelled','timed_out','interrupted')),
    pid INTEGER NOT NULL DEFAULT 0,
    working_directory TEXT NOT NULL DEFAULT '',
    command TEXT NOT NULL DEFAULT '[]',
    session_id TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL DEFAULT 0,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    exit_code INTEGER,
    stdout_path TEXT NOT NULL DEFAULT '',
    stderr_path TEXT NOT NULL DEFAULT '',
    raw_output TEXT NOT NULL DEFAULT '',
    result TEXT,
    error TEXT NOT NULL DEFAULT ''
);

CREATE TABLE events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT NOT NULL,
    entity_id TEXT NOT NULL DEFAULT '',
    data BLOB,
    created_at TEXT NOT NULL
);

CREATE TABLE artifacts (
    id TEXT PRIMARY KEY,
    task_id TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    run_id TEXT REFERENCES runs(id) ON DELETE SET NULL,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    path TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    metadata BLOB,
    created_at TEXT NOT NULL
);
`},
	{2, `
CREATE INDEX tasks_queue ON tasks(status, priority, created_at);
CREATE INDEX tasks_workspace ON tasks(workspace_id, created_at);
CREATE INDEX runs_task ON runs(task_id, started_at);
CREATE INDEX runs_status ON runs(status, started_at);
CREATE INDEX events_created ON events(created_at);
CREATE INDEX events_type ON events(type, id);
CREATE INDEX artifacts_task ON artifacts(task_id, created_at);
CREATE INDEX artifacts_run ON artifacts(run_id, created_at);

CREATE TABLE schedules (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    expression TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    action TEXT NOT NULL,
    last_run_at TEXT,
    next_run_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE reports (
    id TEXT PRIMARY KEY,
    task_id TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    title TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    role TEXT NOT NULL,
    content TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE approvals (
    id TEXT PRIMARY KEY,
    task_id TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    run_id TEXT REFERENCES runs(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT NOT NULL,
    resolved_at TEXT
);
`},
	{3, `
CREATE TABLE policy (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    read TEXT NOT NULL DEFAULT 'allow',
    work TEXT NOT NULL DEFAULT 'allow',
    publish TEXT NOT NULL DEFAULT 'ask',
    dangerous TEXT NOT NULL DEFAULT 'ask'
);
INSERT INTO policy (id) VALUES (1);
`},
	{4, `
ALTER TABLE messages ADD COLUMN effect TEXT NOT NULL DEFAULT 'conversation_only';
ALTER TABLE messages ADD COLUMN effect_metadata BLOB;
ALTER TABLE messages ADD COLUMN updated_at TEXT;
UPDATE messages SET updated_at = created_at WHERE updated_at IS NULL;

ALTER TABLE approvals ADD COLUMN proposed_change TEXT NOT NULL DEFAULT '';
ALTER TABLE approvals ADD COLUMN evidence TEXT NOT NULL DEFAULT '';
ALTER TABLE approvals ADD COLUMN evidence_metadata BLOB;
ALTER TABLE approvals ADD COLUMN rejection_note TEXT NOT NULL DEFAULT '';
ALTER TABLE approvals ADD COLUMN updated_at TEXT;
ALTER TABLE approvals ADD COLUMN expires_at TEXT;
UPDATE approvals SET updated_at = created_at WHERE updated_at IS NULL;

ALTER TABLE reports ADD COLUMN kind TEXT NOT NULL DEFAULT '';
ALTER TABLE reports ADD COLUMN body TEXT NOT NULL DEFAULT '';
ALTER TABLE reports ADD COLUMN updated_at TEXT;
UPDATE reports SET updated_at = created_at WHERE updated_at IS NULL;

ALTER TABLE schedules ADD COLUMN kind TEXT NOT NULL DEFAULT 'task';
ALTER TABLE schedules ADD COLUMN interval_seconds INTEGER NOT NULL DEFAULT 0;
ALTER TABLE schedules ADD COLUMN payload BLOB;
ALTER TABLE schedules ADD COLUMN claim_token TEXT NOT NULL DEFAULT '';
ALTER TABLE schedules ADD COLUMN claimed_at TEXT;
ALTER TABLE schedules ADD COLUMN lease_until TEXT;
ALTER TABLE schedules ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
UPDATE schedules
SET kind = CASE WHEN action IN ('script', 'task', 'orient') THEN action ELSE 'task' END;
`},
	{5, `
CREATE TABLE report_tasks (
    report_id TEXT NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    PRIMARY KEY (report_id, task_id)
);
INSERT OR IGNORE INTO report_tasks(report_id, task_id)
SELECT id, task_id FROM reports WHERE task_id IS NOT NULL;

CREATE TABLE report_artifacts (
    report_id TEXT NOT NULL REFERENCES reports(id) ON DELETE CASCADE,
    artifact_id TEXT NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    PRIMARY KEY (report_id, artifact_id)
);

CREATE TABLE scripts (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    path TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    timeout_seconds INTEGER NOT NULL DEFAULT 0 CHECK (timeout_seconds >= 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE script_runs (
    id TEXT PRIMARY KEY,
    script_id TEXT NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    schedule_id TEXT REFERENCES schedules(id) ON DELETE SET NULL,
    status TEXT NOT NULL CHECK (status IN ('pending','running','completed','failed','cancelled','timed_out','interrupted')),
    pid INTEGER NOT NULL DEFAULT 0,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    exit_code INTEGER,
    stdout_path TEXT NOT NULL DEFAULT '',
    stderr_path TEXT NOT NULL DEFAULT '',
    result BLOB,
    error TEXT NOT NULL DEFAULT ''
);

ALTER TABLE artifacts ADD COLUMN script_run_id TEXT REFERENCES script_runs(id) ON DELETE SET NULL;

CREATE TABLE memory_updates (
    id TEXT PRIMARY KEY,
    target TEXT NOT NULL CHECK (target IN ('durable','daily')),
    content TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('proposed','applied','rejected')),
    rejection_note TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    resolved_at TEXT
);

CREATE INDEX messages_chronology ON messages(created_at, id);
CREATE INDEX approvals_status ON approvals(status, created_at);
CREATE INDEX approvals_task ON approvals(task_id, created_at);
CREATE INDEX report_tasks_task ON report_tasks(task_id, report_id);
CREATE INDEX report_artifacts_artifact ON report_artifacts(artifact_id, report_id);
CREATE INDEX schedules_due ON schedules(enabled, next_run_at, lease_until);
CREATE INDEX script_runs_script ON script_runs(script_id, started_at);
CREATE INDEX script_runs_status ON script_runs(status, started_at);
CREATE INDEX artifacts_script_run ON artifacts(script_run_id, created_at);
CREATE INDEX memory_updates_status ON memory_updates(status, created_at);
`},
	{6, `
ALTER TABLE settings ADD COLUMN active_workspace_id TEXT REFERENCES workspaces(id) ON DELETE SET NULL;
UPDATE settings
SET active_workspace_id = (SELECT id FROM workspaces ORDER BY created_at, id LIMIT 1)
WHERE id = 1;

ALTER TABLE missions ADD COLUMN workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE;
ALTER TABLE messages ADD COLUMN workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE;
ALTER TABLE reports ADD COLUMN workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE;
ALTER TABLE approvals ADD COLUMN workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE;
ALTER TABLE schedules ADD COLUMN workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE;
ALTER TABLE memory_updates ADD COLUMN workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE;
ALTER TABLE events ADD COLUMN workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE;

UPDATE missions SET workspace_id = (SELECT active_workspace_id FROM settings WHERE id = 1) WHERE workspace_id IS NULL;
UPDATE messages SET workspace_id = (SELECT active_workspace_id FROM settings WHERE id = 1) WHERE workspace_id IS NULL;
UPDATE reports SET workspace_id = (SELECT active_workspace_id FROM settings WHERE id = 1) WHERE workspace_id IS NULL;
UPDATE approvals SET workspace_id = (SELECT active_workspace_id FROM settings WHERE id = 1) WHERE workspace_id IS NULL;
UPDATE schedules SET workspace_id = (SELECT active_workspace_id FROM settings WHERE id = 1) WHERE workspace_id IS NULL;
UPDATE memory_updates SET workspace_id = (SELECT active_workspace_id FROM settings WHERE id = 1) WHERE workspace_id IS NULL;
UPDATE events SET workspace_id = (SELECT active_workspace_id FROM settings WHERE id = 1) WHERE workspace_id IS NULL;

DROP INDEX missions_one_active;
CREATE UNIQUE INDEX missions_one_active_per_workspace
ON missions(COALESCE(workspace_id, '')) WHERE active = 1;
CREATE INDEX missions_workspace ON missions(workspace_id, created_at);
CREATE INDEX messages_workspace_chronology ON messages(workspace_id, id);
CREATE INDEX reports_workspace ON reports(workspace_id, created_at);
CREATE INDEX approvals_workspace_status ON approvals(workspace_id, status, created_at);
CREATE INDEX schedules_workspace_due ON schedules(workspace_id, enabled, next_run_at);
CREATE INDEX memory_updates_workspace ON memory_updates(workspace_id, status, created_at);
CREATE INDEX events_workspace ON events(workspace_id, id);
`},
	{7, `
CREATE TABLE runs_new (
    id TEXT PRIMARY KEY,
    task_id TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    type TEXT NOT NULL CHECK (type IN ('orient','execute','review','chat')),
    status TEXT NOT NULL CHECK (status IN ('pending','running','completed','failed','cancelled','timed_out','interrupted')),
    pid INTEGER NOT NULL DEFAULT 0,
    working_directory TEXT NOT NULL DEFAULT '',
    command TEXT NOT NULL DEFAULT '[]',
    session_id TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL DEFAULT 0,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    exit_code INTEGER,
    stdout_path TEXT NOT NULL DEFAULT '',
    stderr_path TEXT NOT NULL DEFAULT '',
    raw_output TEXT NOT NULL DEFAULT '',
    result TEXT,
    error TEXT NOT NULL DEFAULT ''
);
INSERT INTO runs_new SELECT * FROM runs;
DROP TABLE runs;
ALTER TABLE runs_new RENAME TO runs;
CREATE INDEX runs_task ON runs(task_id, started_at);
CREATE INDEX runs_status ON runs(status, started_at);
`},
	{8, `
ALTER TABLE messages ADD COLUMN parent_message_id INTEGER REFERENCES messages(id) ON DELETE CASCADE;
ALTER TABLE messages ADD COLUMN thread_root_id INTEGER REFERENCES messages(id) ON DELETE CASCADE;
CREATE INDEX messages_workspace_top_level ON messages(workspace_id, id) WHERE parent_message_id IS NULL;
CREATE INDEX messages_thread ON messages(thread_root_id, id);
`},
	{9, `
ALTER TABLE workspaces ADD COLUMN mission_started INTEGER NOT NULL DEFAULT 0 CHECK (mission_started IN (0, 1));
UPDATE workspaces
SET mission_started = (SELECT mission_started FROM settings WHERE id = 1)
WHERE id = (SELECT active_workspace_id FROM settings WHERE id = 1);
`},
	{10, `
ALTER TABLE workspaces ADD COLUMN orientation_queued INTEGER NOT NULL DEFAULT 0 CHECK (orientation_queued IN (0, 1));
ALTER TABLE workspaces ADD COLUMN last_orientation_at TEXT;
UPDATE workspaces
SET orientation_queued = (SELECT orientation_queued FROM settings WHERE id = 1),
    last_orientation_at = (SELECT last_orientation_at FROM settings WHERE id = 1)
WHERE id = (SELECT active_workspace_id FROM settings WHERE id = 1);
`},
	{11, `
ALTER TABLE settings ADD COLUMN last_backup_at TEXT;
`},
	{12, `
ALTER TABLE settings ADD COLUMN codex_model TEXT NOT NULL DEFAULT '';
ALTER TABLE settings ADD COLUMN codex_reasoning_effort TEXT NOT NULL DEFAULT '';
`},
	{13, `
CREATE TABLE workspace_policies (
    workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    read TEXT NOT NULL DEFAULT 'allow',
    work TEXT NOT NULL DEFAULT 'allow',
    publish TEXT NOT NULL DEFAULT 'ask',
    dangerous TEXT NOT NULL DEFAULT 'ask'
);
INSERT INTO workspace_policies(workspace_id, read, work, publish, dangerous)
SELECT w.id, p.read, p.work, p.publish, p.dangerous FROM workspaces w CROSS JOIN policy p WHERE p.id = 1;

ALTER TABLE scripts ADD COLUMN workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE;
UPDATE scripts SET workspace_id = (SELECT active_workspace_id FROM settings WHERE id = 1);
CREATE INDEX scripts_workspace ON scripts(workspace_id, name, id);
`},
	{14, `
CREATE TABLE scripts_new (
    id TEXT PRIMARY KEY,
    workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    path TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    timeout_seconds INTEGER NOT NULL DEFAULT 0 CHECK (timeout_seconds >= 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(workspace_id, name),
    UNIQUE(workspace_id, path)
);
INSERT INTO scripts_new(id, workspace_id, name, path, description, enabled, timeout_seconds, created_at, updated_at)
SELECT id, workspace_id, name, path, description, enabled, timeout_seconds, created_at, updated_at FROM scripts;
DROP TABLE scripts;
ALTER TABLE scripts_new RENAME TO scripts;
CREATE INDEX scripts_workspace ON scripts(workspace_id, name, id);
`},
	{15, `
ALTER TABLE tasks ADD COLUMN planned_at TEXT;
CREATE INDEX tasks_workspace_planned ON tasks(workspace_id, planned_at, id);
ALTER TABLE workspaces ADD COLUMN icon_path TEXT NOT NULL DEFAULT '';

CREATE TABLE integrations (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    provider TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('draft','needs_credentials','verifying','ready','failed','disabled')),
    manifest BLOB NOT NULL DEFAULT '{}',
    credential_requirements BLOB NOT NULL DEFAULT '[]',
    allowed_hosts BLOB NOT NULL DEFAULT '[]',
    last_verified_at TEXT,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(workspace_id, name)
);
CREATE INDEX integrations_workspace_status ON integrations(workspace_id, status, name, id);
CREATE INDEX integrations_workspace_provider ON integrations(workspace_id, provider, name, id);

CREATE TABLE plans (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    objective TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('proposed','active','completed','archived')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX plans_workspace_status ON plans(workspace_id, status, updated_at, id);

CREATE TABLE plan_items (
    id TEXT PRIMARY KEY,
    plan_id TEXT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    kind TEXT NOT NULL CHECK (kind IN ('task','schedule','milestone')),
    title TEXT NOT NULL,
    purpose TEXT NOT NULL DEFAULT '',
    why TEXT NOT NULL DEFAULT '',
    planned_at TEXT,
    cadence BLOB,
    position INTEGER NOT NULL DEFAULT 0 CHECK (position >= 0),
    task_id TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    schedule_id TEXT REFERENCES schedules(id) ON DELETE SET NULL,
    status TEXT NOT NULL CHECK (status IN ('proposed','accepted','skipped')),
    UNIQUE(plan_id, position)
);
CREATE INDEX plan_items_plan_position ON plan_items(plan_id, position, id);
CREATE INDEX plan_items_task ON plan_items(task_id) WHERE task_id IS NOT NULL;
CREATE INDEX plan_items_schedule ON plan_items(schedule_id) WHERE schedule_id IS NOT NULL;
`},
	{16, `
CREATE TABLE datasets (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    slug TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    schema_json BLOB NOT NULL DEFAULT '[]',
    unique_key_json BLOB NOT NULL DEFAULT '[]',
    row_count INTEGER NOT NULL DEFAULT 0 CHECK (row_count >= 0),
    deleted_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(workspace_id, slug)
);
CREATE INDEX datasets_workspace_deleted ON datasets(workspace_id, deleted_at, updated_at, id);

CREATE TABLE dataset_rows (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    dataset_id TEXT NOT NULL REFERENCES datasets(id) ON DELETE CASCADE,
    values_json BLOB NOT NULL,
    search_text TEXT NOT NULL DEFAULT '',
    unique_fingerprint TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX dataset_rows_dataset_id ON dataset_rows(dataset_id, id);
CREATE INDEX dataset_rows_dataset_updated ON dataset_rows(dataset_id, updated_at, id);
CREATE INDEX dataset_rows_dataset_search ON dataset_rows(dataset_id, search_text);
CREATE UNIQUE INDEX dataset_rows_unique_fingerprint
ON dataset_rows(dataset_id, unique_fingerprint) WHERE unique_fingerprint <> '';
`},
	{17, `
ALTER TABLE messages ADD COLUMN status TEXT NOT NULL DEFAULT 'complete'
    CHECK (status IN ('queued','processing','complete','failed'));
CREATE INDEX messages_queue_order ON messages(status, id);
`},
	{18, `
ALTER TABLE workspaces ADD COLUMN context_ready INTEGER NOT NULL DEFAULT 0
    CHECK (context_ready IN (0, 1));
ALTER TABLE workspaces ADD COLUMN context_prompted INTEGER NOT NULL DEFAULT 0
    CHECK (context_prompted IN (0, 1));
`},
	{19, `
CREATE TABLE memory_updates_new (
    id TEXT PRIMARY KEY,
    workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
    target TEXT NOT NULL CHECK (target IN ('durable','daily','soul')),
    content TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('proposed','applied','rejected')),
    rejection_note TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    resolved_at TEXT
);
INSERT INTO memory_updates_new(id, workspace_id, target, content, source, status, rejection_note, created_at, resolved_at)
SELECT id, workspace_id, target, content, source, status, rejection_note, created_at, resolved_at FROM memory_updates;
DROP TABLE memory_updates;
ALTER TABLE memory_updates_new RENAME TO memory_updates;
CREATE INDEX memory_updates_status ON memory_updates(status, created_at);
CREATE INDEX memory_updates_workspace ON memory_updates(workspace_id, status, created_at);
`},
	{20, `
ALTER TABLE scripts ADD COLUMN access TEXT NOT NULL DEFAULT 'read'
    CHECK (access IN ('read','write'));

CREATE TABLE secret_records (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    label TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    reference_key TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(workspace_id, name),
    UNIQUE(workspace_id, reference_key)
);
CREATE INDEX secret_records_workspace_name ON secret_records(workspace_id, name, id);

CREATE TABLE script_credential_bindings (
    script_id TEXT NOT NULL REFERENCES scripts(id) ON DELETE CASCADE,
    env TEXT NOT NULL CHECK (length(env) BETWEEN 1 AND 128),
    secret_record_id TEXT NOT NULL REFERENCES secret_records(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(script_id, env)
);
CREATE INDEX script_credential_bindings_secret ON script_credential_bindings(secret_record_id, script_id);
`},
	{21, `
ALTER TABLE reports ADD COLUMN status TEXT NOT NULL DEFAULT 'unread'
    CHECK (status IN ('unread','read','archived'));
CREATE INDEX reports_workspace_status_created
ON reports(workspace_id, status, created_at, id);
`},
	{22, `
CREATE TABLE idle_steward_state (
    workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id) ON DELETE CASCADE,
    empty_checks INTEGER NOT NULL DEFAULT 0 CHECK (empty_checks >= 0),
    last_run_at TEXT,
    next_run_at TEXT
);
CREATE INDEX idle_steward_next_run ON idle_steward_state(next_run_at);
`},
	{23, `
CREATE TABLE mcp_servers (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    transport TEXT NOT NULL CHECK (transport IN ('stdio','http')),
    command TEXT NOT NULL DEFAULT '',
    args_json BLOB NOT NULL DEFAULT '[]',
    url TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    access TEXT NOT NULL DEFAULT 'read' CHECK (access IN ('read','full')),
    required INTEGER NOT NULL DEFAULT 0 CHECK (required IN (0, 1)),
    startup_timeout_seconds INTEGER NOT NULL DEFAULT 10 CHECK (startup_timeout_seconds BETWEEN 1 AND 120),
    tool_timeout_seconds INTEGER NOT NULL DEFAULT 60 CHECK (tool_timeout_seconds BETWEEN 1 AND 600),
    enabled_tools_json BLOB NOT NULL DEFAULT '[]',
    disabled_tools_json BLOB NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(workspace_id, name)
);
CREATE INDEX mcp_servers_workspace_enabled ON mcp_servers(workspace_id, enabled, name, id);

CREATE TABLE mcp_secret_bindings (
    server_id TEXT NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    env_var TEXT NOT NULL CHECK (length(env_var) BETWEEN 1 AND 128),
    secret_record_id TEXT NOT NULL REFERENCES secret_records(id) ON DELETE RESTRICT,
    header_name TEXT NOT NULL DEFAULT '',
    bearer INTEGER NOT NULL DEFAULT 0 CHECK (bearer IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(server_id, env_var)
);
CREATE INDEX mcp_secret_bindings_secret ON mcp_secret_bindings(secret_record_id, server_id);
`},
	{24, `
ALTER TABLE mcp_servers ADD COLUMN auth TEXT NOT NULL DEFAULT 'none'
    CHECK (auth IN ('none','oauth','secret'));

-- Remote connectors created before explicit authentication modes existed were
-- optimistically treated as public. Most hosted MCP servers use OAuth, so make
-- those legacy, unbound connectors request sign-in instead of advertising tools
-- that Codex cannot call. Public servers can still be changed back to "none".
UPDATE mcp_servers
SET auth = 'oauth'
WHERE transport = 'http'
  AND NOT EXISTS (
      SELECT 1 FROM mcp_secret_bindings
      WHERE mcp_secret_bindings.server_id = mcp_servers.id
  );
`},
	{25, `
ALTER TABLE settings ADD COLUMN max_parallel_tasks INTEGER NOT NULL DEFAULT 1
    CHECK (max_parallel_tasks BETWEEN 1 AND 8);

CREATE TABLE task_dependencies (
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    prerequisite_task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY(task_id, prerequisite_task_id),
    CHECK(task_id <> prerequisite_task_id)
);
CREATE INDEX task_dependencies_prerequisite
ON task_dependencies(prerequisite_task_id, task_id);
`},
	{26, `
CREATE TABLE local_apps (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    directory TEXT NOT NULL,
    command_json BLOB NOT NULL,
    port INTEGER NOT NULL CHECK (port BETWEEN 1024 AND 65535),
    health_path TEXT NOT NULL DEFAULT '/',
    auto_start INTEGER NOT NULL DEFAULT 0 CHECK (auto_start IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(workspace_id, name),
    UNIQUE(workspace_id, port)
);
CREATE INDEX local_apps_workspace_name ON local_apps(workspace_id, name, id);
`},
	{27, `
ALTER TABLE tasks ADD COLUMN run_requested_at TEXT;
CREATE INDEX tasks_run_requested
ON tasks(run_requested_at, workspace_id, status)
WHERE run_requested_at IS NOT NULL;
`},
}

// Migrate applies every numbered migration exactly once.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
)`); err != nil {
		return fmt.Errorf("store: create migration table: %w", err)
	}

	for _, m := range migrations {
		var applied int
		err := s.db.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE version = ?", m.version).Scan(&applied)
		if err == nil {
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("store: inspect migration %d: %w", m.version, err)
		}

		foreignKeysDisabled := m.version == 7 || m.version == 14
		if foreignKeysDisabled {
			if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
				return fmt.Errorf("store: disable foreign keys for migration %d: %w", m.version, err)
			}
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			if foreignKeysDisabled {
				_, _ = s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
			}
			return fmt.Errorf("store: begin migration %d: %w", m.version, err)
		}
		if _, err := tx.ExecContext(ctx, m.up); err != nil {
			_ = tx.Rollback()
			if foreignKeysDisabled {
				_, _ = s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
			}
			return fmt.Errorf("store: apply migration %d: %w", m.version, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)", m.version, formatTime(s.now())); err != nil {
			_ = tx.Rollback()
			if foreignKeysDisabled {
				_, _ = s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
			}
			return fmt.Errorf("store: record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			if foreignKeysDisabled {
				_, _ = s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON")
			}
			return fmt.Errorf("store: commit migration %d: %w", m.version, err)
		}
		if foreignKeysDisabled {
			if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
				return fmt.Errorf("store: restore foreign keys after migration %d: %w", m.version, err)
			}
		}
	}
	return nil
}

// SchemaVersion returns the newest applied migration number.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version); err != nil {
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	return version, nil
}

func newID() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", fmt.Errorf("store: generate id: %w", err)
	}
	// UUID v4 layout, encoded without a dependency.
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	buf := make([]byte, 36)
	hex.Encode(buf[0:8], data[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], data[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], data[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], data[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], data[10:16])
	return string(buf), nil
}

func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func defaultTime(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback
	}
	return t.UTC()
}

func parseTime(value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: parse timestamp %q: %w", value, err)
	}
	return t, nil
}

func notFound(entity string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", entity, ErrNotFound)
	}
	return err
}

// activeWorkspaceID returns the selected scope, or an empty string for a
// legacy installation without workspaces.
func (s *Store) activeWorkspaceID(ctx context.Context) (string, error) {
	var id sql.NullString
	if err := s.db.QueryRowContext(ctx, "SELECT active_workspace_id FROM settings WHERE id = 1").Scan(&id); err != nil {
		return "", fmt.Errorf("store: get active workspace id: %w", err)
	}
	return id.String, nil
}

func (s *Store) defaultWorkspaceID(ctx context.Context, id string) (string, error) {
	if id != "" {
		return id, nil
	}
	return s.activeWorkspaceID(ctx)
}
