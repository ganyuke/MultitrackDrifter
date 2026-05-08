package auth

import (
	"context"
	"net/http"
)

type contextKey string

const PrincipalKey contextKey = "principal"

func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := s.PrincipalFromRequest(r)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), PrincipalKey, p)))
	})
}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(PrincipalKey).(Principal)
	return p, ok
}
