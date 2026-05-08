package httpapi

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/example/multitrack-drifter/internal/auth"
	"github.com/example/multitrack-drifter/internal/config"
	exp "github.com/example/multitrack-drifter/internal/export"
	ff "github.com/example/multitrack-drifter/internal/ffmpeg"
	"github.com/example/multitrack-drifter/internal/ingest"
	"github.com/example/multitrack-drifter/internal/realtime"
	"github.com/example/multitrack-drifter/internal/storage"
	"github.com/example/multitrack-drifter/internal/storage/localstore"
)

type Server struct {
	db     *sql.DB
	cfg    config.Config
	auth   *auth.Service
	source storage.SourceStore
	hls    storage.HLSStore
	ingest *ingest.Worker
	hub    *realtime.Hub
	static http.Handler
}

func New(db *sql.DB, cfg config.Config, authSvc *auth.Service, source storage.SourceStore, hls storage.HLSStore, worker *ingest.Worker, hub *realtime.Hub, static http.Handler) *Server {
	return &Server{db: db, cfg: cfg, auth: authSvc, source: source, hls: hls, ingest: worker, hub: hub, static: static}
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.login)
	mux.Handle("POST /api/logout", s.auth.Middleware(http.HandlerFunc(s.logout)))
	mux.Handle("GET /api/me", s.auth.Middleware(http.HandlerFunc(s.me)))
	mux.Handle("POST /api/me/color", s.auth.Middleware(http.HandlerFunc(s.setColor)))
	mux.Handle("GET /api/projects", s.auth.Middleware(http.HandlerFunc(s.listProjects)))
	mux.Handle("POST /api/projects", s.auth.Middleware(http.HandlerFunc(s.createProject)))
	mux.Handle("GET /api/projects/{projectID}", s.auth.Middleware(http.HandlerFunc(s.getProject)))
	mux.Handle("PATCH /api/projects/{projectID}", s.auth.Middleware(http.HandlerFunc(s.patchProject)))
	mux.Handle("GET /api/projects/{projectID}/sources", s.auth.Middleware(http.HandlerFunc(s.listSources)))
	mux.Handle("GET /api/projects/{projectID}/sources/probe", s.auth.Middleware(http.HandlerFunc(s.probeSource)))
	mux.Handle("POST /api/projects/{projectID}/assets", s.auth.Middleware(http.HandlerFunc(s.createAssetClip)))
	mux.Handle("POST /api/projects/{projectID}/ingest", s.auth.Middleware(http.HandlerFunc(s.triggerIngest)))
	mux.Handle("GET /api/projects/{projectID}/ingest-jobs", s.auth.Middleware(http.HandlerFunc(s.listIngestJobs)))
	mux.Handle("POST /api/projects/{projectID}/perspectives", s.auth.Middleware(http.HandlerFunc(s.createPerspective)))
	mux.Handle("PATCH /api/projects/{projectID}/perspectives/{perspectiveID}", s.auth.Middleware(http.HandlerFunc(s.patchPerspective)))
	mux.Handle("POST /api/projects/{projectID}/tracks", s.auth.Middleware(http.HandlerFunc(s.createTrack)))
	mux.Handle("PATCH /api/projects/{projectID}/clips/{clipID}", s.auth.Middleware(http.HandlerFunc(s.patchClip)))
	mux.Handle("DELETE /api/projects/{projectID}/clips/{clipID}", s.auth.Middleware(http.HandlerFunc(s.deleteClip)))
	mux.Handle("GET /api/projects/{projectID}/playback-manifest", s.auth.Middleware(http.HandlerFunc(s.playbackManifest)))
	mux.Handle("GET /api/projects/{projectID}/markers", s.auth.Middleware(http.HandlerFunc(s.listMarkers)))
	mux.Handle("POST /api/projects/{projectID}/markers", s.auth.Middleware(http.HandlerFunc(s.createMarker)))
	mux.Handle("PATCH /api/projects/{projectID}/markers/{markerID}", s.auth.Middleware(http.HandlerFunc(s.patchMarker)))
	mux.Handle("DELETE /api/projects/{projectID}/markers/{markerID}", s.auth.Middleware(http.HandlerFunc(s.deleteMarker)))
	mux.Handle("GET /api/projects/{projectID}/regions", s.auth.Middleware(http.HandlerFunc(s.listRegions)))
	mux.Handle("POST /api/projects/{projectID}/regions", s.auth.Middleware(http.HandlerFunc(s.createRegion)))
	mux.Handle("PATCH /api/projects/{projectID}/regions/{regionID}", s.auth.Middleware(http.HandlerFunc(s.patchRegion)))
	mux.Handle("DELETE /api/projects/{projectID}/regions/{regionID}", s.auth.Middleware(http.HandlerFunc(s.deleteRegion)))
	mux.Handle("GET /api/projects/{projectID}/export.csv", s.auth.Middleware(http.HandlerFunc(s.exportCSV)))
	mux.Handle("GET /api/projects/{projectID}/export.md", s.auth.Middleware(http.HandlerFunc(s.exportMarkdown)))
	mux.Handle("GET /api/projects/{projectID}/export.json", s.auth.Middleware(http.HandlerFunc(s.exportJSON)))
	mux.Handle("GET /api/projects/{projectID}/export.edl", s.auth.Middleware(http.HandlerFunc(s.exportEDL)))
	mux.Handle("GET /ws/projects/{projectID}", s.auth.Middleware(http.HandlerFunc(s.wsProject)))
	if s.cfg.HLSLocalURLPrefix != "" {
		mux.Handle("GET "+s.cfg.HLSLocalURLPrefix+"/{path...}", s.auth.Middleware(http.HandlerFunc(s.localHLS)))
	}
	mux.Handle("/", s.static)
	return logging(mux)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req struct{ Username, Password string }
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	p, token, err := s.auth.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		writeError(w, 401, err)
		return
	}
	s.auth.WriteSessionCookie(w, token)
	writeJSON(w, 200, p)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.CookieName); err == nil {
		_ = s.auth.Logout(r.Context(), c.Value)
	}
	s.auth.ClearSessionCookie(w)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.FromContext(r.Context())
	// Keep long review sessions alive, but avoid rewriting the session row on every /api/me poll.
	if c, err := r.Cookie(auth.CookieName); err == nil {
		if refreshed, err := s.auth.RefreshIfNeeded(r.Context(), c.Value, p); err == nil {
			p = refreshed
		}
	}
	writeJSON(w, 200, p)
}

