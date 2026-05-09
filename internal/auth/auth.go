package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	ldap "github.com/go-ldap/ldap/v3"

	"github.com/example/multitrack-drifter/internal/config"
)

const CookieName = "drifter_session"

var Palette = []string{"#0072B2", "#D55E00", "#009E73", "#CC79A7", "#F0E442", "#56B4E9", "#E69F00", "#8F00FF"}

type Principal struct {
	Username          string `json:"username"`
	DisplayName       string `json:"displayName"`
	Color             string `json:"color"`
	CanCreateProjects bool   `json:"canCreateProjects"`
	ExpiresAt         string `json:"expiresAt"`
}

type Service struct {
	db  *sql.DB
	cfg config.Config
}

func New(db *sql.DB, cfg config.Config) *Service { return &Service{db: db, cfg: cfg} }

func (s *Service) Login(ctx context.Context, username, password string) (Principal, string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return Principal{}, "", errors.New("username required")
	}
	var display string
	var canCreate bool
	if s.cfg.DevAuthEnabled {
		display = username
		canCreate = true
	} else {
		var err error
		display, canCreate, err = s.ldapAuthenticate(username, password)
		if err != nil {
			return Principal{}, "", err
		}
	}
	color := colorFor(username)
	// Preserve any user-chosen color; only set default on first login.
	var existingColor string
	_ = s.db.QueryRowContext(ctx, `SELECT color FROM users WHERE username=?`, username).Scan(&existingColor)
	if existingColor != "" {
		color = existingColor
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO users(username, display_name, color, can_create_projects, updated_at)
VALUES (?, ?, ?, ?, datetime('now'))
ON CONFLICT(username) DO UPDATE SET display_name=excluded.display_name, can_create_projects=excluded.can_create_projects, updated_at=datetime('now')`,
		username, display, color, boolInt(canCreate))
	if err != nil {
		return Principal{}, "", err
	}
	principal, token, err := s.createSession(ctx, username)
	if err != nil {
		return Principal{}, "", err
	}
	principal.DisplayName = display
	principal.Color = color
	principal.CanCreateProjects = canCreate
	return principal, token, nil
}

func (s *Service) ldapAuthenticate(username, password string) (string, bool, error) {
	if password == "" {
		return "", false, errors.New("invalid credentials")
	}
	if s.cfg.LDAP.URL == "" {
		return "", false, errors.New("LDAP_URL not configured")
	}
	conn, err := ldap.DialURL(s.cfg.LDAP.URL)
	if err != nil {
		return "", false, err
	}
	defer conn.Close()
	if s.cfg.LDAP.BindDN != "" {
		if err := conn.Bind(s.cfg.LDAP.BindDN, s.cfg.LDAP.BindPassword); err != nil {
			return "", false, err
		}
	}
	filter := fmt.Sprintf(s.cfg.LDAP.UserFilter, ldap.EscapeFilter(username))
	search := ldap.NewSearchRequest(s.cfg.LDAP.UserBaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 1, 0, false, filter, []string{"dn", "cn", "displayName", "memberOf"}, nil)
	res, err := conn.Search(search)
	if err != nil {
		return "", false, err
	}
	if len(res.Entries) != 1 {
		return "", false, errors.New("invalid credentials")
	}
	entry := res.Entries[0]
	if err := conn.Bind(entry.DN, password); err != nil {
		return "", false, errors.New("invalid credentials")
	}
	display := entry.GetAttributeValue("displayName")
	if display == "" {
		display = entry.GetAttributeValue("cn")
	}
	if display == "" {
		display = username
	}
	groups := append([]string{}, entry.GetAttributeValues("memberOf")...)
	canCreate := containsAnyDN(groups, s.cfg.LDAP.CreatorGroups)
	return display, canCreate, nil
}

// createSession inserts a new session row. User metadata is NOT stored in sessions —
// it is always joined from the users table at lookup time to stay fresh.
func (s *Service) createSession(ctx context.Context, username string) (Principal, string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return Principal{}, "", err
	}
	token := hex.EncodeToString(bytes)
	expires := time.Now().Add(s.cfg.SessionTTL).UTC()
	hash := s.tokenHash(token)
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(token_hash, username, expires_at) VALUES (?, ?, ?)`, hash, username, expires.Format(time.RFC3339))
	if err != nil {
		return Principal{}, "", err
	}
	return Principal{Username: username, ExpiresAt: expires.Format(time.RFC3339)}, token, nil
}

