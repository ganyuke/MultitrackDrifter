package ingest

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/example/multitrack-drifter/internal/config"
	ff "github.com/example/multitrack-drifter/internal/ffmpeg"
	"github.com/example/multitrack-drifter/internal/realtime"
	"github.com/example/multitrack-drifter/internal/storage"
	"github.com/example/multitrack-drifter/internal/storage/localstore"
)

const (
	idlePollInterval    = 5 * time.Second
	progressMinInterval = 500 * time.Millisecond
)

type Worker struct {
	db     *sql.DB
	cfg    config.Config
	source storage.SourceStore
	hls    storage.HLSStore
	runner ff.Runner
	notify chan struct{}
	hub    *realtime.Hub

	queueLogMu sync.Mutex
	queueLogAt time.Time
}

func NewWorker(db *sql.DB, cfg config.Config, source storage.SourceStore, hls storage.HLSStore, hub *realtime.Hub) *Worker {
	workerCount := max(1, cfg.IngestWorkers)
	return &Worker{
		db:     db,
		cfg:    cfg,
		source: source,
		hls:    hls,
		runner: ff.Runner{FFmpeg: cfg.FFmpegBin, FFprobe: cfg.FFprobeBin},
		notify: make(chan struct{}, workerCount),
		hub:    hub,
	}
}

func (w *Worker) Start(ctx context.Context) {
	if err := w.requeueInterruptedJobs(ctx); err != nil {
		slog.ErrorContext(ctx, "ingest: failed to requeue interrupted jobs", "err", err)
	}

	workerCount := max(1, w.cfg.IngestWorkers)
	for range workerCount {
		go w.run(ctx)
	}
	w.Notify()
}

func (w *Worker) run(ctx context.Context) {
	for {
		job, ok, err := w.claimNextJob(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.ErrorContext(ctx, "ingest: failed to claim job", "err", err)
			if ok {
				continue
			}
			if !w.waitForWork(ctx) {
				return
			}
			continue
		}
		if !ok {
			if !w.waitForWork(ctx) {
				return
			}
			continue
		}
		w.process(ctx, job)
	}
}

func (w *Worker) waitForWork(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	case <-w.notify:
		return true
	case <-time.After(idlePollInterval):
		return true
	}
}

func (w *Worker) Notify() {
	workerCount := max(1, w.cfg.IngestWorkers)
	for range workerCount {
		select {
		case w.notify <- struct{}{}:
		default:
			go w.logQueueDepth(context.Background())
			return
		}
	}
	go w.logQueueDepth(context.Background())
}

func (w *Worker) EnqueueProject(ctx context.Context, projectID int64) ([]int64, error) {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
INSERT OR IGNORE INTO ingest_jobs(project_id, clip_id, state)
SELECT c.project_id, c.id, 'PENDING'
FROM clips c
WHERE c.project_id=?
  AND c.ingest_status IN ('PENDING','FAILED')
ORDER BY c.id
RETURNING id, clip_id`, projectID)
	if err != nil {
		return nil, err
	}
	var jobIDs []int64
	var clipIDs []int64
	for rows.Next() {
		var jobID, clipID int64
		if err := rows.Scan(&jobID, &clipID); err != nil {
			rows.Close()
			return nil, err
		}
		jobIDs = append(jobIDs, jobID)
		clipIDs = append(clipIDs, clipID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, clipID := range clipIDs {
		if _, err := tx.ExecContext(ctx, `UPDATE clips SET ingest_status='PENDING', updated_at=datetime('now') WHERE id=? AND ingest_status='FAILED'`, clipID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	if len(jobIDs) > 0 {
		w.Notify()
	}
	return jobIDs, nil
}

type claimedJob struct {
	JobID     int64
	ProjectID int64
	ClipID    int64
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

func (w *Worker) requeueInterruptedJobs(ctx context.Context) error {
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE clips
SET ingest_status='PENDING', updated_at=datetime('now')
WHERE ingest_status='PROCESSING'
  AND id IN (SELECT clip_id FROM ingest_jobs WHERE state='PROCESSING')`); err != nil {
		_ = tx.Rollback()
		return err
	}
	res, err := tx.ExecContext(ctx, `
UPDATE ingest_jobs
SET state='PENDING',
    error='server restarted while processing; requeued',
    started_at=NULL,
    finished_at=NULL
WHERE state='PROCESSING'`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		slog.InfoContext(ctx, "ingest: requeued interrupted jobs", "count", n)
	}
	return nil
}

