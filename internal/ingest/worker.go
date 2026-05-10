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
	idlePollInterval        = 5 * time.Second
	progressMinInterval     = 500 * time.Millisecond
	progressPersistInterval = 2 * time.Second
	progressLogInterval     = 15 * time.Second
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
	if err := w.reconcileClipStatuses(ctx); err != nil {
		slog.ErrorContext(ctx, "ingest: failed to reconcile clip statuses", "err", err)
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
    stage='requeued',
    progress_pct=0,
    progress_time_ms=0,
    ffmpeg_frame=0,
    ffmpeg_fps=0,
    ffmpeg_bitrate='',
    ffmpeg_speed='',
    error='server restarted while processing; requeued',
    started_at=NULL,
	    finished_at=NULL,
    updated_at=datetime('now')
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

func (w *Worker) reconcileClipStatuses(ctx context.Context) error {
	// A clip status is what the playback manifest and timeline UI render. Keep it
	// in sync with the latest job so a crash or older bug cannot strand a clip as
	// PROCESSING forever after the worker is no longer doing work for it.
	res, err := w.db.ExecContext(ctx, `
UPDATE clips
SET ingest_status = COALESCE((
      SELECT CASE j.state
        WHEN 'SUCCESS' THEN 'SUCCESS'
        WHEN 'FAILED' THEN 'FAILED'
        WHEN 'PENDING' THEN 'PENDING'
        WHEN 'PROCESSING' THEN 'PROCESSING'
      END
      FROM ingest_jobs j
      WHERE j.clip_id=clips.id
      ORDER BY j.id DESC
      LIMIT 1
    ), 'PENDING'),
    updated_at=datetime('now')
WHERE ingest_status='PROCESSING'
  AND NOT EXISTS (
    SELECT 1 FROM ingest_jobs j
    WHERE j.clip_id=clips.id AND j.state IN ('PENDING','PROCESSING')
  )`)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		slog.WarnContext(ctx, "ingest: repaired clips stranded in processing", "count", n)
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
    stage='starting',
    progress_pct=0,
    progress_time_ms=0,
    total_duration_ms=0,
    ffmpeg_frame=0,
    ffmpeg_fps=0,
    ffmpeg_bitrate='',
    ffmpeg_speed='',
    started_at=datetime('now'),
    finished_at=NULL,
	    error='',
    updated_at=datetime('now')
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
			w.markInterrupted(context.Background(), claimed)
			return
		}
		slog.ErrorContext(context.Background(), "ingest: job failed", "job_id", claimed.JobID, "project_id", claimed.ProjectID, "clip_id", claimed.ClipID, "duration_ms", time.Since(start).Milliseconds(), "err", err)
		w.failClaimed(context.Background(), claimed, err)
		return
	}
	slog.InfoContext(ctx, "ingest: job finished", "job_id", claimed.JobID, "project_id", claimed.ProjectID, "clip_id", claimed.ClipID, "duration_ms", time.Since(start).Milliseconds())
}

func (w *Worker) markInterrupted(ctx context.Context, job claimedJob) {
	_, _ = w.db.ExecContext(ctx, `UPDATE clips SET ingest_status='PENDING', updated_at=datetime('now') WHERE id=? AND ingest_status='PROCESSING'`, job.ClipID)
	_, _ = w.db.ExecContext(ctx, `
UPDATE ingest_jobs
SET state='PENDING', stage='interrupted', error='worker interrupted while processing; requeued', started_at=NULL, finished_at=NULL, updated_at=datetime('now')
WHERE id=? AND state='PROCESSING'`, job.JobID)
	w.broadcastIngest(job.ProjectID, job.ClipID, job.JobID, "PENDING", "worker interrupted while processing; requeued")
}

