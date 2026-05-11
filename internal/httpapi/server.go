package httpapi

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ganyuke/multitrack-drifter/internal/auth"
	"github.com/ganyuke/multitrack-drifter/internal/config"
	exp "github.com/ganyuke/multitrack-drifter/internal/export"
	ff "github.com/ganyuke/multitrack-drifter/internal/ffmpeg"
	"github.com/ganyuke/multitrack-drifter/internal/hlsassets"
	"github.com/ganyuke/multitrack-drifter/internal/ingest"
	"github.com/ganyuke/multitrack-drifter/internal/realtime"
	"github.com/ganyuke/multitrack-drifter/internal/storage"
)

type Server struct {
	db       *sql.DB
	cfg      config.Config
	auth     *auth.Service
	source   storage.SourceStore
	hls      storage.HLSStore
	ingest   *ingest.Worker
	hub      *realtime.Hub
	static   http.Handler
	urlCache signedURLCache
}

type signedURLCache struct {
	mu sync.Mutex
	m  map[string]signedURLCacheEntry
}

type signedURLCacheEntry struct {
	url       string
	expiresAt time.Time
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
	mux.Handle("PATCH /api/projects/{projectID}", s.auth.Middleware(http.HandlerFunc(s.patchProject)))
	mux.Handle("GET /api/projects/{projectID}/members", s.auth.Middleware(http.HandlerFunc(s.listProjectMembers)))
	mux.Handle("POST /api/projects/{projectID}/members", s.auth.Middleware(http.HandlerFunc(s.addProjectMember)))
	mux.Handle("PATCH /api/projects/{projectID}/members/{username}", s.auth.Middleware(http.HandlerFunc(s.patchProjectMember)))
	mux.Handle("DELETE /api/projects/{projectID}/members/{username}", s.auth.Middleware(http.HandlerFunc(s.deleteProjectMember)))
	mux.Handle("GET /api/projects/{projectID}/sources", s.auth.Middleware(http.HandlerFunc(s.listSources)))
	mux.Handle("GET /api/projects/{projectID}/sources/probe", s.auth.Middleware(http.HandlerFunc(s.probeSource)))
	mux.Handle("POST /api/projects/{projectID}/assets", s.auth.Middleware(http.HandlerFunc(s.createAssetClip)))
	mux.Handle("POST /api/projects/{projectID}/ingest", s.auth.Middleware(http.HandlerFunc(s.triggerIngest)))
	mux.Handle("GET /api/projects/{projectID}/ingest-jobs", s.auth.Middleware(http.HandlerFunc(s.listIngestJobs)))
	mux.Handle("POST /api/projects/{projectID}/clips/{clipID}/ingest", s.auth.Middleware(http.HandlerFunc(s.retryClipIngest)))
	mux.Handle("POST /api/projects/{projectID}/perspectives", s.auth.Middleware(http.HandlerFunc(s.createPerspective)))
	mux.Handle("PATCH /api/projects/{projectID}/perspectives/{perspectiveID}", s.auth.Middleware(http.HandlerFunc(s.patchPerspective)))
	mux.Handle("POST /api/projects/{projectID}/tracks", s.auth.Middleware(http.HandlerFunc(s.createTrack)))
	mux.Handle("PATCH /api/projects/{projectID}/clips", s.auth.Middleware(http.HandlerFunc(s.patchClips)))
	mux.Handle("DELETE /api/projects/{projectID}/clips", s.auth.Middleware(http.HandlerFunc(s.deleteClips)))
	mux.Handle("PATCH /api/projects/{projectID}/clips/{clipID}", s.auth.Middleware(http.HandlerFunc(s.patchClip)))
	mux.Handle("GET /api/projects/{projectID}/state", s.auth.Middleware(http.HandlerFunc(s.getProjectState)))
	mux.Handle("POST /api/projects/{projectID}/markers", s.auth.Middleware(http.HandlerFunc(s.createMarker)))
	mux.Handle("PATCH /api/projects/{projectID}/markers/{markerID}", s.auth.Middleware(http.HandlerFunc(s.patchMarker)))
	mux.Handle("DELETE /api/projects/{projectID}/markers/{markerID}", s.auth.Middleware(http.HandlerFunc(s.deleteMarker)))
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
	color, ok := auth.CanonicalColor(req.Color)
	if !ok {
		writeError(w, 400, errors.New("color not in accessible palette"))
		return
	}
	if err := s.auth.SetColor(r.Context(), p.Username, color); err != nil {
		writeError(w, 400, err)
		return
	}
	p.Color = color
	if s.hub != nil {
		s.hub.UpdateUserColor(p.Username, color)
	}
	writeJSON(w, 200, p)
}