func (w *Worker) claimNextJob(ctx context.Context) (claimedJob, bool, error) {
	var pending int
	if err := w.db.QueryRowContext(ctx, `SELECT 1 FROM ingest_jobs WHERE state='PENDING' LIMIT 1`).Scan(&pending); err == sql.ErrNoRows {
		return claimedJob{}, false, nil
	} else if err != nil {
		return claimedJob{}, false, err
	}

	var job claimedJob
	err := w.db.QueryRowContext(ctx, `
UPDATE ingest_jobs
SET state='PROCESSING',
    started_at=datetime('now'),
    finished_at=NULL,
    error=''
WHERE id = (
  SELECT id
  FROM ingest_jobs
  WHERE state='PENDING'
  ORDER BY id
  LIMIT 1
)
RETURNING id, project_id, clip_id`).Scan(&job.JobID, &job.ProjectID, &job.ClipID)
	if err == sql.ErrNoRows {
		return claimedJob{}, false, nil
	}
	if err != nil {
		return claimedJob{}, false, err
	}
	if _, err := w.db.ExecContext(ctx, `UPDATE clips SET ingest_status='PROCESSING', updated_at=datetime('now') WHERE id=?`, job.ClipID); err != nil {
		w.failClaimed(context.Background(), job, err)
		return claimedJob{}, true, err
	}
	w.broadcastIngest(job.ProjectID, job.ClipID, job.JobID, "PROCESSING", "")
	return job, true, nil
}

func (w *Worker) process(ctx context.Context, claimed claimedJob) {
	start := time.Now()
	slog.InfoContext(ctx, "ingest: job started", "job_id", claimed.JobID, "project_id", claimed.ProjectID, "clip_id", claimed.ClipID)
	if err := w.processErr(ctx, claimed); err != nil {
		if ctx.Err() != nil {
			slog.InfoContext(context.Background(), "ingest: job interrupted", "job_id", claimed.JobID, "project_id", claimed.ProjectID, "clip_id", claimed.ClipID, "duration_ms", time.Since(start).Milliseconds())
			return
		}
		slog.ErrorContext(context.Background(), "ingest: job failed", "job_id", claimed.JobID, "project_id", claimed.ProjectID, "clip_id", claimed.ClipID, "duration_ms", time.Since(start).Milliseconds(), "err", err)
		w.failClaimed(context.Background(), claimed, err)
		return
	}
	slog.InfoContext(ctx, "ingest: job finished", "job_id", claimed.JobID, "project_id", claimed.ProjectID, "clip_id", claimed.ClipID, "duration_ms", time.Since(start).Milliseconds())
}