func (s *Server) setColor(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.FromContext(r.Context())
	var req struct {
		Color string `json:"color"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if err := s.auth.SetColor(r.Context(), p.Username, req.Color); err != nil {
		writeError(w, 400, err)
		return
	}
	p.Color = req.Color
	writeJSON(w, 200, p)
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id, name, description, owner_username, created_at, updated_at FROM projects ORDER BY updated_at DESC`)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int64
		var name, desc, owner, created, updated string
		if err := rows.Scan(&id, &name, &desc, &owner, &created, &updated); err != nil {
			writeError(w, 500, err)
			return
		}
		out = append(out, map[string]any{"id": id, "name": name, "description": desc, "ownerUsername": owner, "createdAt": created, "updatedAt": updated})
	}
	writeJSON(w, 200, out)
}

func (s *Server) createProject(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.FromContext(r.Context())
	if !p.CanCreateProjects {
		writeError(w, 403, errors.New("not in creator group"))
		return
	}
	var req struct{ Name, Description string }
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeError(w, 400, errors.New("name required"))
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	res, err := tx.ExecContext(r.Context(), `INSERT INTO projects(name, description, owner_username) VALUES (?, ?, ?)`, req.Name, req.Description, p.Username)
	if err != nil {
		_ = tx.Rollback()
		writeError(w, 500, err)
		return
	}
	id, _ := res.LastInsertId()
	_, _ = tx.ExecContext(r.Context(), `INSERT INTO project_memberships(project_id, username, role) VALUES (?, ?, 'owner')`, id, p.Username)
	if err := tx.Commit(); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "name": req.Name, "description": req.Description, "ownerUsername": p.Username})
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	var project map[string]any
	row := s.db.QueryRowContext(r.Context(), `SELECT id, name, description, owner_username FROM projects WHERE id=?`, projectID)
	var id int64
	var name, desc, owner string
	if err := row.Scan(&id, &name, &desc, &owner); err != nil {
		writeError(w, 404, err)
		return
	}
	project = map[string]any{"id": id, "name": name, "description": desc, "ownerUsername": owner}
	project["perspectives"] = s.queryRows(r.Context(), `SELECT id, name, sort_order FROM perspectives WHERE project_id=? ORDER BY sort_order,id`, projectID)
	project["tracks"] = s.queryRows(r.Context(), `SELECT id, perspective_id, kind, name, sort_order FROM tracks WHERE project_id=? ORDER BY sort_order,id`, projectID)
	project["clips"] = s.queryRows(r.Context(), `SELECT id, perspective_id, track_id, source_asset_id, source_revision_id, COALESCE(hls_asset_id,0), media_kind, wallclock_start_ms, duration_ms, COALESCE(fps_num,0), COALESCE(fps_den,0), stream_index, display_name, ingest_status FROM clips WHERE project_id=? ORDER BY wallclock_start_ms,id`, projectID)
	writeJSON(w, 200, project)
}

func (s *Server) patchProject(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireOwner(w, r, projectID) {
		return
	}
	var req struct{ Name, Description string }
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	_, err := s.db.ExecContext(r.Context(), `UPDATE projects SET name=COALESCE(NULLIF(?,''),name), description=?, updated_at=datetime('now') WHERE id=?`, req.Name, req.Description, projectID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) listSources(w http.ResponseWriter, r *http.Request) {
	prefix := r.URL.Query().Get("prefix")
	delimiter := r.URL.Query().Get("delimiter")
	if delimiter == "" {
		delimiter = "/"
	}
	items, err := s.source.List(r.Context(), prefix, delimiter)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, items)
}

func (s *Server) probeSource(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireOwner(w, r, projectID) {
		return
	}
	sourcePath := r.URL.Query().Get("path")
	if strings.TrimSpace(sourcePath) == "" {
		writeError(w, 400, errors.New("path required"))
		return
	}
	info, err := s.source.Stat(r.Context(), storage.ObjectRef{Adapter: s.cfg.SourceAdapter, Path: sourcePath})
	if err != nil {
		writeError(w, 400, err)
		return
	}
	probe, err := s.probeSourcePath(r.Context(), sourcePath)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	streams := ff.StreamSummaries(probe)
	writeJSON(w, 200, map[string]any{"sourcePath": sourcePath, "name": filepath.Base(sourcePath), "sizeBytes": info.SizeBytes, "streams": streams})
}