func (s *Service) PrincipalFromRequest(r *http.Request) (Principal, error) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return Principal{}, err
	}
	return s.PrincipalFromToken(r.Context(), cookie.Value)
}

// PrincipalFromToken loads the session and joins the current user row so we always
// return live display_name/color/can_create_projects rather than a stale cached copy.
func (s *Service) PrincipalFromToken(ctx context.Context, token string) (Principal, error) {
	var p Principal
	var canInt int
	err := s.db.QueryRowContext(ctx, `
SELECT s.username, u.display_name, u.color, u.can_create_projects, s.expires_at
FROM sessions s
JOIN users u ON u.username = s.username
WHERE s.token_hash = ?`, s.tokenHash(token)).Scan(&p.Username, &p.DisplayName, &p.Color, &canInt, &p.ExpiresAt)
	if err != nil {
		return Principal{}, err
	}
	p.CanCreateProjects = canInt == 1
	exp, err := time.Parse(time.RFC3339, p.ExpiresAt)
	if err != nil || time.Now().After(exp) {
		_ = s.Logout(ctx, token)
		return Principal{}, errors.New("session expired")
	}
	return p, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, s.tokenHash(token))
	return err
}

func (s *Service) Refresh(ctx context.Context, token string) (Principal, error) {
	p, err := s.PrincipalFromToken(ctx, token)
	if err != nil {
		return Principal{}, err
	}
	return s.refreshPrincipal(ctx, token, p)
}

func (s *Service) RefreshIfNeeded(ctx context.Context, token string, p Principal) (Principal, error) {
	expiresAt, err := time.Parse(time.RFC3339, p.ExpiresAt)
	if err != nil {
		return s.Refresh(ctx, token)
	}
	if time.Until(expiresAt) > s.cfg.SessionTTL/5 {
		return p, nil
	}
	return s.refreshPrincipal(ctx, token, p)
}

func (s *Service) refreshPrincipal(ctx context.Context, token string, p Principal) (Principal, error) {
	expires := time.Now().Add(s.cfg.SessionTTL).UTC().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE token_hash = ?`, expires, s.tokenHash(token))
	if err != nil {
		return Principal{}, err
	}
	p.ExpiresAt = expires
	return p, nil
}

func (s *Service) SetColor(ctx context.Context, username, color string) error {
	canonical, ok := CanonicalColor(color)
	if !ok {
		return errors.New("color not in accessible palette")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE users SET color = ?, updated_at = datetime('now') WHERE username = ?`, canonical, username)
	return err
}

func (s *Service) WriteSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/", HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: int(s.cfg.SessionTTL.Seconds())})
}

func (s *Service) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.cfg.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

func (s *Service) tokenHash(token string) string {
	mac := hmac.New(sha256.New, []byte(s.cfg.CookieSecret))
	_, _ = mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil))
}

func colorFor(username string) string {
	h := sha256.Sum256([]byte(username))
	return Palette[int(h[0])%len(Palette)]
}

func CanonicalColor(color string) (string, bool) {
	for _, c := range Palette {
		if strings.EqualFold(c, color) {
			return c, true
		}
	}
	return "", false
}

func containsAnyDN(haystack, needles []string) bool {
	for _, h := range haystack {
		for _, n := range needles {
			if strings.EqualFold(strings.TrimSpace(h), strings.TrimSpace(n)) {
				return true
			}
		}
	}
	return false
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
