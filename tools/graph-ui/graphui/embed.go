package graphui

import (
	"embed"
	"io/fs"
)

// distFS embeds the real, built frontend (npm run build, ui/vite.config.ts's own outDir points
// here directly - see that file's own comment) - kata cycle 52's own real point: this package is
// self-contained, no loose directory path to keep in sync with wherever a caller's checkout
// happens to live.
//
//go:embed all:dist
var distFS embed.FS

// EmbeddedUI returns the real built frontend, re-rooted so "index.html" resolves directly - the
// same shape os.DirFS(dir) already has, so NewMux's own spaHandler needs no special-casing between
// the embedded case and a real disk directory (dev override via -ui).
func EmbeddedUI() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// A real build-time invariant, not a runtime possibility: dist/ is embedded directly above
		// and always exists whenever this package itself compiles.
		panic(err)
	}
	return sub
}