func (s *Server) probeSourcePath(ctx context.Context, sourcePath string) (ff.Probe, error) {
	if local, ok := s.source.(*localstore.Source); ok {
		input, err := local.ResolvePath(sourcePath)
		if err != nil {
			return ff.Probe{}, err
		}
		return ff.Runner{FFmpeg: s.cfg.FFmpegBin, FFprobe: s.cfg.FFprobeBin}.Probe(ctx, input)
	}
	return ff.Probe{}, errors.New("source probing for non-local adapters is not implemented in this POC; S3 support needs presigned ffprobe input")
}

func (s *Server) createAssetClip(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireOwner(w, r, projectID) {
		return
	}
	var req struct {
		SourcePath       string             `json:"sourcePath"`
		Perspective      string             `json:"perspective"`
		Track            string             `json:"track"`
		Kind             string             `json:"kind"`
		WallclockStartMS int64              `json:"wallclockStartMs"`
		DisplayName      string             `json:"displayName"`
		StreamIndex      *int               `json:"streamIndex"`
		Streams          []createStreamSpec `json:"streams"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	req.SourcePath = strings.TrimLeft(req.SourcePath, "/")
	if strings.TrimSpace(req.SourcePath) == "" {
		writeError(w, 400, errors.New("sourcePath required"))
		return
	}
	if req.Perspective == "" {
		req.Perspective = inferPerspective(req.SourcePath)
	}
	if req.DisplayName == "" {
		req.DisplayName = filepath.Base(req.SourcePath)
	}
	info, err := s.source.Stat(r.Context(), storage.ObjectRef{Adapter: s.cfg.SourceAdapter, Path: req.SourcePath})
	if err != nil {
		writeError(w, 400, err)
		return
	}
	streams, err := s.selectedStreamSpecs(r.Context(), req.SourcePath, req.Streams, req.StreamIndex, req.Kind, req.Track, req.DisplayName)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	clipReqs := make([]createClipReq, 0, len(streams))
	for _, st := range streams {
		track := strings.TrimSpace(st.Track)
		if track == "" {
			track = req.Perspective + " / " + defaultStreamTrackName(st)
		}
		displayName := strings.TrimSpace(st.DisplayName)
		if displayName == "" {
			displayName = req.DisplayName + " - " + defaultStreamTrackName(st)
		}
		clipReqs = append(clipReqs, createClipReq{SourcePath: req.SourcePath, Perspective: req.Perspective, Track: track, Kind: st.Kind, WallclockStartMS: req.WallclockStartMS, DisplayName: displayName, StreamIndex: st.StreamIndex, DurationMS: st.DurationMS, FPSNum: st.FPSNum, FPSDen: st.FPSDen})
	}
	clipIDs, pendingClipIDs, reusedClipIDs, err := s.createSourceRevisionClips(r.Context(), projectID, clipReqs, info)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	jobIDs, err := s.enqueueClipJobs(r.Context(), projectID, pendingClipIDs)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, map[string]any{"clipIds": clipIDs, "reusedClipIds": reusedClipIDs, "prepareJobIds": jobIDs})
}

type createStreamSpec struct {
	StreamIndex int    `json:"streamIndex"`
	Kind        string `json:"kind"`
	Track       string `json:"track"`
	DisplayName string `json:"displayName"`
	Label       string `json:"label"`
	DurationMS  int64  `json:"durationMs"`
	FPSNum      int64  `json:"fpsNum"`
	FPSDen      int64  `json:"fpsDen"`
}

type createClipReq struct {
	SourcePath, Perspective, Track, Kind, DisplayName string
	WallclockStartMS                                  int64
	StreamIndex                                       int
	DurationMS                                        int64
	FPSNum                                            int64
	FPSDen                                            int64
}

func (s *Server) selectedStreamSpecs(ctx context.Context, sourcePath string, requested []createStreamSpec, streamIndex *int, kind, track, displayName string) ([]createStreamSpec, error) {
	probe, probeErr := s.probeSourcePath(ctx, sourcePath)
	summaryByIndex := map[int]ff.StreamSummary{}
	if probeErr == nil {
		for _, summary := range ff.StreamSummaries(probe) {
			summaryByIndex[summary.Index] = summary
		}
	}

	enrich := func(st createStreamSpec) (createStreamSpec, error) {
		st.Kind = strings.ToLower(strings.TrimSpace(st.Kind))
		if summary, ok := summaryByIndex[st.StreamIndex]; ok {
			if st.Kind == "" {
				st.Kind = summary.Kind
			}
			if strings.TrimSpace(st.Label) == "" {
				st.Label = summary.Label
			}
			st.DurationMS = summary.DurationMS
			st.FPSNum = summary.FPSNum
			st.FPSDen = summary.FPSDen
		}
		if st.Kind == "" {
			st.Kind = strings.ToLower(strings.TrimSpace(kind))
		}
		if st.Kind == "" {
			st.Kind = "video"
		}
		if st.Kind != "video" && st.Kind != "audio" {
			return st, fmt.Errorf("stream %d has invalid kind %q", st.StreamIndex, st.Kind)
		}
		if st.Kind == "audio" {
			st.FPSNum, st.FPSDen = 0, 0
		}
		return st, nil
	}

	if len(requested) > 0 {
		out := make([]createStreamSpec, 0, len(requested))
		for _, st := range requested {
			enriched, err := enrich(st)
			if err != nil {
				return nil, err
			}
			out = append(out, enriched)
		}
		return out, nil
	}
	if streamIndex != nil {
		enriched, err := enrich(createStreamSpec{StreamIndex: *streamIndex, Kind: kind, Track: track, DisplayName: displayName})
		if err != nil {
			return nil, err
		}
		return []createStreamSpec{enriched}, nil
	}
	if probeErr != nil {
		fallback, err := enrich(createStreamSpec{StreamIndex: -1, Kind: kind, Track: track, DisplayName: displayName})
		if err != nil {
			return nil, err
		}
		return []createStreamSpec{fallback}, nil
	}
	summaries := ff.StreamSummaries(probe)
	out := make([]createStreamSpec, 0, len(summaries))
	for _, st := range summaries {
		out = append(out, createStreamSpec{StreamIndex: st.Index, Kind: st.Kind, Track: track, DisplayName: displayName, Label: st.Label, DurationMS: st.DurationMS, FPSNum: st.FPSNum, FPSDen: st.FPSDen})
	}
	if len(out) == 0 {
		return nil, errors.New("no video or audio streams found")
	}
	return out, nil
}

func defaultStreamTrackName(st createStreamSpec) string {
	label := strings.TrimSpace(st.Label)
	if label != "" {
		return label
	}
	if st.Kind == "audio" {
		return fmt.Sprintf("Audio %d", st.StreamIndex)
	}
	return fmt.Sprintf("Video %d", st.StreamIndex)
}

func (s *Server) createSourceRevisionClips(ctx context.Context, projectID int64, reqs []createClipReq, info storage.ObjectInfo) (clipIDs []int64, pendingClipIDs []int64, reusedClipIDs []int64, err error) {
	if len(reqs) == 0 {
		return nil, nil, nil, errors.New("at least one stream required")
	}
	fp := ingest.FingerprintJSON(info)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var assetID, revID int64
	displayName := reqs[0].DisplayName
	if err = tx.QueryRowContext(ctx, `SELECT id FROM source_assets WHERE current_path=?`, info.Ref.Path).Scan(&assetID); err == sql.ErrNoRows {
		var res sql.Result
		res, err = tx.ExecContext(ctx, `INSERT INTO source_assets(adapter, bucket, current_key, current_path, display_name) VALUES (?, ?, ?, ?, ?)`, info.Ref.Adapter, info.Ref.Bucket, info.Ref.Key, info.Ref.Path, displayName)
		if err != nil {
			return nil, nil, nil, err
		}
		assetID, _ = res.LastInsertId()
	} else if err != nil {
		return nil, nil, nil, err
	} else {
		_, _ = tx.ExecContext(ctx, `UPDATE source_assets SET current_key=?, current_path=?, updated_at=datetime('now') WHERE id=?`, info.Ref.Key, info.Ref.Path, assetID)
	}

	if err = tx.QueryRowContext(ctx, `SELECT id FROM source_asset_revisions WHERE source_asset_id=? AND fingerprint_json=?`, assetID, fp).Scan(&revID); err == sql.ErrNoRows {
		var res sql.Result
		res, err = tx.ExecContext(ctx, `INSERT INTO source_asset_revisions(source_asset_id, adapter, bucket, key, path, size_bytes, fingerprint_json) VALUES (?, ?, ?, ?, ?, ?, ?)`, assetID, info.Ref.Adapter, info.Ref.Bucket, info.Ref.Key, info.Ref.Path, info.SizeBytes, fp)
		if err != nil {
			return nil, nil, nil, err
		}
		revID, _ = res.LastInsertId()
	} else if err != nil {
		return nil, nil, nil, err
	}

	perspectiveIDs := map[string]int64{}
	trackIDs := map[string]int64{}
	for _, req := range reqs {
		if req.Kind != "video" && req.Kind != "audio" {
			return nil, nil, nil, fmt.Errorf("invalid media kind %q", req.Kind)
		}
		perspectiveID, e := perspectiveIDFor(ctx, tx, projectID, req.Perspective, perspectiveIDs)
		if e != nil {
			err = e
			return nil, nil, nil, err
		}
		trackID, e := trackIDFor(ctx, tx, projectID, perspectiveID, req.Kind, req.Track, trackIDs)
		if e != nil {
			err = e
			return nil, nil, nil, err
		}

		// Import is idempotent for the same project/track/source revision/stream. Clicking
		// the same source repeatedly should select the existing timeline reference instead
		// of creating duplicate clips or duplicate transcode jobs.
		var existingClipID int64
		err = tx.QueryRowContext(ctx, `
SELECT id FROM clips
WHERE project_id=? AND source_revision_id=? AND track_id=? AND media_kind=? AND stream_index=?
LIMIT 1`, projectID, revID, trackID, req.Kind, req.StreamIndex).Scan(&existingClipID)
		if err == nil {
			if req.DurationMS > 0 {
				_, _ = tx.ExecContext(ctx, `UPDATE clips SET duration_ms=CASE WHEN duration_ms=0 THEN ? ELSE duration_ms END, fps_num=COALESCE(fps_num, NULLIF(?,0)), fps_den=COALESCE(fps_den, NULLIF(?,0)), updated_at=datetime('now') WHERE id=?`, req.DurationMS, req.FPSNum, req.FPSDen, existingClipID)
			}
			clipIDs = append(clipIDs, existingClipID)
			reusedClipIDs = append(reusedClipIDs, existingClipID)
			continue
		}
		if err != sql.ErrNoRows {
			return nil, nil, nil, err
		}

		var hlsID sql.NullInt64
		durationMS := req.DurationMS
		fpsNum, fpsDen := req.FPSNum, req.FPSDen
		status := "PENDING"
		streamID := fmt.Sprintf("stream-%d", req.StreamIndex)
		var hlsDurationMS, hlsFPSNum, hlsFPSDen int64
		err = tx.QueryRowContext(ctx, `
SELECT id, duration_ms, COALESCE(fps_num,0), COALESCE(fps_den,0)
FROM hls_assets
WHERE source_revision_id=? AND stream_id=? AND transcode_profile_version=?`, revID, streamID, s.cfg.TranscodeProfile).Scan(&hlsID, &hlsDurationMS, &hlsFPSNum, &hlsFPSDen)
		if err == nil {
			status = "SUCCESS"
			durationMS, fpsNum, fpsDen = hlsDurationMS, hlsFPSNum, hlsFPSDen
		} else if err == sql.ErrNoRows {
			err = nil
		} else {
			return nil, nil, nil, err
		}

		var hlsValue any
		if hlsID.Valid {
			hlsValue = hlsID.Int64
		}
		res, e := tx.ExecContext(ctx, `
INSERT INTO clips(project_id, perspective_id, track_id, source_asset_id, source_revision_id, hls_asset_id, media_kind, wallclock_start_ms, duration_ms, fps_num, fps_den, display_name, stream_index, ingest_status)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?,0), NULLIF(?,0), ?, ?, ?)`, projectID, perspectiveID, trackID, assetID, revID, hlsValue, req.Kind, req.WallclockStartMS, durationMS, fpsNum, fpsDen, req.DisplayName, req.StreamIndex, status)
		if e != nil {
			err = e
			return nil, nil, nil, err
		}
		clipID, _ := res.LastInsertId()
		clipIDs = append(clipIDs, clipID)
		if status != "SUCCESS" {
			pendingClipIDs = append(pendingClipIDs, clipID)
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, nil, nil, err
	}
	return clipIDs, pendingClipIDs, reusedClipIDs, nil
}

func perspectiveIDFor(ctx context.Context, tx *sql.Tx, projectID int64, name string, cache map[string]int64) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Untitled perspective"
	}
	if id, ok := cache[name]; ok {
		return id, nil
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM perspectives WHERE project_id=? AND name=?`, projectID, name).Scan(&id); err == sql.ErrNoRows {
		res, err := tx.ExecContext(ctx, `INSERT INTO perspectives(project_id, name) VALUES (?, ?)`, projectID, name)
		if err != nil {
			return 0, err
		}
		id, _ = res.LastInsertId()
	} else if err != nil {
		return 0, err
	}
	cache[name] = id
	return id, nil
}