func (w *Worker) processErr(ctx context.Context, claimed claimedJob) error {
	job, err := w.loadJob(ctx, claimed.JobID)
	if err != nil {
		return err
	}

	if ok, err := w.attachExistingHLS(ctx, job); err != nil {
		return err
	} else if ok {
		w.broadcastIngest(job.ProjectID, job.ClipID, job.JobID, "SUCCESS", "")
		return nil
	}

	input, err := w.inputPath(ctx, job.SourcePath)
	if err != nil {
		return err
	}
	probe, err := w.runner.Probe(ctx, input)
	if err != nil {
		return err
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
	tmp, err := os.MkdirTemp("", "drifter-hls-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	progress := w.progressReporter(job.ProjectID, job.ClipID, job.JobID, meta.DurationMS)
	if err := w.runner.TranscodeHLS(ctx, input, tmp, streamIndex, kind, ff.TranscodeOptions{Preset: w.cfg.TranscodePreset, SegmentSeconds: w.cfg.HLSSegmentSeconds, Progress: progress}); err != nil {
		return err
	}
	if err := w.uploadHLSDirectory(ctx, tmp, assetPath); err != nil {
		return err
	}

	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	var hlsID int64
	err = tx.QueryRowContext(ctx, `
INSERT INTO hls_assets(source_revision_id, adapter, playlist_path, stream_id, media_kind, transcode_profile_version, duration_ms, fps_num, fps_den)
VALUES (?, ?, ?, ?, ?, ?, ?, NULLIF(?,0), NULLIF(?,0))
ON CONFLICT(source_revision_id, stream_id, transcode_profile_version) DO UPDATE SET duration_ms=excluded.duration_ms
RETURNING id`, job.SourceRevisionID, w.cfg.HLSAdapter, assetPath+"/index.m3u8", fmt.Sprintf("stream-%d", streamIndex), kind, w.cfg.TranscodeProfile, meta.DurationMS, meta.FPSNum, meta.FPSDen).Scan(&hlsID)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE clips SET hls_asset_id=?, duration_ms=?, fps_num=NULLIF(?,0), fps_den=NULLIF(?,0), stream_index=?, media_kind=?, ingest_status='SUCCESS', updated_at=datetime('now') WHERE id=?`, hlsID, meta.DurationMS, meta.FPSNum, meta.FPSDen, streamIndex, kind, job.ClipID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ingest_jobs SET state='SUCCESS', error='', finished_at=datetime('now') WHERE id=?`, job.JobID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	w.broadcastIngest(job.ProjectID, job.ClipID, job.JobID, "SUCCESS", "")
	return nil
}

func (w *Worker) loadJob(ctx context.Context, jobID int64) (clipJob, error) {
	var j clipJob
	err := w.db.QueryRowContext(ctx, `
SELECT j.id, j.project_id, c.id, c.source_revision_id, r.path, c.media_kind, c.stream_index, c.display_name
FROM ingest_jobs j JOIN clips c ON c.id=j.clip_id JOIN source_asset_revisions r ON r.id=c.source_revision_id
WHERE j.id=? AND j.state='PROCESSING'`, jobID).Scan(&j.JobID, &j.ProjectID, &j.ClipID, &j.SourceRevisionID, &j.SourcePath, &j.MediaKind, &j.StreamIndex, &j.DisplayName)
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
	rc, err := w.source.Open(ctx, storage.ObjectRef{Adapter: w.cfg.SourceAdapter, Path: sourcePath})
	if err != nil {
		return "", err
	}
	defer rc.Close()
	tmp, err := os.MkdirTemp("", "drifter-source-*")
	if err != nil {
		return "", err
	}
	tmpFile := tmp + "/source"
	f, err := os.Create(tmpFile)
	if err != nil {
		os.RemoveAll(tmp)
		return "", err
	}
	_, copyErr := io.Copy(f, rc)
	closeErr := f.Close()
	if closeErr != nil {
		os.RemoveAll(tmp)
		return "", closeErr
	}
	if copyErr != nil {
		os.RemoveAll(tmp)
		return "", copyErr
	}
	return tmpFile, nil
}

func (w *Worker) uploadHLSDirectory(ctx context.Context, tmp, assetPath string) error {
	var files []string
	if err := filepath.WalkDir(tmp, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	}); err != nil {
		return err
	}

	workers := max(1, w.cfg.HLSUploadWorkers)
	jobs := make(chan string)
	errCh := make(chan error, 1)
	uploadCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for path := range jobs {
				if err := w.uploadHLSFile(uploadCtx, tmp, assetPath, path); err != nil {
					select {
					case errCh <- err:
						cancel()
					default:
					}
				}
			}
		}()
	}

	for _, path := range files {
		select {
		case <-uploadCtx.Done():
			break
		case jobs <- path:
		}
		if uploadCtx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()

	select {
	case err := <-errCh:
		return err
	default:
	}
	return ctx.Err()
}