func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	p, _ := auth.FromContext(r.Context())
	rows, err := s.db.QueryContext(r.Context(), `
SELECT DISTINCT p.id, p.name, p.description, p.owner_username, p.created_at, p.updated_at
FROM projects p
LEFT JOIN project_memberships pm ON pm.project_id=p.id AND pm.username=?
WHERE p.owner_username=? OR pm.role IN ('editor','viewer')
ORDER BY p.updated_at DESC`, p.Username, p.Username)
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
	if err := tx.Commit(); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "name": req.Name, "description": req.Description, "ownerUsername": p.Username})
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

func (s *Server) getProjectState(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireProjectMember(w, r, projectID) {
		return
	}

	var name, desc, owner string
	if err := s.db.QueryRowContext(r.Context(), `SELECT name, description, owner_username FROM projects WHERE id=?`, projectID).Scan(&name, &desc, &owner); err != nil {
		writeError(w, 404, err)
		return
	}

	perspectives := s.queryRows(r.Context(), `SELECT id, name, sort_order FROM perspectives WHERE project_id=? ORDER BY sort_order,id`, projectID)
	tracks := s.queryRows(r.Context(), `SELECT id, perspective_id, kind, name, sort_order FROM tracks WHERE project_id=? ORDER BY sort_order,id`, projectID)

	clipRows, err := s.db.QueryContext(r.Context(), `
SELECT c.id, c.perspective_id, p.name, c.track_id, t.name, c.media_kind,
       c.wallclock_start_ms, c.duration_ms,
       COALESCE(c.fps_num,0), COALESCE(c.fps_den,0),
       c.stream_index, c.display_name, c.ingest_status,
       COALESCE(h.playlist_path,''), COALESCE(c.link_group_id,'')
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
	defer clipRows.Close()

	var clips []map[string]any
	for clipRows.Next() {
		var clipID, pid, tid, start, dur, fpsN, fpsD int64
		var streamIndex int
		var pname, tname, kind, displayName, status, playlist, linkGroupID string
		if err := clipRows.Scan(&clipID, &pid, &pname, &tid, &tname, &kind, &start, &dur, &fpsN, &fpsD, &streamIndex, &displayName, &status, &playlist, &linkGroupID); err != nil {
			writeError(w, 500, err)
			return
		}
		hlsURL := ""
		if playlist != "" && status == "SUCCESS" {
			hlsURL, err = s.cachedHLSURL(r.Context(), storage.ObjectRef{Adapter: s.cfg.HLSAdapter, Path: playlist}, s.cfg.HLSPresignTTL)
			if err != nil {
				writeError(w, 500, err)
				return
			}
		}
		clips = append(clips, map[string]any{
			"clipId": clipID, "perspectiveId": pid, "perspectiveName": pname,
			"trackId": tid, "trackName": tname, "kind": kind,
			"wallclockStartMs": start, "durationMs": dur,
			"fpsNum": fpsN, "fpsDen": fpsD, "streamIndex": streamIndex,
			"displayName": displayName, "ingestStatus": status,
			"hlsURL": hlsURL, "linkGroupId": linkGroupID,
		})
	}
	if err := clipRows.Err(); err != nil {
		writeError(w, 500, err)
		return
	}

	markers := s.queryRows(r.Context(), `
SELECT m.id, m.marker_ts_ms, m.author_username, u.color AS authorColor,
       m.label, COALESCE(m.note,'') AS note, m.created_at, m.updated_at
FROM markers m
JOIN users u ON u.username=m.author_username
WHERE m.project_id=? ORDER BY m.marker_ts_ms`, projectID)

	regions := s.queryRows(r.Context(), `
SELECT r.id, r.region_start_ms, r.region_end_ms, r.author_username, u.color AS authorColor,
       r.label, COALESCE(r.note,'') AS note, r.created_at, r.updated_at
FROM regions r
JOIN users u ON u.username=r.author_username
WHERE r.project_id=? ORDER BY r.region_start_ms`, projectID)

	members := s.queryRows(r.Context(), `
SELECT username, display_name, color, role, created_at
FROM (
  SELECT u.username, u.display_name, u.color, 'owner' AS role, p.created_at, 0 AS ord
  FROM projects p JOIN users u ON u.username=p.owner_username WHERE p.id=?
  UNION ALL
  SELECT u.username, u.display_name, u.color, pm.role, pm.created_at,
    CASE pm.role WHEN 'editor' THEN 1 ELSE 2 END
  FROM project_memberships pm
  JOIN projects p ON p.id=pm.project_id
  JOIN users u ON u.username=pm.username
  WHERE pm.project_id=? AND pm.username<>p.owner_username
) ORDER BY ord, username`, projectID, projectID)

	writeJSON(w, 200, map[string]any{
		"id": projectID, "name": name, "description": desc, "ownerUsername": owner,
		"perspectives": perspectives,
		"tracks":       tracks,
		"clips":        clips,
		"markers":      markers,
		"regions":      regions,
		"members":      members,
	})
}

func (s *Server) listSources(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireProjectEditor(w, r, projectID) {
		return
	}
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
	if !s.requireProjectEditor(w, r, projectID) {
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
	ref := storage.ObjectRef{Adapter: s.cfg.SourceAdapter, Path: sourcePath}
	url, err := s.source.PresignRead(ctx, ref, 10*time.Minute)
	if err != nil {
		return ff.Probe{}, fmt.Errorf("presign read: %w", err)
	}
	return ff.Runner{FFmpeg: s.cfg.FFmpegBin, FFprobe: s.cfg.FFprobeBin}.Probe(ctx, url)
}

func (s *Server) createAssetClip(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireProjectEditor(w, r, projectID) {
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
			track = defaultStreamTrackName(st)
		}
		displayName := strings.TrimSpace(st.DisplayName)
		if displayName == "" {
			displayName = req.DisplayName + " - " + defaultStreamTrackName(st)
		}
		clipReqs = append(clipReqs, createClipReq{
			SourcePath: req.SourcePath, Perspective: req.Perspective, Track: track,
			Kind: st.Kind, WallclockStartMS: req.WallclockStartMS, DisplayName: displayName,
			StreamIndex: st.StreamIndex, DurationMS: st.DurationMS, FPSNum: st.FPSNum, FPSDen: st.FPSDen,
		})
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
	DurationMS, FPSNum, FPSDen                        int64
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
			st.DurationMS, st.FPSNum, st.FPSDen = summary.DurationMS, summary.FPSNum, summary.FPSDen
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
	if label := strings.TrimSpace(st.Label); label != "" {
		return label
	}
	if st.Kind == "audio" {
		return fmt.Sprintf("Audio %d", st.StreamIndex)
	}
	return fmt.Sprintf("Video %d", st.StreamIndex)
}

func (s *Server) createSourceRevisionClips(ctx context.Context, projectID int64, reqs []createClipReq, info storage.ObjectInfo) (clipIDs, pendingClipIDs, reusedClipIDs []int64, err error) {
	if len(reqs) == 0 {
		return nil, nil, nil, errors.New("at least one stream required")
	}
	linkGroupID := ""
	if len(reqs) > 1 {
		linkGroupID = newLinkGroupID()
	}
	fp := ingest.FingerprintJSON(info)
	etagHash := ingest.ETagHash(info)
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
	if err = tx.QueryRowContext(ctx, `SELECT id FROM source_assets WHERE current_path=?`, info.Ref.Path).Scan(&assetID); err == sql.ErrNoRows {
		var res sql.Result
		res, err = tx.ExecContext(ctx, `INSERT INTO source_assets(adapter, bucket, current_key, current_path, display_name) VALUES (?, ?, ?, ?, ?)`,
			info.Ref.Adapter, info.Ref.Bucket, info.Ref.Key, info.Ref.Path, reqs[0].DisplayName)
		if err != nil {
			return nil, nil, nil, err
		}
		assetID, _ = res.LastInsertId()
	} else if err != nil {
		return nil, nil, nil, err
	} else {
		_, _ = tx.ExecContext(ctx, `UPDATE source_assets SET current_key=?, current_path=?, updated_at=datetime('now') WHERE id=?`, info.Ref.Key, info.Ref.Path, assetID)
	}

	// Dedup: try ETag hash first (reliable content identity from Garage/S3 HeadObject,
	// no download needed). Fall back to full fingerprint for local files or multipart uploads.
	if etagHash != "" {
		_ = tx.QueryRowContext(ctx, `SELECT id FROM source_asset_revisions WHERE source_asset_id=? AND strong_hash=?`, assetID, etagHash).Scan(&revID)
	}
	if revID == 0 {
		err = tx.QueryRowContext(ctx, `SELECT id FROM source_asset_revisions WHERE source_asset_id=? AND fingerprint_json=?`, assetID, fp).Scan(&revID)
	}
	if revID == 0 {
		if !errors.Is(err, sql.ErrNoRows) && err != nil {
			return nil, nil, nil, err
		}
		var etagHashVal any
		if etagHash != "" {
			etagHashVal = etagHash
		}
		var res sql.Result
		res, err = tx.ExecContext(ctx, `INSERT INTO source_asset_revisions(source_asset_id, adapter, bucket, key, path, size_bytes, fingerprint_json, strong_hash) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			assetID, info.Ref.Adapter, info.Ref.Bucket, info.Ref.Key, info.Ref.Path, info.SizeBytes, fp, etagHashVal)
		if err != nil {
			return nil, nil, nil, err
		}
		revID, _ = res.LastInsertId()
	}

	perspIDs := map[string]int64{}
	trackIDs := map[string]int64{}
	for _, req := range reqs {
		if req.Kind != "video" && req.Kind != "audio" {
			return nil, nil, nil, fmt.Errorf("invalid media kind %q", req.Kind)
		}
		perspID, e := perspectiveIDFor(ctx, tx, projectID, req.Perspective, perspIDs)
		if e != nil {
			return nil, nil, nil, e
		}
		trackID, e := trackIDFor(ctx, tx, projectID, perspID, req.Kind, req.Track, trackIDs)
		if e != nil {
			return nil, nil, nil, e
		}

		var hlsID sql.NullInt64
		durationMS, fpsNum, fpsDen := req.DurationMS, req.FPSNum, req.FPSDen
		status := "PENDING"
		streamID := fmt.Sprintf("stream-%d", req.StreamIndex)
		var hlsDur, hlsFPSNum, hlsFPSDen int64
		var hlsPlaylistPath string
		err = tx.QueryRowContext(ctx, `
SELECT id, duration_ms, COALESCE(fps_num,0), COALESCE(fps_den,0), COALESCE(playlist_path,'')
FROM hls_assets WHERE source_revision_id=? AND stream_id=? AND transcode_profile_version=?`,
			revID, streamID, s.cfg.TranscodeProfile).Scan(&hlsID, &hlsDur, &hlsFPSNum, &hlsFPSDen, &hlsPlaylistPath)
		if err == nil {
			if s.hlsAssetIsReusable(ctx, hlsID.Int64, hlsPlaylistPath) {
				status = "SUCCESS"
				durationMS, fpsNum, fpsDen = hlsDur, hlsFPSNum, hlsFPSDen
			} else {
				hlsID = sql.NullInt64{}
			}
		} else if err == sql.ErrNoRows {
			err = nil
		} else {
			return nil, nil, nil, err
		}

		var hlsVal any
		if hlsID.Valid {
			hlsVal = hlsID.Int64
		}
		res, e := tx.ExecContext(ctx, `
INSERT INTO clips(project_id, perspective_id, track_id, source_asset_id, source_revision_id,
                  hls_asset_id, media_kind, wallclock_start_ms, duration_ms,
                  fps_num, fps_den, display_name, stream_index, ingest_status, link_group_id)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?,0), NULLIF(?,0), ?, ?, ?, ?)`,
			projectID, perspID, trackID, assetID, revID, hlsVal,
			req.Kind, req.WallclockStartMS, durationMS,
			fpsNum, fpsDen, req.DisplayName, req.StreamIndex, status, linkGroupID)
		if e != nil {
			return nil, nil, nil, e
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

func trackIDFor(ctx context.Context, tx *sql.Tx, projectID, perspID int64, kind, name string, cache map[string]int64) (int64, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = titleKind(kind)
	}
	cacheKey := fmt.Sprintf("%d:%s:%s", perspID, kind, name)
	if id, ok := cache[cacheKey]; ok {
		return id, nil
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM tracks WHERE project_id=? AND perspective_id=? AND kind=? AND name=?`, projectID, perspID, kind, name).Scan(&id); err == sql.ErrNoRows {
		res, err := tx.ExecContext(ctx, `INSERT INTO tracks(project_id, perspective_id, kind, name) VALUES (?, ?, ?, ?)`, projectID, perspID, kind, name)
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

func newLinkGroupID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("lg-%d", time.Now().UnixNano())
	}
	return "lg-" + hex.EncodeToString(b[:])
}

func (s *Server) enqueueClipJobs(ctx context.Context, projectID int64, clipIDs []int64) ([]int64, error) {
	if len(clipIDs) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(clipIDs))
	args := make([]any, 0, len(clipIDs)+2)
	for i, id := range clipIDs {
		placeholders[i] = "(?)"
		args = append(args, id)
	}
	args = append(args, projectID, projectID)

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
WITH requested(clip_id) AS (VALUES %s),
active_jobs AS (
  SELECT clip_id, MAX(id) AS job_id FROM ingest_jobs
  WHERE project_id=? AND state IN ('PENDING','PROCESSING') GROUP BY clip_id
)
SELECT r.clip_id, c.ingest_status, COALESCE(a.job_id, 0)
FROM requested r
JOIN clips c ON c.id=r.clip_id AND c.project_id=?
LEFT JOIN active_jobs a ON a.clip_id=r.clip_id`, strings.Join(placeholders, ",")), args...)
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
		jobID, err := s.insertPendingIngestJob(ctx, projectID, clipID)
		if err != nil {
			return nil, err
		}
		if jobID != 0 {
			jobIDs = append(jobIDs, jobID)
		}
	}
	if len(jobIDs) > 0 {
		s.ingest.Notify()
	}
	return jobIDs, nil
}

