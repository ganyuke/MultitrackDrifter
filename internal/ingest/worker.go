package ingest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/example/multitrack-drifter/internal/config"
	ff "github.com/example/multitrack-drifter/internal/ffmpeg"
	"github.com/example/multitrack-drifter/internal/storage"
	"github.com/example/multitrack-drifter/internal/storage/localstore"
)

type Worker struct {
	db     *sql.DB
	cfg    config.Config
	source storage.SourceStore
	hls    storage.HLSStore
	runner ff.Runner
	jobs   chan int64
}

func NewWorker(db *sql.DB, cfg config.Config, source storage.SourceStore, hls storage.HLSStore) *Worker {
	return &Worker{db: db, cfg: cfg, source: source, hls: hls, runner: ff.Runner{FFmpeg: cfg.FFmpegBin, FFprobe: cfg.FFprobeBin}, jobs: make(chan int64, 64)}
}

func (w *Worker) Start(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case jobID := <-w.jobs:
				w.process(ctx, jobID)
			}
		}
	}()
}

func (w *Worker) Enqueue(jobID int64) {
	select {
	case w.jobs <- jobID:
	default:
		go w.process(context.Background(), jobID)
	}
}

func (w *Worker) EnqueueProject(ctx context.Context, projectID int64) ([]int64, error) {
	rows, err := w.db.QueryContext(ctx, `SELECT id FROM clips WHERE project_id = ? AND ingest_status IN ('PENDING','FAILED')`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var clipID int64
		if err := rows.Scan(&clipID); err != nil {
			return nil, err
		}
		var jobID int64
		res, err := w.db.ExecContext(ctx, `INSERT INTO ingest_jobs(project_id, clip_id, state) VALUES (?, ?, 'PENDING')`, projectID, clipID)
		if err != nil {
			return nil, err
		}
		jobID, _ = res.LastInsertId()
		ids = append(ids, jobID)
		w.Enqueue(jobID)
	}
	return ids, rows.Err()
}

type clipJob struct {
	JobID            int64
	ProjectID        int64
	ClipID           int64
	SourceRevisionID int64
	SourcePath       string
	MediaKind        string
	StreamIndex      int
	DisplayName      string
}

func (w *Worker) process(ctx context.Context, jobID int64) {
	if err := w.processErr(ctx, jobID); err != nil {
		_, _ = w.db.ExecContext(context.Background(), `UPDATE ingest_jobs SET state='FAILED', error=?, finished_at=datetime('now') WHERE id=?`, err.Error(), jobID)
	}
}