func (w *Worker) uploadHLSFile(ctx context.Context, tmp, assetPath, path string) error {
	rel, err := filepath.Rel(tmp, path)
	if err != nil {
		return err
	}
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
}

func (w *Worker) progressReporter(projectID, clipID, jobID, totalDurationMS int64) func(ff.Progress) {
	if w.hub == nil || projectID == 0 || totalDurationMS <= 0 {
		return nil
	}
	var mu sync.Mutex
	var lastAt time.Time
	var lastPct int64 = -1
	return func(p ff.Progress) {
		if p.TimeMS < 0 {
			return
		}
		pct := p.TimeMS * 100 / totalDurationMS
		if pct > 100 {
			pct = 100
		}
		now := time.Now()
		mu.Lock()
		if pct == lastPct || (pct < 100 && now.Sub(lastAt) < progressMinInterval) {
			mu.Unlock()
			return
		}
		lastPct = pct
		lastAt = now
		mu.Unlock()
		w.hub.Broadcast(projectID, realtime.Event{
			Type:      "clip.ingest.progress",
			ProjectID: projectID,
			User:      "system",
			Payload: map[string]any{
				"clipId": clipID,
				"jobId":  jobID,
				"pct":    float64(pct) / 100,
				"timeMs": p.TimeMS,
			},
		})
	}
}

func (w *Worker) logQueueDepth(ctx context.Context) {
	w.queueLogMu.Lock()
	if time.Since(w.queueLogAt) < 30*time.Second {
		w.queueLogMu.Unlock()
		return
	}
	w.queueLogAt = time.Now()
	w.queueLogMu.Unlock()

	var pending int
	if err := w.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ingest_jobs WHERE state='PENDING'`).Scan(&pending); err != nil {
		slog.DebugContext(ctx, "ingest: queue depth unavailable", "err", err)
		return
	}
	if pending > 0 {
		slog.InfoContext(ctx, "ingest: queue depth", "pending_jobs", pending)
	}
}

func (w *Worker) failClaimed(ctx context.Context, job claimedJob, err error) {
	msg := err.Error()
	_, _ = w.db.ExecContext(ctx, `UPDATE clips SET ingest_status='FAILED', updated_at=datetime('now') WHERE id=?`, job.ClipID)
	_, _ = w.db.ExecContext(ctx, `UPDATE ingest_jobs SET state='FAILED', error=?, finished_at=datetime('now') WHERE id=?`, msg, job.JobID)
	w.broadcastIngest(job.ProjectID, job.ClipID, job.JobID, "FAILED", msg)
}

func (w *Worker) broadcastIngest(projectID, clipID, jobID int64, state, errMsg string) {
	if w.hub == nil || projectID == 0 {
		return
	}
	w.hub.Broadcast(projectID, realtime.Event{
		Type:      "clip.ingest.updated",
		ProjectID: projectID,
		User:      "system",
		Payload: map[string]any{
			"clipId": clipID,
			"jobId":  jobID,
			"state":  state,
			"error":  errMsg,
		},
	})
}

func immutableAssetPath(revisionID int64, streamIndex int, profile string) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s", revisionID, streamIndex, profile)))
	return fmt.Sprintf("rev-%d/stream-%d/%s/%s", revisionID, streamIndex, profile, hex.EncodeToString(h[:])[:12])
}

func FingerprintJSON(info storage.ObjectInfo) string {
	m := map[string]any{"adapter": info.Ref.Adapter, "bucket": info.Ref.Bucket, "key": info.Ref.Key, "path": info.Ref.Path, "size": info.SizeBytes, "modifiedUnix": info.ModifiedUnix, "etag": info.ETag, "device": info.Device, "inode": info.Inode}
	b, _ := json.Marshal(m)
	return string(b)
}