func (s *Server) insertPendingIngestJob(ctx context.Context, projectID, clipID int64) (int64, error) {
	var jobID int64
	err := s.db.QueryRowContext(ctx, `
INSERT OR IGNORE INTO ingest_jobs(project_id, clip_id, state)
VALUES (?, ?, 'PENDING') RETURNING id`, projectID, clipID).Scan(&jobID)
	if err == nil {
		return jobID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	// Race: another goroutine inserted first. Return the existing active job.
	err = s.db.QueryRowContext(ctx, `
SELECT id FROM ingest_jobs WHERE project_id=? AND clip_id=?
ORDER BY CASE WHEN state IN ('PENDING','PROCESSING') THEN 0 ELSE 1 END, id DESC
LIMIT 1`, projectID, clipID).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return jobID, err
}

func (s *Server) triggerIngest(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireProjectEditor(w, r, projectID) {
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
	if !s.requireProjectMember(w, r, projectID) {
		return
	}
	rows := s.queryRows(r.Context(), `
SELECT j.id, j.clip_id, COALESCE(c.display_name,'') AS clip_name, j.state, j.stage, j.error,
       j.progress_pct, j.progress_time_ms, j.total_duration_ms,
       j.ffmpeg_frame, j.ffmpeg_fps, j.ffmpeg_bitrate, j.ffmpeg_speed,
       j.created_at, j.started_at, j.finished_at, j.updated_at
FROM ingest_jobs j
LEFT JOIN clips c ON c.id=j.clip_id
WHERE j.project_id=?
ORDER BY CASE j.state WHEN 'PROCESSING' THEN 0 WHEN 'PENDING' THEN 1 WHEN 'FAILED' THEN 2 ELSE 3 END, j.id DESC`, projectID)
	writeJSON(w, 200, rows)
}

func (s *Server) retryClipIngest(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	clipID, ok := pathID(w, r, "clipID")
	if !ok {
		return
	}
	if !s.requireProjectEditor(w, r, projectID) {
		return
	}
	var status string
	if err := s.db.QueryRowContext(r.Context(), `SELECT ingest_status FROM clips WHERE id=? AND project_id=?`, clipID, projectID).Scan(&status); err != nil {
		writeError(w, 404, err)
		return
	}
	if status == "FAILED" {
		if _, err := s.db.ExecContext(r.Context(), `UPDATE clips SET ingest_status='PENDING', updated_at=datetime('now') WHERE id=? AND project_id=?`, clipID, projectID); err != nil {
			writeError(w, 500, err)
			return
		}
		// Mark all previous FAILED jobs for this clip as cancelled so they
		// don't keep showing in the UI after the retry succeeds.
		if _, err := s.db.ExecContext(r.Context(), `UPDATE ingest_jobs SET state='FAILED', error='superseded by retry', updated_at=datetime('now') WHERE clip_id=? AND project_id=? AND state='FAILED'`, clipID, projectID); err != nil {
			writeError(w, 500, err)
			return
		}
		status = "PENDING"
	}
	ids, err := s.enqueueClipJobs(r.Context(), projectID, []int64{clipID})
	if err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 202, map[string]any{"jobIds": ids, "ingestStatus": status})
}

func (s *Server) createPerspective(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireProjectEditor(w, r, projectID) {
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
	if !s.requireProjectEditor(w, r, projectID) {
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
	if !s.requireProjectEditor(w, r, projectID) {
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

func (s *Server) patchClips(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireProjectEditor(w, r, projectID) {
		return
	}
	var req struct {
		Updates []struct {
			ClipID           int64 `json:"clipId"`
			WallclockStartMS int64 `json:"wallclockStartMs"`
		} `json:"updates"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if len(req.Updates) == 0 {
		writeError(w, 400, errors.New("at least one clip update is required"))
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	updates := make([]map[string]any, 0, len(req.Updates))
	missing := []int64{}
	seen := map[int64]bool{}
	for _, u := range req.Updates {
		if u.ClipID <= 0 {
			writeError(w, 400, errors.New("clipId must be positive"))
			return
		}
		if seen[u.ClipID] {
			continue
		}
		seen[u.ClipID] = true
		start := u.WallclockStartMS
		if start < 0 {
			start = 0
		}
		res, err := tx.ExecContext(r.Context(), `UPDATE clips SET wallclock_start_ms=?, updated_at=datetime('now') WHERE id=? AND project_id=?`, start, u.ClipID, projectID)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			missing = append(missing, u.ClipID)
			continue
		}
		updates = append(updates, map[string]any{"clipId": u.ClipID, "wallclockStartMs": start})
	}
	if err := tx.Commit(); err != nil {
		writeError(w, 500, err)
		return
	}
	if len(updates) > 0 {
		s.broadcast(r, projectID, "clip.timeline.batch_updated", map[string]any{"updates": updates, "missingClipIds": missing})
	}
	writeJSON(w, 200, map[string]any{"updates": updates, "missingClipIds": missing})
}

// patchClip accepts wallclockStartMs, displayName, and linkGroupId independently.
// Previously, displayName was only applied when wallclockStartMs was also present —
// a bug masked by the original conditional structure.
func (s *Server) patchClip(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	clipID, ok := pathID(w, r, "clipID")
	if !ok {
		return
	}
	if !s.requireProjectEditor(w, r, projectID) {
		return
	}
	var req struct {
		WallclockStartMS *int64  `json:"wallclockStartMs"`
		DisplayName      *string `json:"displayName"`
		LinkGroupID      *string `json:"linkGroupId"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if req.WallclockStartMS == nil && req.DisplayName == nil && req.LinkGroupID == nil {
		writeError(w, 400, errors.New("nothing to update"))
		return
	}
	if req.WallclockStartMS != nil {
		if _, err := s.db.ExecContext(r.Context(), `UPDATE clips SET wallclock_start_ms=?, updated_at=datetime('now') WHERE id=? AND project_id=?`, *req.WallclockStartMS, clipID, projectID); err != nil {
			writeError(w, 500, err)
			return
		}
		s.broadcast(r, projectID, "clip.timeline.updated", map[string]any{"clipId": clipID, "wallclockStartMs": *req.WallclockStartMS})
	}
	if req.DisplayName != nil {
		if name := strings.TrimSpace(*req.DisplayName); name != "" {
			if _, err := s.db.ExecContext(r.Context(), `UPDATE clips SET display_name=?, updated_at=datetime('now') WHERE id=? AND project_id=?`, name, clipID, projectID); err != nil {
				writeError(w, 500, err)
				return
			}
		}
	}
	if req.LinkGroupID != nil {
		lg := strings.TrimSpace(*req.LinkGroupID)
		if _, err := s.db.ExecContext(r.Context(), `UPDATE clips SET link_group_id=?, updated_at=datetime('now') WHERE id=? AND project_id=?`, lg, clipID, projectID); err != nil {
			writeError(w, 500, err)
			return
		}
		s.broadcast(r, projectID, "clip.link.updated", map[string]any{"clipId": clipID, "linkGroupId": lg})
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// deleteClips: DELETE /api/projects/{projectID}/clips with {"clipIds":[...]} body.
func (s *Server) deleteClips(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireProjectEditor(w, r, projectID) {
		return
	}
	var req struct {
		ClipIDs []int64 `json:"clipIds"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	if len(req.ClipIDs) == 0 {
		writeError(w, 400, errors.New("at least one clip id is required"))
		return
	}

	seen := map[int64]bool{}
	ids := make([]int64, 0, len(req.ClipIDs))
	for _, id := range req.ClipIDs {
		if id <= 0 {
			writeError(w, 400, errors.New("clipId must be positive"))
			return
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	deleted, missing := []int64{}, []int64{}
	for _, id := range ids {
		res, err := tx.ExecContext(r.Context(), `DELETE FROM clips WHERE id=? AND project_id=?`, id, projectID)
		if err != nil {
			writeError(w, 500, err)
			return
		}
		if n, _ := res.RowsAffected(); n == 0 {
			missing = append(missing, id)
		} else {
			deleted = append(deleted, id)
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, 500, err)
		return
	}
	if len(deleted) > 0 {
		s.broadcast(r, projectID, "clip.deleted.batch", map[string]any{"clipIds": deleted, "missingClipIds": missing})
	}
	writeJSON(w, 200, map[string]any{"deletedClipIds": deleted, "missingClipIds": missing})
}

func (s *Server) hlsAssetIsReusable(ctx context.Context, hlsID int64, playlistPath string) bool {
	if playlistPath != hlsassets.PlaylistPath(hlsID) {
		return false
	}
	if _, err := s.hls.Stat(ctx, storage.ObjectRef{Adapter: s.cfg.HLSAdapter, Path: playlistPath}); err != nil {
		slog.InfoContext(ctx, "ignoring HLS database row because playlist is missing from storage", "hls_asset_id", hlsID, "playlist_path", playlistPath, "err", err)
		return false
	}
	return true
}

func (s *Server) cachedHLSURL(ctx context.Context, ref storage.ObjectRef, ttl time.Duration) (string, error) {
	key := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%d", ref.Adapter, ref.Bucket, ref.Key, ref.Path, int64(ttl.Seconds()))
	now := time.Now()
	s.urlCache.mu.Lock()
	if s.urlCache.m != nil {
		if e, ok := s.urlCache.m[key]; ok && now.Before(e.expiresAt) {
			u := e.url
			s.urlCache.mu.Unlock()
			return u, nil
		}
	}
	s.urlCache.mu.Unlock()

	u, err := s.hls.PublicOrSignedURL(ctx, ref, ttl)
	if err != nil {
		return "", err
	}
	exp := ttl - 30*time.Second
	if exp <= 0 {
		exp = ttl / 2
	}
	if exp <= 0 {
		exp = time.Minute
	}
	s.urlCache.mu.Lock()
	if s.urlCache.m == nil {
		s.urlCache.m = make(map[string]signedURLCacheEntry)
	}
	// Evict expired entries when the cache grows large.
	if len(s.urlCache.m) > 512 {
		for k, v := range s.urlCache.m {
			if now.After(v.expiresAt) {
				delete(s.urlCache.m, k)
			}
		}
	}
	s.urlCache.m[key] = signedURLCacheEntry{url: u, expiresAt: now.Add(exp)}
	s.urlCache.mu.Unlock()
	return u, nil
}

func (s *Server) createMarker(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireProjectEditor(w, r, projectID) {
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
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO markers(project_id, marker_ts_ms, author_username, label, note) VALUES (?, ?, ?, ?, ?)`, projectID, req.TsMS, p.Username, req.Label, req.Note)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	id, _ := res.LastInsertId()
	payload := map[string]any{"id": id, "marker_ts_ms": req.TsMS, "label": req.Label, "note": req.Note, "author_username": p.Username, "authorColor": p.Color}
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

func (s *Server) createRegion(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireProjectEditor(w, r, projectID) {
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
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO regions(project_id, region_start_ms, region_end_ms, author_username, label, note) VALUES (?, ?, ?, ?, ?, ?)`, projectID, req.StartMS, req.EndMS, p.Username, req.Label, req.Note)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	id, _ := res.LastInsertId()
	payload := map[string]any{"id": id, "region_start_ms": req.StartMS, "region_end_ms": req.EndMS, "label": req.Label, "note": req.Note, "author_username": p.Username, "authorColor": p.Color}
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
	if !s.requireProjectMember(w, r, projectID) {
		return
	}
	w.Header().Set("content-type", "text/plain")
	w.Header().Set("content-disposition", `attachment; filename="regions.edl"`)
	if err := exp.WriteEDL(r.Context(), s.db, w, projectID); err != nil {
		writeError(w, 500, err)
	}
}

type itemWriter func(io.Writer, []exp.Item) error

func (s *Server) withItems(w http.ResponseWriter, r *http.Request, contentType, filename string, fn itemWriter) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireProjectMember(w, r, projectID) {
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
	}
}

func (s *Server) wsProject(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireProjectMember(w, r, projectID) {
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
	ref := storage.ObjectRef{Adapter: s.cfg.HLSAdapter, Path: p}
	if info, err := s.hls.Stat(r.Context(), ref); err == nil && info.SizeBytes > 0 {
		w.Header().Set("content-length", strconv.FormatInt(info.SizeBytes, 10))
	}
	rc, err := s.hls.Open(r.Context(), ref)
	if err != nil {
		writeError(w, 404, err)
		return
	}
	defer rc.Close()
	if ct := mime.TypeByExtension(filepath.Ext(p)); ct != "" {
		w.Header().Set("content-type", ct)
	}
	switch {
	case strings.HasSuffix(p, ".m3u8"):
		w.Header().Set("content-type", hlsassets.PlaylistContentType)
		w.Header().Set("cache-control", "no-store")
	case strings.HasSuffix(p, ".ts"):
		w.Header().Set("content-type", hlsassets.SegmentContentType)
		w.Header().Set("cache-control", "public, max-age=31536000, immutable")
	default:
		w.Header().Set("cache-control", "private, max-age=300")
	}
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

func (s *Server) requireProjectEditor(w http.ResponseWriter, r *http.Request, projectID int64) bool {
	p, _ := auth.FromContext(r.Context())
	var allowed int
	err := s.db.QueryRowContext(r.Context(), `
SELECT CASE WHEN EXISTS (
  SELECT 1 FROM projects p
  LEFT JOIN project_memberships pm ON pm.project_id=p.id AND pm.username=?
  WHERE p.id=? AND (p.owner_username=? OR pm.role='editor')
) THEN 1 ELSE 0 END`, p.Username, projectID, p.Username).Scan(&allowed)
	if err != nil {
		writeError(w, 500, err)
		return false
	}
	if allowed != 1 {
		writeError(w, 403, errors.New("editor access required"))
		return false
	}
	return true
}

func (s *Server) canEditMarker(ctx context.Context, projectID, markerID int64, username string) bool {
	var allowed int
	err := s.db.QueryRowContext(ctx, `
SELECT CASE WHEN p.owner_username=? OR m.author_username=? THEN 1 ELSE 0 END
FROM markers m JOIN projects p ON p.id=m.project_id
WHERE m.id=? AND m.project_id=?`, username, username, markerID, projectID).Scan(&allowed)
	return err == nil && allowed == 1
}

func (s *Server) canEditRegion(ctx context.Context, projectID, regionID int64, username string) bool {
	var allowed int
	err := s.db.QueryRowContext(ctx, `
SELECT CASE WHEN p.owner_username=? OR r.author_username=? THEN 1 ELSE 0 END
FROM regions r JOIN projects p ON p.id=r.project_id
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
			if b, ok := vals[i].([]byte); ok {
				m[c] = string(b)
			} else {
				m[c] = vals[i]
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
	w.Header().Set("cache-control", "no-store")
	w.Header().Set("pragma", "no-cache")
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

// --- HTTP logging middleware ---

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
func (w *loggingResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &loggingResponseWriter{ResponseWriter: w}
		next.ServeHTTP(lw, r)
		status := lw.status
		if status == 0 {
			status = http.StatusOK
		}
		slog.InfoContext(r.Context(), "http request", "method", r.Method, "uri", r.URL.RequestURI(), "status", status, "bytes", lw.bytes, "duration_ms", time.Since(start).Milliseconds())
	})
}
