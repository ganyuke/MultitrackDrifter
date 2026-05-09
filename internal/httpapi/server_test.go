package httpapi

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/example/multitrack-drifter/internal/config"
	dbpkg "github.com/example/multitrack-drifter/internal/db"
	"github.com/example/multitrack-drifter/internal/ingest"
	"github.com/example/multitrack-drifter/internal/storage"
)

func TestCreateSourceRevisionClipsCreatesFreshClipWhenHLSExists(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t, ctx)

	projectID := insertTestProject(t, ctx, db)
	info := storage.ObjectInfo{
		Name:      "camera-a.mp4",
		SizeBytes: 42,
		ETag:      "etag-a",
		Ref: storage.ObjectRef{
			Adapter: "local",
			Path:    "camera-a.mp4",
		},
	}
	assetID, revID := insertTestSourceRevision(t, ctx, db, info, "Camera A")
	hlsID := insertTestHLSAsset(t, ctx, db, revID, "stream-0", "test-profile")

	s := &Server{db: db, cfg: config.Config{TranscodeProfile: "test-profile"}}
	req := createClipReq{
		Perspective:      "Main",
		Track:            "Video 0",
		Kind:             "video",
		DisplayName:      "Camera A",
		StreamIndex:      0,
		WallclockStartMS: 12000,
		DurationMS:       1000,
		FPSNum:           30,
		FPSDen:           1,
	}

	firstClipIDs, firstPendingClipIDs, firstReusedClipIDs, err := s.createSourceRevisionClips(ctx, projectID, []createClipReq{req}, info)
	if err != nil {
		t.Fatalf("first createSourceRevisionClips: %v", err)
	}
	secondClipIDs, secondPendingClipIDs, secondReusedClipIDs, err := s.createSourceRevisionClips(ctx, projectID, []createClipReq{req}, info)
	if err != nil {
		t.Fatalf("second createSourceRevisionClips: %v", err)
	}

	if len(firstClipIDs) != 1 || len(secondClipIDs) != 1 {
		t.Fatalf("expected one clip per import, got first=%v second=%v", firstClipIDs, secondClipIDs)
	}
	if firstClipIDs[0] == secondClipIDs[0] {
		t.Fatalf("expected duplicate source import to create a fresh timeline clip, got reused id %d", firstClipIDs[0])
	}
	if len(firstPendingClipIDs) != 0 || len(secondPendingClipIDs) != 0 {
		t.Fatalf("expected existing HLS to make both imports immediately successful, got pending first=%v second=%v", firstPendingClipIDs, secondPendingClipIDs)
	}
	if len(firstReusedClipIDs) != 0 || len(secondReusedClipIDs) != 0 {
		t.Fatalf("expected clip rows not to be reported as reused, got first=%v second=%v", firstReusedClipIDs, secondReusedClipIDs)
	}

	rows, err := db.QueryContext(ctx, `
SELECT id, source_asset_id, source_revision_id, hls_asset_id, ingest_status, wallclock_start_ms
FROM clips
WHERE project_id=?
ORDER BY id`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var seen int
	for rows.Next() {
		seen++
		var clipID, gotAssetID, gotRevID, gotHLSID, wallclockStartMS int64
		var status string
		if err := rows.Scan(&clipID, &gotAssetID, &gotRevID, &gotHLSID, &status, &wallclockStartMS); err != nil {
			t.Fatal(err)
		}
		if gotAssetID != assetID || gotRevID != revID || gotHLSID != hlsID {
			t.Fatalf("clip %d should reuse source/HLS asset ids (%d,%d,%d), got (%d,%d,%d)", clipID, assetID, revID, hlsID, gotAssetID, gotRevID, gotHLSID)
		}
		if status != "SUCCESS" {
			t.Fatalf("clip %d should be SUCCESS when HLS exists, got %q", clipID, status)
		}
		if wallclockStartMS != req.WallclockStartMS {
			t.Fatalf("clip %d wallclock_start_ms = %d, want %d", clipID, wallclockStartMS, req.WallclockStartMS)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != 2 {
		t.Fatalf("expected exactly two clip rows, got %d", seen)
	}
}

func TestCreateSourceRevisionClipsCreatesFreshPendingClipWithoutHLS(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t, ctx)

	projectID := insertTestProject(t, ctx, db)
	info := storage.ObjectInfo{
		Name:      "camera-b.mp4",
		SizeBytes: 84,
		ETag:      "etag-b",
		Ref: storage.ObjectRef{
			Adapter: "local",
			Path:    "camera-b.mp4",
		},
	}
	s := &Server{db: db, cfg: config.Config{TranscodeProfile: "test-profile"}}
	req := createClipReq{
		Perspective:      "Main",
		Track:            "Video 0",
		Kind:             "video",
		DisplayName:      "Camera B",
		StreamIndex:      0,
		WallclockStartMS: 0,
	}

	firstClipIDs, firstPendingClipIDs, firstReusedClipIDs, err := s.createSourceRevisionClips(ctx, projectID, []createClipReq{req}, info)
	if err != nil {
		t.Fatalf("first createSourceRevisionClips: %v", err)
	}
	secondClipIDs, secondPendingClipIDs, secondReusedClipIDs, err := s.createSourceRevisionClips(ctx, projectID, []createClipReq{req}, info)
	if err != nil {
		t.Fatalf("second createSourceRevisionClips: %v", err)
	}

	if len(firstClipIDs) != 1 || len(secondClipIDs) != 1 {
		t.Fatalf("expected one clip per import, got first=%v second=%v", firstClipIDs, secondClipIDs)
	}
	if firstClipIDs[0] == secondClipIDs[0] {
		t.Fatalf("expected duplicate pending source import to create a fresh timeline clip, got reused id %d", firstClipIDs[0])
	}
	if len(firstPendingClipIDs) != 1 || len(secondPendingClipIDs) != 1 {
		t.Fatalf("expected each import without HLS to return its new pending clip, got first=%v second=%v", firstPendingClipIDs, secondPendingClipIDs)
	}
	if firstPendingClipIDs[0] != firstClipIDs[0] || secondPendingClipIDs[0] != secondClipIDs[0] {
		t.Fatalf("pending clip ids should match new clip ids, got first clip=%v pending=%v second clip=%v pending=%v", firstClipIDs, firstPendingClipIDs, secondClipIDs, secondPendingClipIDs)
	}
	if len(firstReusedClipIDs) != 0 || len(secondReusedClipIDs) != 0 {
		t.Fatalf("expected clip rows not to be reported as reused, got first=%v second=%v", firstReusedClipIDs, secondReusedClipIDs)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM clips WHERE project_id=? AND ingest_status='PENDING'`, projectID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected two pending clips, got %d", count)
	}
}

func TestCreateSourceRevisionClipsLinksStreamsFromSameImport(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t, ctx)

	projectID := insertTestProject(t, ctx, db)
	info := storage.ObjectInfo{
		Name:      "camera-c.mp4",
		SizeBytes: 128,
		ETag:      "etag-c",
		Ref: storage.ObjectRef{
			Adapter: "local",
			Path:    "camera-c.mp4",
		},
	}
	s := &Server{db: db, cfg: config.Config{TranscodeProfile: "test-profile"}}
	reqs := []createClipReq{
		{Perspective: "Main", Track: "Video", Kind: "video", DisplayName: "Camera C video", StreamIndex: 0, WallclockStartMS: 5000},
		{Perspective: "Main", Track: "Audio", Kind: "audio", DisplayName: "Camera C audio", StreamIndex: 1, WallclockStartMS: 5000},
	}

	clipIDs, _, _, err := s.createSourceRevisionClips(ctx, projectID, reqs, info)
	if err != nil {
		t.Fatalf("createSourceRevisionClips: %v", err)
	}
	if len(clipIDs) != 2 {
		t.Fatalf("expected two linked clips, got %v", clipIDs)
	}

	rows, err := db.QueryContext(ctx, `SELECT link_group_id FROM clips WHERE project_id=? ORDER BY id`, projectID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var groups []string
	for rows.Next() {
		var group string
		if err := rows.Scan(&group); err != nil {
			t.Fatal(err)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0] == "" || groups[0] != groups[1] {
		t.Fatalf("expected both imported streams to share a non-empty link group, got %#v", groups)
	}
}

func openTestDB(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	db, err := dbpkg.Open(ctx, filepath.Join(t.TempDir(), "drifter-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertTestProject(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	if _, err := db.ExecContext(ctx, `INSERT INTO users(username, display_name, color, can_create_projects) VALUES ('owner', 'Owner', '#abcdef', 1)`); err != nil {
		t.Fatal(err)
	}
	res, err := db.ExecContext(ctx, `INSERT INTO projects(name, owner_username) VALUES ('Project', 'owner')`)
	if err != nil {
		t.Fatal(err)
	}
	projectID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO project_memberships(project_id, username, role) VALUES (?, 'owner', 'owner')`, projectID); err != nil {
		t.Fatal(err)
	}
	return projectID
}

func insertTestSourceRevision(t *testing.T, ctx context.Context, db *sql.DB, info storage.ObjectInfo, displayName string) (int64, int64) {
	t.Helper()
	res, err := db.ExecContext(ctx, `INSERT INTO source_assets(adapter, bucket, current_key, current_path, display_name) VALUES (?, ?, ?, ?, ?)`, info.Ref.Adapter, info.Ref.Bucket, info.Ref.Key, info.Ref.Path, displayName)
	if err != nil {
		t.Fatal(err)
	}
	assetID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	res, err = db.ExecContext(ctx, `INSERT INTO source_asset_revisions(source_asset_id, adapter, bucket, key, path, size_bytes, fingerprint_json) VALUES (?, ?, ?, ?, ?, ?, ?)`, assetID, info.Ref.Adapter, info.Ref.Bucket, info.Ref.Key, info.Ref.Path, info.SizeBytes, ingest.FingerprintJSON(info))
	if err != nil {
		t.Fatal(err)
	}
	revID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return assetID, revID
}

func insertTestHLSAsset(t *testing.T, ctx context.Context, db *sql.DB, revID int64, streamID, profile string) int64 {
	t.Helper()
	res, err := db.ExecContext(ctx, `INSERT INTO hls_assets(source_revision_id, adapter, playlist_path, stream_id, media_kind, transcode_profile_version, duration_ms, fps_num, fps_den) VALUES (?, 'local', 'rev/index.m3u8', ?, 'video', ?, 4321, 30, 1)`, revID, streamID, profile)
	if err != nil {
		t.Fatal(err)
	}
	hlsID, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return hlsID
}
