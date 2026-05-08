package export

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/example/multitrack-drifter/internal/timeline"
)

type Item struct {
	Type               string `json:"type"`
	WallclockStartMS   int64  `json:"wallclock_start_ms"`
	WallclockEndMS     int64  `json:"wallclock_end_ms"`
	StartDisplay       string `json:"wallclock_start_display"`
	EndDisplay         string `json:"wallclock_end_display"`
	Author             string `json:"author"`
	Label              string `json:"label"`
	Note               string `json:"note"`
	ActivePerspectives string `json:"active_perspectives"`
	ActiveTracks       string `json:"active_tracks"`
}

func Items(ctx context.Context, db *sql.DB, projectID int64) ([]Item, error) {
	rows, err := db.QueryContext(ctx, `
SELECT 'marker', marker_ts_ms, marker_ts_ms, author_username, label, COALESCE(note,'') FROM markers WHERE project_id=?
UNION ALL
SELECT 'region', region_start_ms, region_end_ms, author_username, label, COALESCE(note,'') FROM regions WHERE project_id=?
ORDER BY 2`, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Item
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.Type, &it.WallclockStartMS, &it.WallclockEndMS, &it.Author, &it.Label, &it.Note); err != nil {
			return nil, err
		}
		it.StartDisplay = timeline.FormatMS(it.WallclockStartMS)
		it.EndDisplay = timeline.FormatMS(it.WallclockEndMS)
		it.ActivePerspectives, it.ActiveTracks = activeAt(ctx, db, projectID, it.WallclockStartMS, it.WallclockEndMS)
		out = append(out, it)
	}
	return out, rows.Err()
}

func WriteCSV(w io.Writer, items []Item) error {
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"type", "wallclock_start_ms", "wallclock_end_ms", "wallclock_start_display", "wallclock_end_display", "author", "label", "note", "active_perspectives", "active_tracks"})
	for _, it := range items {
		_ = cw.Write([]string{it.Type, fmt.Sprint(it.WallclockStartMS), fmt.Sprint(it.WallclockEndMS), it.StartDisplay, it.EndDisplay, it.Author, it.Label, it.Note, it.ActivePerspectives, it.ActiveTracks})
	}
	cw.Flush()
	return cw.Error()
}

func WriteJSON(w io.Writer, items []Item) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(items)
}

func WriteMarkdown(w io.Writer, items []Item) error {
	_, _ = fmt.Fprintln(w, "# Project Marker Export")
	fmt.Fprintln(w)
	for _, it := range items {
		if it.Type == "region" {
			_, _ = fmt.Fprintf(w, "## %s - %s — %s\n\n", it.StartDisplay, it.EndDisplay, it.Label)
		} else {
			_, _ = fmt.Fprintf(w, "## %s - %s\n\n", it.StartDisplay, it.Label)
		}
		_, _ = fmt.Fprintf(w, "Author: %s  \nType: %s  \nActive perspectives: %s  \nActive tracks: %s\n\n%s\n\n", it.Author, it.Type, it.ActivePerspectives, it.ActiveTracks, it.Note)
	}
	return nil
}

func WriteEDL(ctx context.Context, db *sql.DB, w io.Writer, projectID int64) error {
	_, _ = fmt.Fprintln(w, "TITLE: MULTITRACK DRIFTER REGIONS")
	_, _ = fmt.Fprintln(w, "FCM: NON-DROP FRAME")
	fmt.Fprintln(w)
	rows, err := db.QueryContext(ctx, `
SELECT r.region_start_ms, r.region_end_ms, r.label, COALESCE(c.fps_num,30), COALESCE(c.fps_den,1), c.display_name
FROM regions r
LEFT JOIN clips c ON c.project_id=r.project_id AND r.region_start_ms >= c.wallclock_start_ms AND r.region_start_ms <= c.wallclock_start_ms + c.duration_ms AND c.media_kind='video'
WHERE r.project_id=? ORDER BY r.region_start_ms`, projectID)
	if err != nil {
		return err
	}
	defer rows.Close()
	n := 1
	for rows.Next() {
		var start, end, fpsN, fpsD int64
		var label, clip string
		if err := rows.Scan(&start, &end, &label, &fpsN, &fpsD, &clip); err != nil {
			return err
		}
		srcIn := timeline.TimecodeFromMS(0, fpsN, fpsD)
		srcOut := timeline.TimecodeFromMS(end-start, fpsN, fpsD)
		recIn := timeline.TimecodeFromMS(start, fpsN, fpsD)
		recOut := timeline.TimecodeFromMS(end, fpsN, fpsD)
		_, _ = fmt.Fprintf(w, "%03d  AX       V     C        %s %s %s %s\n", n, srcIn, srcOut, recIn, recOut)
		_, _ = fmt.Fprintf(w, "* FROM CLIP NAME: %s\n", safeEDL(clip))
		_, _ = fmt.Fprintf(w, "* COMMENT: %s\n\n", safeEDL(label))
		n++
	}
	return rows.Err()
}

func activeAt(ctx context.Context, db *sql.DB, projectID, start, end int64) (string, string) {
	rows, err := db.QueryContext(ctx, `
SELECT DISTINCT p.name, t.name FROM clips c JOIN perspectives p ON p.id=c.perspective_id JOIN tracks t ON t.id=c.track_id
WHERE c.project_id=? AND c.wallclock_start_ms <= ? AND c.wallclock_start_ms + c.duration_ms >= ?
ORDER BY p.name, t.name`, projectID, end, start)
	if err != nil {
		return "", ""
	}
	defer rows.Close()
	pmap, tmap := map[string]bool{}, map[string]bool{}
	for rows.Next() {
		var p, t string
		_ = rows.Scan(&p, &t)
		pmap[p] = true
		tmap[t] = true
	}
	return joinKeys(pmap), joinKeys(tmap)
}

func joinKeys(m map[string]bool) string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return strings.Join(out, ", ")
}

func safeEDL(s string) string { return strings.ReplaceAll(s, "\n", " ") }