func (w *Worker) processErr(ctx context.Context, jobID int64) error {
	job, err := w.loadJob(ctx, jobID)
	if err != nil {
		return err
	}
	_, _ = w.db.ExecContext(ctx, `UPDATE ingest_jobs SET state='PROCESSING', started_at=datetime('now') WHERE id=?`, jobID)
	_, _ = w.db.ExecContext(ctx, `UPDATE clips SET ingest_status='PROCESSING', updated_at=datetime('now') WHERE id=?`, job.ClipID)

	if ok, err := w.attachExistingHLS(ctx, job); err != nil {
		return w.failClip(ctx, job, err)
	} else if ok {
		return nil
	}

	input, err := w.inputPath(ctx, job.SourcePath)
	if err != nil {
		return w.failClip(ctx, job, err)
	}
	probe, err := w.runner.Probe(ctx, input)
	if err != nil {
		return w.failClip(ctx, job, err)
	}
	streamIndex, kind, meta := ff.FirstPlayable(probe)
	if job.StreamIndex >= 0 {
		streamIndex = job.StreamIndex
		kind, meta = ff.StreamMeta(probe, streamIndex)
	}
	if job.MediaKind != "" {
		kind = job.MediaKind
		meta.Kind = kind
	}
	if meta.DurationMS == 0 {
		meta.DurationMS = 1000
	}

	assetPath := immutableAssetPath(job.SourceRevisionID, streamIndex, w.cfg.TranscodeProfile)
	if ok, _ := w.manifestExists(ctx, assetPath+"/index.m3u8"); !ok {
		tmp, err := os.MkdirTemp("", "drifter-hls-*")
		if err != nil {
			return w.failClip(ctx, job, err)
		}
		defer os.RemoveAll(tmp)
		if err := w.runner.TranscodeHLS(ctx, input, tmp, streamIndex, kind); err != nil {
			return w.failClip(ctx, job, err)
		}
		if err := filepath.WalkDir(tmp, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(tmp, path)
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			contentType := "video/MP2T"
			if strings.HasSuffix(path, ".m3u8") {
				contentType = "application/vnd.apple.mpegurl"
			}
			return w.hls.Put(ctx, storage.ObjectRef{Adapter: w.cfg.HLSAdapter, Path: filepath.ToSlash(filepath.Join(assetPath, rel))}, f, contentType)
		}); err != nil {
			return w.failClip(ctx, job, err)
		}
	}

	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return w.failClip(ctx, job, err)
	}
	var hlsID int64
	err = tx.QueryRowContext(ctx, `
INSERT INTO hls_assets(source_revision_id, adapter, playlist_path, stream_id, media_kind, transcode_profile_version, duration_ms, fps_num, fps_den)
VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?,0), NULLIF(?,0))
ON CONFLICT(source_revision_id, stream_id, transcode_profile_version) DO UPDATE SET duration_ms=excluded.duration_ms
RETURNING id`, job.SourceRevisionID, w.cfg.HLSAdapter, assetPath+"/index.m3u8", fmt.Sprintf("stream-%d", streamIndex), kind, w.cfg.TranscodeProfile, meta.DurationMS, meta.FPSNum, meta.FPSDen).Scan(&hlsID)
	if err != nil {
		_ = tx.Rollback()
		return w.failClip(ctx, job, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE clips SET hls_asset_id=?, duration_ms=?, fps_num=NULLIF(?,0), fps_den=NULLIF(?,0), stream_index=?, media_kind=?, ingest_status='SUCCESS', updated_at=datetime('now') WHERE id=?`, hlsID, meta.DurationMS, meta.FPSNum, meta.FPSDen, streamIndex, kind, job.ClipID); err != nil {
		_ = tx.Rollback()
		return w.failClip(ctx, job, err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ingest_jobs SET state='SUCCESS', error='', finished_at=datetime('now') WHERE id=?`, jobID); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (w *Worker) loadJob(ctx context.Context, jobID int64) (clipJob, error) {
	var j clipJob
	err := w.db.QueryRowContext(ctx, `
SELECT j.id, j.project_id, c.id, c.source_revision_id, r.path, c.media_kind, c.stream_index, c.display_name
FROM ingest_jobs j JOIN clips c ON c.id=j.clip_id JOIN source_asset_revisions r ON r.id=c.source_revision_id
WHERE j.id=?`, jobID).Scan(&j.JobID, &j.ProjectID, &j.ClipID, &j.SourceRevisionID, &j.SourcePath, &j.MediaKind, &j.StreamIndex, &j.DisplayName)
	return j, err
}

func (w *Worker) attachExistingHLS(ctx context.Context, job clipJob) (bool, error) {
	if job.StreamIndex < 0 {
		return false, nil
	}
	streamID := fmt.Sprintf("stream-%d", job.StreamIndex)
	var hlsID int64
	var durationMS, fpsNum, fpsDen int64
	var kind string
	err := w.db.QueryRowContext(ctx, `
SELECT id, duration_ms, COALESCE(fps_num,0), COALESCE(fps_den,0), media_kind
FROM hls_assets
WHERE source_revision_id=? AND stream_id=? AND transcode_profile_version=?`, job.SourceRevisionID, streamID, w.cfg.TranscodeProfile).Scan(&hlsID, &durationMS, &fpsNum, &fpsDen, &kind)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE clips SET hls_asset_id=?, duration_ms=?, fps_num=NULLIF(?,0), fps_den=NULLIF(?,0), media_kind=?, ingest_status='SUCCESS', updated_at=datetime('now') WHERE id=?`, hlsID, durationMS, fpsNum, fpsDen, kind, job.ClipID); err != nil {
		_ = tx.Rollback()
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ingest_jobs SET state='SUCCESS', error='', finished_at=datetime('now') WHERE id=?`, job.JobID); err != nil {
		_ = tx.Rollback()
		return false, err
	}
	return true, tx.Commit()
}

func (w *Worker) inputPath(ctx context.Context, sourcePath string) (string, error) {
	if local, ok := w.source.(*localstore.Source); ok {
		return local.ResolvePath(sourcePath)
	}
	// S3-compatible stores should return presigned HTTP(S) source URLs in a full implementation.
	rc, err := w.source.Open(ctx, storage.ObjectRef{Adapter: w.cfg.SourceAdapter, Path: sourcePath})
	if err != nil {
		return "", err
	}
	defer rc.Close()
	return "", fmt.Errorf("non-local source ingest is not wired in this POC; source open returned %T", rc)
}

func (w *Worker) manifestExists(ctx context.Context, rel string) (bool, error) {
	rc, err := w.hls.Open(ctx, storage.ObjectRef{Adapter: w.cfg.HLSAdapter, Path: rel})
	if err != nil {
		return false, nil
	}
	_, _ = io.Copy(io.Discard, rc)
	_ = rc.Close()
	return true, nil
}

func (w *Worker) failClip(ctx context.Context, job clipJob, err error) error {
	_, _ = w.db.ExecContext(ctx, `UPDATE clips SET ingest_status='FAILED', updated_at=datetime('now') WHERE id=?`, job.ClipID)
	_, _ = w.db.ExecContext(ctx, `UPDATE ingest_jobs SET state='FAILED', error=?, finished_at=datetime('now') WHERE id=?`, err.Error(), job.JobID)
	return err
}

func immutableAssetPath(revisionID int64, streamIndex int, profile string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:%d", revisionID, streamIndex, profile, time.Now().Year()/1000)))
	return fmt.Sprintf("rev-%d/stream-%d/%s/%s", revisionID, streamIndex, profile, hex.EncodeToString(h[:])[:12])
}

func FingerprintJSON(info storage.ObjectInfo) string {
	m := map[string]any{"adapter": info.Ref.Adapter, "bucket": info.Ref.Bucket, "key": info.Ref.Key, "path": info.Ref.Path, "size": info.SizeBytes, "modifiedUnix": info.ModifiedUnix, "etag": info.ETag, "device": info.Device, "inode": info.Inode}
	b, _ := json.Marshal(m)
	return string(b)
}
