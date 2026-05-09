package httpapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/example/multitrack-drifter/internal/auth"
)

type projectMember struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Color       string `json:"color"`
	Role        string `json:"role"`
	CreatedAt   string `json:"createdAt"`
}

func (s *Server) listProjectMembers(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireProjectMember(w, r, projectID) {
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `
SELECT username, display_name, color, role, created_at
FROM (
  SELECT u.username, u.display_name, u.color, 'owner' AS role, p.created_at AS created_at, 0 AS sort_order
  FROM projects p
  JOIN users u ON u.username = p.owner_username
  WHERE p.id = ?
  UNION ALL
  SELECT u.username, u.display_name, u.color, pm.role, pm.created_at,
    CASE pm.role WHEN 'editor' THEN 1 WHEN 'member' THEN 1 WHEN 'viewer' THEN 2 ELSE 3 END AS sort_order
  FROM project_memberships pm
  JOIN projects p ON p.id = pm.project_id
  JOIN users u ON u.username = pm.username
  WHERE pm.project_id = ? AND pm.username <> p.owner_username
)
ORDER BY sort_order, username`, projectID, projectID)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	defer rows.Close()

	members := []projectMember{}
	for rows.Next() {
		var m projectMember
		if err := rows.Scan(&m.Username, &m.DisplayName, &m.Color, &m.Role, &m.CreatedAt); err != nil {
			writeError(w, 500, err)
			return
		}
		members = append(members, m)
	}
	if err := rows.Err(); err != nil {
		writeError(w, 500, err)
		return
	}
	writeJSON(w, 200, members)
}

func (s *Server) addProjectMember(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireOwner(w, r, projectID) {
		return
	}
	var req struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	username := strings.TrimSpace(req.Username)
	role := normalizeMemberRole(req.Role)
	if username == "" {
		writeError(w, 400, errors.New("username required"))
		return
	}
	if role == "" {
		writeError(w, 400, errors.New("role must be viewer or editor"))
		return
	}
	if s.isProjectOwner(r, projectID, username) {
		writeError(w, 400, errors.New("project owner is already a member"))
		return
	}
	if !s.userExists(r, username) {
		writeError(w, 404, errors.New("user not found; ask them to sign in once, then add them here"))
		return
	}
	_, err := s.db.ExecContext(r.Context(), `
INSERT INTO project_memberships(project_id, username, role)
VALUES (?, ?, ?)
ON CONFLICT(project_id, username) DO UPDATE SET role=excluded.role`, projectID, username, role)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	s.broadcast(r, projectID, "project.member.added", map[string]any{"username": username, "role": role})
	writeJSON(w, 201, map[string]any{"username": username, "role": role})
}

func (s *Server) patchProjectMember(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireOwner(w, r, projectID) {
		return
	}
	username := strings.TrimSpace(r.PathValue("username"))
	if username == "" {
		writeError(w, 400, errors.New("username required"))
		return
	}
	if s.isProjectOwner(r, projectID, username) {
		writeError(w, 400, errors.New("owner role cannot be changed"))
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, 400, err)
		return
	}
	role := normalizeMemberRole(req.Role)
	if role == "" {
		writeError(w, 400, errors.New("role must be viewer or editor"))
		return
	}
	res, err := s.db.ExecContext(r.Context(), `UPDATE project_memberships SET role=? WHERE project_id=? AND username=?`, role, projectID, username)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, 404, errors.New("project member not found"))
		return
	}
	s.broadcast(r, projectID, "project.member.updated", map[string]any{"username": username, "role": role})
	writeJSON(w, 200, map[string]any{"username": username, "role": role})
}

func (s *Server) deleteProjectMember(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathID(w, r, "projectID")
	if !ok {
		return
	}
	if !s.requireOwner(w, r, projectID) {
		return
	}
	username := strings.TrimSpace(r.PathValue("username"))
	if username == "" {
		writeError(w, 400, errors.New("username required"))
		return
	}
	if s.isProjectOwner(r, projectID, username) {
		writeError(w, 400, errors.New("project owner cannot be removed"))
		return
	}
	res, err := s.db.ExecContext(r.Context(), `DELETE FROM project_memberships WHERE project_id=? AND username=?`, projectID, username)
	if err != nil {
		writeError(w, 500, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, 404, errors.New("project member not found"))
		return
	}
	s.broadcast(r, projectID, "project.member.removed", map[string]any{"username": username})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func normalizeMemberRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "", "editor", "member":
		return "editor"
	case "viewer":
		return "viewer"
	default:
		return ""
	}
}

func (s *Server) userExists(r *http.Request, username string) bool {
	var exists int
	err := s.db.QueryRowContext(r.Context(), `SELECT 1 FROM users WHERE username=?`, username).Scan(&exists)
	return err == nil && exists == 1
}

func (s *Server) isProjectOwner(r *http.Request, projectID int64, username string) bool {
	var owner string
	err := s.db.QueryRowContext(r.Context(), `SELECT owner_username FROM projects WHERE id=?`, projectID).Scan(&owner)
	return err == nil && owner == username
}

func (s *Server) requireProjectMember(w http.ResponseWriter, r *http.Request, projectID int64) bool {
	p, _ := auth.FromContext(r.Context())
	var allowed int
	err := s.db.QueryRowContext(r.Context(), `
SELECT CASE WHEN EXISTS (
  SELECT 1
  FROM projects p
  LEFT JOIN project_memberships pm ON pm.project_id=p.id AND pm.username=?
  WHERE p.id=?
    AND (p.owner_username=? OR pm.role IN ('owner','editor','member','viewer'))
) THEN 1 ELSE 0 END`, p.Username, projectID, p.Username).Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, 404, errors.New("project not found"))
		return false
	}
	if err != nil {
		writeError(w, 500, err)
		return false
	}
	if allowed != 1 {
		writeError(w, 403, errors.New("project membership required"))
		return false
	}
	return true
}