func trackIDFor(ctx context.Context, tx *sql.Tx, projectID, perspectiveID int64, kind, name string, cache map[string]int64) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = titleKind(kind)
	}
	cacheKey := fmt.Sprintf("%d:%s:%s", perspectiveID, kind, name)
	if id, ok := cache[cacheKey]; ok {
		return id, nil
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM tracks WHERE project_id=? AND perspective_id=? AND kind=? AND name=?`, projectID, perspectiveID, kind, name).Scan(&id); err == sql.ErrNoRows {
		res, err := tx.ExecContext(ctx, `INSERT INTO tracks(project_id, perspective_id, kind, name) VALUES (?, ?, ?, ?)`, projectID, perspectiveID, kind, name)
		if err != nil {
			return 0, err
		}
		id, _ = res.LastInsertId()
	} else if err != nil {
		return 0, err
	}
	cache[cacheKey] = id
	return id, nil
}

func (s *Server) enqueueClipJobs(ctx context.Context, projectID int64, clipIDs []int64) ([]int64, error) {
	if len(clipIDs) == 0 {
		return nil, nil
	}

	values := make([]string, 0, len(clipIDs))
	args := make([]any, 0, len(clipIDs)+2)
	for _, clipID := range clipIDs {
		values = append(values, "(?)")
		args = append(args, clipID)
	}
	args = append(args, projectID, projectID)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
WITH requested(clip_id) AS (VALUES %s),
active_jobs AS (
	SELECT clip_id, MAX(id) AS job_id
	FROM ingest_jobs
	WHERE project_id=? AND state IN ('PENDING','PROCESSING')
	GROUP BY clip_id
)
SELECT r.clip_id, c.ingest_status, COALESCE(a.job_id, 0)
FROM requested r
JOIN clips c ON c.id=r.clip_id AND c.project_id=?
LEFT JOIN active_jobs a ON a.clip_id=r.clip_id`, strings.Join(values, ",")), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type plan struct {
		status      string
		activeJobID int64
	}
	plans := make(map[int64]plan, len(clipIDs))
	for rows.Next() {
		var clipID int64
		var p plan
		if err := rows.Scan(&clipID, &p.status, &p.activeJobID); err != nil {
			return nil, err
		}
		plans[clipID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	jobIDs := make([]int64, 0, len(clipIDs))
	for _, clipID := range clipIDs {
		p, ok := plans[clipID]
		if !ok {
			return nil, fmt.Errorf("clip %d not found in project %d", clipID, projectID)
		}
		if p.status == "SUCCESS" {
			continue
		}
		if p.activeJobID != 0 {
			jobIDs = append(jobIDs, p.activeJobID)
			continue
		}

		res, err := s.db.ExecContext(ctx, `INSERT INTO ingest_jobs(project_id, clip_id, state) VALUES (?, ?, 'PENDING')`, projectID, clipID)
		if err != nil {
			return nil, err
		}
		jobID, _ := res.LastInsertId()
		jobIDs = append(jobIDs, jobID)
		p.activeJobID = jobID
		plans[clipID] = p
		s.ingest.Enqueue(jobID)
	}
	return jobIDs, nil
}

func (s *Server) triggerIngest(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireOwner(w, r, projectID) {
		return
	}
	ids, err := s.ingest.EnqueueProject(r.Context(), projectID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 202, map[string]any{"jobIds": ids})
}

func (s *Server) listIngestJobs(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	rows := s.queryRows(r.Context(), `SELECT id, clip_id, state, error, created_at, started_at, finished_at FROM ingest_jobs WHERE project_id=? ORDER BY id DESC`, projectID)
	writeJSON(w, 200, rows)
}

func (s *Server) createPerspective(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireOwner(w, r, projectID) {
		return
	}
	var req struct {
		Name      string
		SortOrder int64
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO perspectives(project_id, name, sort_order) VALUES (?, ?, ?)`, projectID, req.Name, req.SortOrder)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *Server) patchPerspective(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	perspectiveID, ok := pathID(w, r, "perspectiveID")
	if !ok {
		return
	}
	if !s.requireOwner(w, r, projectID) {
		return
	}
	var req struct {
		Name      string
		SortOrder int64
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	_, err := s.db.ExecContext(r.Context(), `UPDATE perspectives SET name=COALESCE(NULLIF(?,''), name), sort_order=? WHERE id=? AND project_id=?`, req.Name, req.SortOrder, perspectiveID, projectID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) createTrack(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireOwner(w, r, projectID) {
		return
	}
	var req struct {
		PerspectiveID int64 `json:"perspectiveId"`
		Name, Kind    string
		SortOrder     int64
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO tracks(project_id, perspective_id, kind, name, sort_order) VALUES (?, ?, ?, ?, ?)`, projectID, req.PerspectiveID, req.Kind, req.Name, req.SortOrder)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	id, _ := res.LastInsertId()
	writeJSON(w, 201, map[string]any{"id": id})
}

func (s *Server) patchClip(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	clipID, ok := pathID(w, r, "clipID")
	if !ok {
		return
	}
	if !s.requireOwner(w, r, projectID) {
		return
	}
	var req struct {
		WallclockStartMS *int64 `json:"wallclockStartMs"`
		DisplayName      string `json:"displayName"`
		StreamIndex      *int   `json:"streamIndex"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if req.WallclockStartMS != nil {
		_, err := s.db.ExecContext(r.Context(), `UPDATE clips SET wallclock_start_ms=?, display_name=COALESCE(NULLIF(?,''),display_name), updated_at=datetime('now') WHERE id=? AND project_id=?`, *req.WallclockStartMS, req.DisplayName, clipID, projectID)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		s.broadcast(r, projectID, "clip.timeline.updated", map[string]any{"clipId": clipID, "wallclockStartMs": *req.WallclockStartMS})
	} else {
		_, err := s.db.ExecContext(r.Context(), `UPDATE clips SET display_name=COALESCE(NULLIF(?,''),display_name), updated_at=datetime('now') WHERE id=? AND project_id=?`, req.DisplayName, clipID, projectID)
		if err != nil {
			writeError(w, 500, err)
			return
		}
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteClip(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	clipID, ok := pathID(w, r, "clipID")
	if !ok {
		return
	}
	if !s.requireOwner(w, r, projectID) {
		return
	}
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM clips WHERE id=? AND project_id=?`, clipID, projectID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, 404, errors.New("clip not found"))
		return
	}
	s.broadcast(r, projectID, "clip.deleted", map[string]any{"clipId": clipID})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) playbackManifest(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
SELECT c.id, c.perspective_id, p.name, c.track_id, t.name, c.media_kind, c.wallclock_start_ms, c.duration_ms,
       COALESCE(c.fps_num,0), COALESCE(c.fps_den,0), c.stream_index, c.display_name, c.ingest_status,
       COALESCE(h.playlist_path, '')
FROM clips c
JOIN tracks t ON t.id=c.track_id
JOIN perspectives p ON p.id=c.perspective_id
LEFT JOIN hls_assets h ON h.id=c.hls_asset_id
WHERE c.project_id=?
ORDER BY c.wallclock_start_ms, c.id`, projectID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer rows.Close()
	var clips []map[string]any
	for rows.Next() {
		var clipID, pid, tid, start, dur, fpsN, fpsD int64
		var streamIndex int
		var pname, tname, kind, displayName, status, playlist string
		if err := rows.Scan(&clipID, &pid, &pname, &tid, &tname, &kind, &start, &dur, &fpsN, &fpsD, &streamIndex, &displayName, &status, &playlist); err != nil {
			writeError(w, 500, err)
			return
		}
		url := ""
		if playlist != "" && status == "SUCCESS" {
			var err error
			url, err = s.hls.PublicOrSignedURL(r.Context(), storage.ObjectRef{Adapter: s.cfg.HLSAdapter, Path: playlist}, s.cfg.HLSPresignTTL)
			if err != nil {
				writeError(w, 500, err)
				return
			}
		}
		clips = append(clips, map[string]any{"clipId": clipID, "perspectiveId": pid, "perspectiveName": pname, "trackId": tid, "trackName": tname, "kind": kind, "wallclockStartMs": start, "durationMs": dur, "fpsNum": fpsN, "fpsDen": fpsD, "streamIndex": streamIndex, "displayName": displayName, "ingestStatus": status, "hlsURL": url})
	}
	writeJSON(w, 200, map[string]any{"projectId": projectID, "clips": clips})
}

func (s *Server) listMarkers(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	writeJSON(w, 200, s.queryRows(r.Context(), `SELECT id, marker_ts_ms, author_username, author_color, author_color AS authorColor, label, COALESCE(note,'') AS note, created_at, updated_at FROM markers WHERE project_id=? ORDER BY marker_ts_ms`, projectID))
}

func (s *Server) createMarker(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	p, _ := auth.FromContext(r.Context())
	var req struct {
		TsMS        int64 `json:"tsMs"`
		Label, Note string
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO markers(project_id, marker_ts_ms, author_username, author_color, label, note) VALUES (?, ?, ?, ?, ?, ?)`, projectID, req.TsMS, p.Username, p.Color, req.Label, req.Note)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	id, _ := res.LastInsertId()
	payload := map[string]any{"id": id, "tsMs": req.TsMS, "label": req.Label, "note": req.Note, "author": p.Username, "color": p.Color}
	s.broadcast(r, projectID, "marker.created", payload)
	writeJSON(w, 201, payload)
}

