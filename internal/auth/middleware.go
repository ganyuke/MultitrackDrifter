package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

type contextKey string

const PrincipalKey contextKey = "principal"

// Middleware authenticates the session and, for state-changing requests,
// validates the Origin header to prevent CSRF attacks.
//
// Why Origin-check rather than a token: this API only accepts
// application/json bodies. Browsers cannot send application/json
// cross-origin without a preflight, and preflights fail because CORS is
// not configured. The Origin check is a belt-and-suspenders guard for
// any request with side effects (non-GET/HEAD/OPTIONS).
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CSRF: reject state-changing requests whose Origin doesn't match
		// the configured app base URL. WebSocket upgrades are excluded
		// because the browser already enforces origin for WS.
		if !isSafeMethod(r.Method) && !isWSUpgrade(r) {
			if err := s.checkOrigin(r); err != nil {
				http.Error(w, "forbidden: "+err.Error(), http.StatusForbidden)
				return
			}
		}

		p, err := s.PrincipalFromRequest(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), PrincipalKey, p)))
	})
}

func (s *Service) checkOrigin(r *http.Request) error {
	// If no AppBaseURL is configured we can't validate — skip.
	if s.cfg.AppBaseURL == "" {
		return nil
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Same-origin requests from non-browser clients (curl, server-to-server)
		// have no Origin. Allow them; real browsers always send Origin on
		// cross-origin requests.
		return nil
	}
	allowed := strings.TrimRight(s.cfg.AppBaseURL, "/")
	if !strings.EqualFold(strings.TrimRight(origin, "/"), allowed) {
		return errors.New("origin not allowed")
	}
	return nil
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

func isWSUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(PrincipalKey).(Principal)
	return p, ok
}
