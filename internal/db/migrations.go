package db

import (
	"context"
	"database/sql"
	"fmt"
)

type migration struct {
	version int
	sql     string
}

var migrations = []migration{{1, `
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS users (
  username TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  color TEXT NOT NULL,
  can_create_projects INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY,
  username TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
  display_name TEXT NOT NULL,
  user_color TEXT NOT NULL,
  can_create_projects INTEGER NOT NULL DEFAULT 0,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS projects (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  owner_username TEXT NOT NULL REFERENCES users(username),
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS project_memberships (
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  username TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
  role TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY (project_id, username)
);
CREATE TABLE IF NOT EXISTS project_acl_rules (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  subject_type TEXT NOT NULL,
  subject TEXT NOT NULL,
  permission TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS perspectives (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS tracks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  perspective_id INTEGER NOT NULL REFERENCES perspectives(id) ON DELETE CASCADE,
  kind TEXT NOT NULL CHECK(kind IN ('video','audio')),
  name TEXT NOT NULL,
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS source_assets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  adapter TEXT NOT NULL,
  bucket TEXT NOT NULL DEFAULT '',
  current_key TEXT NOT NULL DEFAULT '',
  current_path TEXT NOT NULL DEFAULT '',
  display_name TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS source_asset_revisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  source_asset_id INTEGER NOT NULL REFERENCES source_assets(id) ON DELETE CASCADE,
  adapter TEXT NOT NULL,
  bucket TEXT NOT NULL DEFAULT '',
  key TEXT NOT NULL DEFAULT '',
  path TEXT NOT NULL DEFAULT '',
  size_bytes INTEGER NOT NULL,
  fingerprint_json TEXT NOT NULL,
  strong_hash TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS hls_assets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  source_revision_id INTEGER NOT NULL REFERENCES source_asset_revisions(id),
  adapter TEXT NOT NULL,
  bucket TEXT NOT NULL DEFAULT '',
  playlist_key TEXT NOT NULL DEFAULT '',
  playlist_path TEXT NOT NULL DEFAULT '',
  stream_id TEXT NOT NULL,
  media_kind TEXT NOT NULL CHECK(media_kind IN ('video','audio')),
  transcode_profile_version TEXT NOT NULL,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  fps_num INTEGER,
  fps_den INTEGER,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  UNIQUE(source_revision_id, stream_id, transcode_profile_version)
);
CREATE TABLE IF NOT EXISTS clips (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  perspective_id INTEGER NOT NULL REFERENCES perspectives(id) ON DELETE CASCADE,
  track_id INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
  source_asset_id INTEGER NOT NULL REFERENCES source_assets(id),
  source_revision_id INTEGER NOT NULL REFERENCES source_asset_revisions(id),
  hls_asset_id INTEGER REFERENCES hls_assets(id),
  media_kind TEXT NOT NULL CHECK(media_kind IN ('video','audio')),
  wallclock_start_ms INTEGER NOT NULL,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  fps_num INTEGER,
  fps_den INTEGER,
  stream_index INTEGER NOT NULL DEFAULT 0,
  display_name TEXT NOT NULL,
  ingest_status TEXT NOT NULL DEFAULT 'PENDING' CHECK(ingest_status IN ('PENDING','PROCESSING','SUCCESS','FAILED')),
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS ingest_jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  clip_id INTEGER NOT NULL REFERENCES clips(id) ON DELETE CASCADE,
  state TEXT NOT NULL CHECK(state IN ('PENDING','PROCESSING','SUCCESS','FAILED')),
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  started_at TEXT,
  finished_at TEXT
);
CREATE TABLE IF NOT EXISTS markers (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  marker_ts_ms INTEGER NOT NULL,
  author_username TEXT NOT NULL REFERENCES users(username),
  author_color TEXT NOT NULL,
  label TEXT NOT NULL,
  note TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS regions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  region_start_ms INTEGER NOT NULL,
  region_end_ms INTEGER NOT NULL,
  author_username TEXT NOT NULL REFERENCES users(username),
  author_color TEXT NOT NULL,
  label TEXT NOT NULL,
  note TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),
  CHECK(region_end_ms >= region_start_ms)
);
CREATE INDEX IF NOT EXISTS idx_clips_project ON clips(project_id);
CREATE INDEX IF NOT EXISTS idx_markers_project_ts ON markers(project_id, marker_ts_ms);
CREATE INDEX IF NOT EXISTS idx_regions_project_ts ON regions(project_id, region_start_ms);
CREATE INDEX IF NOT EXISTS idx_ingest_jobs_state ON ingest_jobs(state);
`}}

func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL DEFAULT (datetime('now')));`); err != nil {
		return err
	}
	for _, m := range migrations {
		var exists int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, m.version).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", m.version, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES (?)`, m.version); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
