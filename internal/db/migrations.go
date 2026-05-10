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
  role TEXT NOT NULL CHECK(role IN ('editor','viewer')),
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  PRIMARY KEY (project_id, username)
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
  link_group_id TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS ingest_jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  clip_id INTEGER NOT NULL REFERENCES clips(id) ON DELETE CASCADE,
  state TEXT NOT NULL CHECK(state IN ('PENDING','PROCESSING','SUCCESS','FAILED')),
  stage TEXT NOT NULL DEFAULT '',
  progress_pct REAL NOT NULL DEFAULT 0,
  progress_time_ms INTEGER NOT NULL DEFAULT 0,
  total_duration_ms INTEGER NOT NULL DEFAULT 0,
  ffmpeg_frame INTEGER NOT NULL DEFAULT 0,
  ffmpeg_fps REAL NOT NULL DEFAULT 0,
  ffmpeg_bitrate TEXT NOT NULL DEFAULT '',
  ffmpeg_speed TEXT NOT NULL DEFAULT '',
  last_log TEXT NOT NULL DEFAULT '',
  error TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  started_at TEXT,
  finished_at TEXT,
  updated_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS markers (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  marker_ts_ms INTEGER NOT NULL,
  author_username TEXT NOT NULL REFERENCES users(username),
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
CREATE INDEX IF NOT EXISTS idx_perspectives_project_sort ON perspectives(project_id, sort_order, id);
CREATE INDEX IF NOT EXISTS idx_tracks_project_sort ON tracks(project_id, sort_order, id);
CREATE INDEX IF NOT EXISTS idx_clips_project_ingest_status ON clips(project_id, ingest_status);
CREATE INDEX IF NOT EXISTS idx_ingest_jobs_project_state_clip ON ingest_jobs(project_id, state, clip_id, id);
CREATE INDEX IF NOT EXISTS idx_ingest_jobs_state_id ON ingest_jobs(state, id);
CREATE INDEX IF NOT EXISTS idx_clips_project_link_group ON clips(project_id, link_group_id);
`},
// Migration 2: sessions table had denormalized user fields (display_name, user_color,
// can_create_projects). These were stale copies of users table data. Drop them and
// join to users at query time. Also drop the dead project_acl_rules table that was
// never queried. Also drop author_color from markers/regions — color is always read
// from users table with a JOIN; the stored copy became stale when users changed colors.
{2, `
-- sessions: drop denormalized user columns; we JOIN users at lookup time
ALTER TABLE sessions ADD COLUMN _v2_marker INTEGER NOT NULL DEFAULT 1;

CREATE TABLE sessions_v2 (
  token_hash TEXT PRIMARY KEY,
  username TEXT NOT NULL REFERENCES users(username) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO sessions_v2(token_hash, username, expires_at, created_at)
  SELECT token_hash, username, expires_at, created_at FROM sessions;
DROP TABLE sessions;
ALTER TABLE sessions_v2 RENAME TO sessions;

-- markers/regions: drop denormalized author_color; always join users.color
CREATE TABLE markers_v2 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  marker_ts_ms INTEGER NOT NULL,
  author_username TEXT NOT NULL REFERENCES users(username),
  label TEXT NOT NULL,
  note TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO markers_v2(id, project_id, marker_ts_ms, author_username, label, note, created_at, updated_at)
  SELECT id, project_id, marker_ts_ms, author_username, label, COALESCE(note,''), created_at, updated_at FROM markers;
DROP TABLE markers;
ALTER TABLE markers_v2 RENAME TO markers;

CREATE TABLE regions_v2 (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
  region_start_ms INTEGER NOT NULL,
  region_end_ms INTEGER NOT NULL,
  author_username TEXT NOT NULL REFERENCES users(username),
  label TEXT NOT NULL,
  note TEXT,
  created_at TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at TEXT NOT NULL DEFAULT (datetime('now')),
  CHECK(region_end_ms >= region_start_ms)
);
INSERT INTO regions_v2(id, project_id, region_start_ms, region_end_ms, author_username, label, note, created_at, updated_at)
  SELECT id, project_id, region_start_ms, region_end_ms, author_username, label, COALESCE(note,''), created_at, updated_at FROM regions;
DROP TABLE regions;
ALTER TABLE regions_v2 RENAME TO regions;

-- drop the dead ACL rules table — never queried, never enforced
DROP TABLE IF EXISTS project_acl_rules;

-- restore indexes dropped with old tables
CREATE INDEX IF NOT EXISTS idx_markers_project_ts ON markers(project_id, marker_ts_ms);
CREATE INDEX IF NOT EXISTS idx_regions_project_ts ON regions(project_id, region_start_ms);
`},
// Migration 3: consolidate 'member' role → 'editor'. The schema allowed 'member' as a
// synonym for 'editor' in code but the CHECK constraint accepted it in old rows.
{3, `
UPDATE project_memberships SET role='editor' WHERE role='member';
`},
// Migration 4-6 from original (indexes + ingest job fields + link_group_id) are
// already folded into migration 1 above for fresh installs. These run only on
// databases that have older migrations recorded but don't have the columns yet.
// They are no-ops on fresh installs because the schema_migrations table will have
// version 1 covering everything.
// Migrations 4-6 from original codebase are no-ops here; their schema changes are
// folded into migration 1 above.  The SELECT 1 entries exist so existing databases
// that have versions 4-6 already recorded don't error on startup.
{4, `SELECT 1`},
{5, `SELECT 1`},
{6, `SELECT 1`},
// Migration 7: index strong_hash for ETag-based revision dedup.
{7, `
CREATE INDEX IF NOT EXISTS idx_source_asset_revisions_strong_hash
  ON source_asset_revisions(source_asset_id, strong_hash)
  WHERE strong_hash IS NOT NULL;
`},
}

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
