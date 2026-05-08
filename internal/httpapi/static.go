package httpapi

import (
	"io/fs"
	"net/http"
	"net/url"
	"strings"

	"github.com/example/multitrack-drifter/internal/webdist"
)

func StaticHandler() http.Handler {
	dist, err := fs.Sub(webdist.FS, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/ws/") || strings.HasPrefix(r.URL.Path, "/media/") {
			http.NotFound(w, r)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if f, err := dist.Open(path); err == nil {
			_ = f.Close()
			files.ServeHTTP(w, r)
			return
		}
		r2 := new(http.Request)
		*r2 = *r
		r2.URL = newURL(r.URL.String(), "/index.html")
		files.ServeHTTP(w, r2)
	})
}

func newURL(raw, path string) *url.URL { u, _ := url.Parse(raw); u.Path = path; return u }