func (w *Worker) processErr(ctx context.Context, claimed claimedJob) error {
	w.setJobStage(ctx, claimed, "loading", 0, 0)
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

	w.setJobStage(ctx, claimed, "resolving source", 0, 0)
	input, cleanupInput, err := w.inputPath(ctx, job.SourcePath)
	if err != nil {
		return err
	}
	defer cleanupInput()
	w.setJobStage(ctx, claimed, "probing", 0, 0)
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
	w.setJobStage(ctx, claimed, "transcoding", 0, meta.DurationMS)
	progress := w.progressReporter(job.ProjectID, job.ClipID, job.JobID, meta.DurationMS)
	if err := w.runner.TranscodeHLS(ctx, input, tmp, streamIndex, kind, ff.TranscodeOptions{Preset: w.cfg.TranscodePreset, SegmentSeconds: w.cfg.HLSSegmentSeconds, Progress: progress}); err != nil {
		return err
	}
	w.setJobStage(ctx, claimed, "uploading HLS", 0.98, meta.DurationMS)
	if err := w.uploadHLSDirectory(ctx, tmp, assetPath); err != nil {
		return err
	}
	w.setJobStage(ctx, claimed, "finalizing", 0.99, meta.DurationMS)

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
	if _, err := tx.ExecContext(ctx, `UPDATE ingest_jobs SET state='SUCCESS', stage='done', progress_pct=1, progress_time_ms=?, total_duration_ms=?, error='', finished_at=datetime('now'), updated_at=datetime('now') WHERE id=?`, meta.DurationMS, meta.DurationMS, job.JobID); err != nil {
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
	if _, err := tx.ExecContext(ctx, `UPDATE ingest_jobs SET state='SUCCESS', stage='reused existing HLS', progress_pct=1, progress_time_ms=?, total_duration_ms=?, error='', finished_at=datetime('now'), updated_at=datetime('now') WHERE id=?`, durationMS, durationMS, job.JobID); err != nil {
		_ = tx.Rollback()
		return false, err
	}
	return true, tx.Commit()
}

func (w *Worker) inputPath(ctx context.Context, sourcePath string) (string, func(), error) {
	if local, ok := w.source.(*localstore.Source); ok {
		path, err := local.ResolvePath(sourcePath)
		return path, func() {}, err
	}
	rc, err := w.source.Open(ctx, storage.ObjectRef{Adapter: w.cfg.SourceAdapter, Path: sourcePath})
	if err != nil {
		return "", func() {}, err
	}
	defer rc.Close()
	tmp, err := os.MkdirTemp("", "drifter-source-*")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	tmpFile := tmp + "/source"
	f, err := os.Create(tmpFile)
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	_, copyErr := io.Copy(f, rc)
	closeErr := f.Close()
	if closeErr != nil {
		cleanup()
		return "", func() {}, closeErr
	}
	if copyErr != nil {
		cleanup()
		return "", func() {}, copyErr
	}
	return tmpFile, cleanup, nil
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

func (w *Worker) setJobStage(ctx context.Context, job claimedJob, stage string, pct float64, totalDurationMS int64) {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	_, _ = w.db.ExecContext(ctx, `
UPDATE ingest_jobs
SET stage=?, progress_pct=MAX(progress_pct, ?), total_duration_ms=CASE WHEN ? > 0 THEN ? ELSE total_duration_ms END, updated_at=datetime('now')
WHERE id=?`, stage, pct, totalDurationMS, totalDurationMS, job.JobID)
	w.broadcastIngestProgress(job.ProjectID, job.ClipID, job.JobID, pct, 0, totalDurationMS, ff.Progress{}, stage)
}

func (w *Worker) progressReporter(projectID, clipID, jobID, totalDurationMS int64) func(ff.Progress) {
	if projectID == 0 || totalDurationMS <= 0 {
		return nil
	}
	var mu sync.Mutex
	var lastAt time.Time
	var lastPersistAt time.Time
	var lastLogAt time.Time
	var lastPct int64 = -1
	var lastPersistPct int64 = -1
	return func(p ff.Progress) {
		if p.TimeMS < 0 {
			return
		}
		pct := p.TimeMS * 100 / totalDurationMS
		if pct > 100 {
			pct = 100
		}
		now := time.Now()
		shouldBroadcast := false
		shouldPersist := false
		shouldLog := false
		mu.Lock()
		if pct != lastPct && (pct == 100 || now.Sub(lastAt) >= progressMinInterval) {
			lastPct = pct
			lastAt = now
			shouldBroadcast = true
		}
		if pct != lastPersistPct || now.Sub(lastPersistAt) >= progressPersistInterval || pct == 100 {
			lastPersistPct = pct
			lastPersistAt = now
			shouldPersist = true
		}
		if now.Sub(lastLogAt) >= progressLogInterval || pct == 100 {
			lastLogAt = now
			shouldLog = true
		}
		mu.Unlock()
		pctFloat := float64(pct) / 100
		if shouldPersist {
			w.persistFFmpegProgress(context.Background(), jobID, pctFloat, totalDurationMS, p)
		}
		if shouldLog {
			slog.InfoContext(context.Background(), "ffmpeg: ingest progress", "job_id", jobID, "clip_id", clipID, "pct", pctFloat, "time_ms", p.TimeMS, "total_ms", totalDurationMS, "frame", p.Frame, "fps", p.FPS, "bitrate", p.Bitrate, "speed", p.Speed)
		}
		if shouldBroadcast {
			w.broadcastIngestProgress(projectID, clipID, jobID, pctFloat, p.TimeMS, totalDurationMS, p, "transcoding")
		}
	}
}

func (w *Worker) persistFFmpegProgress(ctx context.Context, jobID int64, pct float64, totalDurationMS int64, p ff.Progress) {
	if pct < 0 {
		pct = 0
	}
	if pct > 1 {
		pct = 1
	}
	_, _ = w.db.ExecContext(ctx, `
UPDATE ingest_jobs
SET stage='transcoding', progress_pct=?, progress_time_ms=?, total_duration_ms=?, ffmpeg_frame=?, ffmpeg_fps=?, ffmpeg_bitrate=?, ffmpeg_speed=?, updated_at=datetime('now')
WHERE id=? AND state='PROCESSING'`, pct, p.TimeMS, totalDurationMS, p.Frame, p.FPS, p.Bitrate, p.Speed, jobID)
}

func (w *Worker) broadcastIngestProgress(projectID, clipID, jobID int64, pct float64, timeMS, totalDurationMS int64, p ff.Progress, stage string) {
	if w.hub == nil || projectID == 0 {
		return
	}
	w.hub.Broadcast(projectID, realtime.Event{
		Type:      "clip.ingest.progress",
		ProjectID: projectID,
		User:      "system",
		Payload: map[string]any{
			"clipId":          clipID,
			"jobId":           jobID,
			"pct":             pct,
			"timeMs":          timeMS,
			"totalDurationMs": totalDurationMS,
			"frame":           p.Frame,
			"fps":             p.FPS,
			"bitrate":         p.Bitrate,
			"speed":           p.Speed,
			"stage":           stage,
		},
	})
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
	_, _ = w.db.ExecContext(ctx, `UPDATE ingest_jobs SET state='FAILED', stage='failed', error=?, last_log=?, finished_at=datetime('now'), updated_at=datetime('now') WHERE id=?`, msg, truncateForDB(msg, 4000), job.JobID)
	w.broadcastIngest(job.ProjectID, job.ClipID, job.JobID, "FAILED", msg)
}

func truncateForDB(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen]
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

// fingerprint is a fixed-field struct so json.Marshal produces a stable,
// deterministic byte sequence regardless of Go map iteration order.
type fingerprint struct {
	Adapter      string `json:"adapter"`
	Bucket       string `json:"bucket,omitempty"`
	Key          string `json:"key,omitempty"`
	Path         string `json:"path,omitempty"`
	Size         int64  `json:"size"`
	ModifiedUnix int64  `json:"modifiedUnix,omitempty"`
	ETag         string `json:"etag,omitempty"`
	Device       uint64 `json:"device,omitempty"`
	Inode        uint64 `json:"inode,omitempty"`
}

// FingerprintJSON returns a stable JSON fingerprint for a source object.
// Field order is deterministic because it uses a struct, not a map.
func FingerprintJSON(info storage.ObjectInfo) string {
	fp := fingerprint{
		Adapter:      info.Ref.Adapter,
		Bucket:       info.Ref.Bucket,
		Key:          info.Ref.Key,
		Path:         info.Ref.Path,
		Size:         info.SizeBytes,
		ModifiedUnix: info.ModifiedUnix,
		ETag:         info.ETag,
		Device:       info.Device,
		Inode:        info.Inode,
	}
	b, _ := json.Marshal(fp)
	return string(b)
}

// ETagHash returns a hex-encoded SHA-256 of the ETag when available.
// For Garage/S3 single-part uploads the ETag is the MD5 of the content,
// making it a reliable content-identity key without a full download.
// Returns empty string when ETag is absent.
func ETagHash(info storage.ObjectInfo) string {
	etag := strings.Trim(strings.TrimSpace(info.ETag), `"`)
	if etag == "" || strings.Contains(etag, "-") {
		// Empty or multipart ETag (md5-of-md5s, e.g. "abc123-5") — not a
		// reliable content identity, skip.
		return ""
	}
	h := sha256.Sum256([]byte(etag))
	return hex.EncodeToString(h[:])
}
