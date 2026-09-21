package dashboard

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

// uiAssets is the three-tier admin UI of docs/mvp.md M6, embedded in the
// binary: plain HTML and JS, no build step and no CDN, so a dashboard is a
// single file that works with no network at all.
//
//go:embed ui
var uiAssets embed.FS

// uiFS is the embedded directory rooted at `ui/`, so the page is served at
// `/` and not at `/ui/`.
func uiFS() fs.FS {
	sub, err := fs.Sub(uiAssets, "ui")
	if err != nil {
		// Unreachable: the directory is embedded at compile time, so a
		// failure here would mean this binary was built without it.
		panic("dashboard: the embedded ui directory is missing: " + err.Error())
	}
	return sub
}

// etagFileServer serves the embedded UI with a content ETag. An embed.FS has
// no modification times, so http.FileServerFS alone sends neither
// Last-Modified nor ETag and a browser keeps a stale app.js across a binary
// upgrade until the reader forces a reload (measured 2026-09-21, task 29).
// The hash is of the file's bytes, so a rebuilt binary with a changed asset
// invalidates exactly that asset and nothing else.
func etagFileServer(root fs.FS) http.Handler {
	files := http.FileServerFS(root)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if name == "" {
			name = "index.html"
		}
		if f, err := root.Open(name); err == nil {
			sum := sha256.New()
			_, _ = io.Copy(sum, f)
			_ = f.Close()
			tag := `"` + hex.EncodeToString(sum.Sum(nil))[:16] + `"`
			if r.Header.Get("If-None-Match") == tag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", tag)
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
