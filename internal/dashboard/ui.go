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

// uiAssets is the three-tier admin UI of docs/mvp.md M6, and Mate Office
// under ui/office/ (docs/dashboard.md section 11), embedded in the binary:
// plain HTML and JS, no build step and no CDN, so a dashboard is a single
// file that works with no network at all.
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

// fontLicenses are the SIL Open Font License texts of the three typefaces
// Mate Office embeds under ui/office/fonts (Geist, Geist Mono, Bricolage
// Grotesque). The OFL lets the fonts ship inside this binary on condition
// that the licence ships with them, so it is embedded too and served at
// /office/fonts/licenses/. It lives outside ui/ because the licence text
// names its own web addresses, and every file under ui/ is held to "no
// off-origin URL at all" by TestUIMakesNoOffOriginRequest.
//
//go:embed fontlicenses
var fontLicenses embed.FS

func fontLicensesFS() fs.FS {
	sub, err := fs.Sub(fontLicenses, "fontlicenses")
	if err != nil {
		panic("dashboard: the embedded font licences are missing: " + err.Error())
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
		if name == "" || strings.HasSuffix(name, "/") {
			// A directory is served as its index.html (`/`, `/office/`),
			// and that is the file the tag has to be the hash of.
			name += "index.html"
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
