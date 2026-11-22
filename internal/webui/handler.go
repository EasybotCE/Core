package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"easybot/internal/config"
)

// Package assets are populated by build.mjs before Go compilation. The checked
// in placeholder keeps ordinary source tests buildable without frontend output.
//
//go:embed dist
var embeddedAssets embed.FS

// New composes the UI and API on one listener. Built assets are embedded into
// the executable; EASYBOT_WEB_DIR is an optional development override.
func New(api http.Handler) http.Handler {
	if override := os.Getenv("EASYBOT_WEB_DIR"); override != "" {
		if !filepath.IsAbs(override) {
			if dir, err := config.BaseDirectory(); err == nil {
				override = filepath.Join(dir, override)
			}
		}
		return NewWithDirectory(api, override)
	}
	assets, err := fs.Sub(embeddedAssets, "dist")
	if err != nil {
		return unavailable(api)
	}
	return NewWithFS(api, assets)
}

func NewWithDirectory(api http.Handler, directory string) http.Handler {
	if directory == "" {
		return unavailable(api)
	}
	return newHandler(api, http.Dir(directory), func() bool {
		info, err := os.Stat(filepath.Join(directory, "index.html"))
		return err == nil && !info.IsDir()
	})
}

func NewWithFS(api http.Handler, assets fs.FS) http.Handler {
	if assets == nil {
		return unavailable(api)
	}
	return newHandler(api, http.FS(assets), func() bool {
		info, err := fs.Stat(assets, "index.html")
		return err == nil && !info.IsDir()
	})
}

func unavailable(api http.Handler) http.Handler {
	return newHandler(api, nil, func() bool { return false })
}

func newHandler(api http.Handler, files http.FileSystem, available func() bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		if files == nil || !available() {
			http.Error(w, "WebUI resources unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.FileServer(files).ServeHTTP(w, r)
	})
}
