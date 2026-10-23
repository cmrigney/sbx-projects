// Package webui serves the React app in web/, built into dist/, so a phone's
// browser can do what the iOS app does against the same API.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the built app. Paths that aren't files get index.html, and
// before the app is built every path says how to build it.
func Handler() http.Handler {
	root, _ := fs.Sub(dist, "dist")
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
		if _, err := fs.Stat(root, "index.html"); err != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("The web app isn't built into this binary. Run task web, then build sbx-projects again.\n"))
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "index.html" {
			serveIndex(w, r, root)
			return
		}
		if _, err := fs.Stat(root, name); err != nil {
			serveIndex(w, r, root)
			return
		}
		// Vite names the bundles by their content, so they never change.
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, root fs.FS) {
	// Always fresh, so a new build's bundles are picked up on reload.
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, root, "index.html")
}
