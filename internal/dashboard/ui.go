package dashboard

import (
	"embed"
	"io/fs"
)

// uiAssets is the three-tier admin UI of docs/mvp.md M6, embedded in the
// binary: plain HTML and JS, no build step and no CDN, so a dashboard is a
// single file that works with no network at all.
//
// Task 28 ships only the placeholder page - enough to prove the mount and
// the wiring - and task 29 owns everything else under this directory.
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