func (s *Server) patchMarker(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	markerID, ok := pathID(w, r, "markerID")
	if !ok {
		return
	}
	p, _ := auth.FromContext(r.Context())
	if !s.canEditMarker(r.Context(), projectID, markerID, p.Username) {
		writeError(w, 403, errors.New("not marker author or project owner"))
		return
	}
	var req struct {
		TsMS        *int64 `json:"tsMs"`
		Label, Note string
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if req.TsMS == nil {
		var cur int64
		_ = s.db.QueryRowContext(r.Context(), `SELECT marker_ts_ms FROM markers WHERE id=?`, markerID).Scan(&cur)
		req.TsMS = &cur
	}
	_, err := s.db.ExecContext(r.Context(), `UPDATE markers SET marker_ts_ms=?, label=COALESCE(NULLIF(?,''),label), note=?, updated_at=datetime('now') WHERE id=? AND project_id=?`, *req.TsMS, req.Label, req.Note, markerID, projectID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	s.broadcast(r, projectID, "marker.updated", map[string]any{"id": markerID})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteMarker(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	markerID, ok := pathID(w, r, "markerID")
	if !ok {
		return
	}
	p, _ := auth.FromContext(r.Context())
	if !s.canEditMarker(r.Context(), projectID, markerID, p.Username) {
		writeError(w, 403, errors.New("not marker author or project owner"))
		return
	}
	_, err := s.db.ExecContext(r.Context(), `DELETE FROM markers WHERE id=? AND project_id=?`, markerID, projectID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	s.broadcast(r, projectID, "marker.deleted", map[string]any{"id": markerID})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) listRegions(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	writeJSON(w, 200, s.queryRows(r.Context(), `SELECT id, region_start_ms, region_end_ms, author_username, author_color, author_color AS authorColor, label, COALESCE(note,'') AS note, created_at, updated_at FROM regions WHERE project_id=? ORDER BY region_start_ms`, projectID))
}

func (s *Server) createRegion(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	p, _ := auth.FromContext(r.Context())
	var req struct {
		StartMS int64  `json:"startMs"`
		EndMS   int64  `json:"endMs"`
		Label   string `json:"label"`
		Note    string `json:"note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if req.EndMS < req.StartMS {
		writeError(w, 400, errors.New("end before start"))
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO regions(project_id, region_start_ms, region_end_ms, author_username, author_color, label, note) VALUES (?, ?, ?, ?, ?, ?, ?)`, projectID, req.StartMS, req.EndMS, p.Username, p.Color, req.Label, req.Note)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	id, _ := res.LastInsertId()
	payload := map[string]any{"id": id, "startMs": req.StartMS, "endMs": req.EndMS, "label": req.Label, "note": req.Note, "author": p.Username, "color": p.Color}
	s.broadcast(r, projectID, "region.created", payload)
	writeJSON(w, 201, payload)
}

func (s *Server) patchRegion(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	regionID, ok := pathID(w, r, "regionID")
	if !ok {
		return
	}
	p, _ := auth.FromContext(r.Context())
	if !s.canEditRegion(r.Context(), projectID, regionID, p.Username) {
		writeError(w, 403, errors.New("not region author or project owner"))
		return
	}
	var req struct {
		StartMS int64  `json:"startMs"`
		EndMS   int64  `json:"endMs"`
		Label   string `json:"label"`
		Note    string `json:"note"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	_, err := s.db.ExecContext(r.Context(), `UPDATE regions SET region_start_ms=?, region_end_ms=?, label=COALESCE(NULLIF(?,''),label), note=?, updated_at=datetime('now') WHERE id=? AND project_id=?`, req.StartMS, req.EndMS, req.Label, req.Note, regionID, projectID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	s.broadcast(r, projectID, "region.updated", map[string]any{"id": regionID})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) deleteRegion(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	regionID, ok := pathID(w, r, "regionID")
	if !ok {
		return
	}
	p, _ := auth.FromContext(r.Context())
	if !s.canEditRegion(r.Context(), projectID, regionID, p.Username) {
		writeError(w, 403, errors.New("not region author or project owner"))
		return
	}
	_, err := s.db.ExecContext(r.Context(), `DELETE FROM regions WHERE id=? AND project_id=?`, regionID, projectID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	s.broadcast(r, projectID, "region.deleted", map[string]any{"id": regionID})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) exportCSV(w http.ResponseWriter, r *http.Request) {
	s.withItems(w, r, "text/csv", "markers.csv", exp.WriteCSV)
}
func (s *Server) exportMarkdown(w http.ResponseWriter, r *http.Request) {
	s.withItems(w, r, "text/markdown", "markers.md", exp.WriteMarkdown)
}
func (s *Server) exportJSON(w http.ResponseWriter, r *http.Request) {
	s.withItems(w, r, "application/json", "markers.json", exp.WriteJSON)
}
func (s *Server) exportEDL(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	w.Header().Set("content-type", "text/plain")
	w.Header().Set("content-disposition", `attachment; filename="regions.edl"`)
	if err := exp.WriteEDL(r.Context(), s.db, w, projectID); err != nil {
		writeError(w, 500, err)
		return
	}
}

type itemWriter func(io.Writer, []exp.Item) error

func (s *Server) withItems(w http.ResponseWriter, r *http.Request, contentType, filename string, fn itemWriter) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	items, err := exp.Items(r.Context(), s.db, projectID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	w.Header().Set("content-type", contentType)
	w.Header().Set("content-disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	if err := fn(w, items); err != nil {
		writeError(w, 500, err)
		return
	}
}

func (s *Server) wsProject(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	p, _ := auth.FromContext(r.Context())
	realtime.ServeWS(s.hub, w, r, projectID, p.Username, p.Color)
}

func (s *Server) localHLS(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("path")
	if p == "" || strings.Contains(p, "..") {
		writeError(w, 400, errors.New("bad hls path"))
		return
	}
	rc, err := s.hls.Open(r.Context(), storage.ObjectRef{Adapter: s.cfg.HLSAdapter, Path: p})
	if err != nil {
		writeError(w, 404, err)
		return
	}
	defer rc.Close()
	if ct := mime.TypeByExtension(filepath.Ext(p)); ct != "" {
		w.Header().Set("content-type", ct)
	}
	if strings.HasSuffix(p, ".m3u8") {
		w.Header().Set("content-type", "application/vnd.apple.mpegurl")
	}
	if strings.HasSuffix(p, ".ts") {
		w.Header().Set("content-type", "video/MP2T")
	}
	w.Header().Set("cache-control", "private, max-age=300")
	_, _ = io.Copy(w, rc)
}

func (s *Server) requireOwner(w http.ResponseWriter, r *http.Request, projectID int64) bool {
	p, _ := auth.FromContext(r.Context())
	var owner string
	if err := s.db.QueryRowContext(r.Context(), `SELECT owner_username FROM projects WHERE id=?`, projectID).Scan(&owner); err != nil {
		writeError(w, 404, err)
		return false
	}
	if owner != p.Username {
		writeError(w, 403, errors.New("project owner required"))
		return false
	}
	return true
}

func (s *Server) canEditMarker(ctx context.Context, projectID, markerID int64, username string) bool {
	var allowed int
	err := s.db.QueryRowContext(ctx, `
SELECT CASE WHEN p.owner_username=? OR m.author_username=? THEN 1 ELSE 0 END
FROM markers m
JOIN projects p ON p.id=m.project_id
WHERE m.id=? AND m.project_id=?`, username, username, markerID, projectID).Scan(&allowed)
	return err == nil && allowed == 1
}

func (s *Server) canEditRegion(ctx context.Context, projectID, regionID int64, username string) bool {
	var allowed int
	err := s.db.QueryRowContext(ctx, `
SELECT CASE WHEN p.owner_username=? OR r.author_username=? THEN 1 ELSE 0 END
FROM regions r
JOIN projects p ON p.id=r.project_id
WHERE r.id=? AND r.project_id=?`, username, username, regionID, projectID).Scan(&allowed)
	return err == nil && allowed == 1
}

func (s *Server) broadcast(r *http.Request, projectID int64, typ string, payload any) {
	p, _ := auth.FromContext(r.Context())
	s.hub.Broadcast(projectID, realtime.Event{Type: typ, ProjectID: projectID, User: p.Username, Color: p.Color, Payload: payload})
}

func (s *Server) queryRows(ctx context.Context, query string, args ...any) []map[string]any {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return []map[string]any{}
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			continue
		}
		m := map[string]any{}
		for i, c := range cols {
			switch v := vals[i].(type) {
			case []byte:
				m[c] = string(v)
			default:
				m[c] = v
			}
		}
		out = append(out, m)
	}
	return out
}

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, fmt.Errorf("invalid %s", name))
		return 0, false
	}
	return id, true
}

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(dst)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func inferPerspective(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) > 1 && parts[0] != "" {
		return parts[len(parts)-2]
	}
	return "Default"
}
func titleKind(kind string) string {
	if kind == "audio" {
		return "Audio"
	}
	return "Video"
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *loggingResponseWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (w *loggingResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *loggingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("response writer does not support hijacking")
	}
	if w.status == 0 {
		w.status = http.StatusSwitchingProtocols
	}
	return h.Hijack()
}

func (w *loggingResponseWriter) ReadFrom(r io.Reader) (int64, error) {
	if rf, ok := w.ResponseWriter.(io.ReaderFrom); ok {
		if w.status == 0 {
			w.status = http.StatusOK
		}
		n, err := rf.ReadFrom(r)
		w.bytes += n
		return n, err
	}
	type onlyWriter struct{ io.Writer }
	return io.Copy(onlyWriter{w}, r)
}

func (w *loggingResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		recorder := &loggingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		log.Printf("http %s %s status=%d bytes=%d duration=%s", r.Method, r.URL.Path, status, recorder.bytes, time.Since(start))
	})
}
