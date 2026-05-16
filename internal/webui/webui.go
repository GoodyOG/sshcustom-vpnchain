// Package webui serves the SSHCustom-VPNChain dashboard.
//
// The HTML lives in two places:
//
//  1. Embedded in the binary via embed.FS (this package's index.html). This
//     is the always-available fallback. A fresh install or a botched module
//     copy still gets a working dashboard.
//
//  2. On disk at <work_dir>/webroot/index.html. If present and non-empty,
//     it wins. This lets developers hot-edit the dashboard without
//     rebuilding the Go binary, and it lets users override the bundled UI
//     if they want.
//
// Build process: the build script copies src/module/webroot/index.html into
// internal/webui/index.html before `go build` so the embed picks up the
// canonical UI. There is exactly one source of truth for the HTML.
package webui

import (
	_ "embed"
	"net/http"
	"os"
	"path/filepath"
)

// indexHTML is the dashboard page baked into the binary.
//
//go:embed index.html
var indexHTML []byte

// faviconSVG is the dashboard tab icon, served at /favicon.svg.
//
//go:embed favicon.svg
var faviconSVG []byte

// Handler returns an http.Handler that serves the dashboard. It prefers the
// on-disk file at workDir/webroot/index.html and falls back to the embedded
// copy. Errors reading the disk file (permission denied, missing) silently
// fall through to embedded; this is intentional — the dashboard must always
// render so the user can at least see the daemon is alive.
func Handler(workDir string) http.Handler {
	diskPath := filepath.Join(workDir, "webroot", "index.html")
	faviconPath := filepath.Join(workDir, "webroot", "favicon.svg")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Favicon: same disk-first / embedded-fallback strategy.
		if r.URL.Path == "/favicon.svg" || r.URL.Path == "/favicon.ico" {
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Header().Set("Cache-Control", "public, max-age=86400")
			if b, err := os.ReadFile(faviconPath); err == nil && len(b) > 0 {
				_, _ = w.Write(b)
				return
			}
			_, _ = w.Write(faviconSVG)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// Disk override wins. Treat empty-file as "use embedded" because a
		// truncated webroot/index.html during install would otherwise serve
		// a blank page.
		if b, err := os.ReadFile(diskPath); err == nil && len(b) > 0 {
			_, _ = w.Write(b)
			return
		}
		_, _ = w.Write(indexHTML)
	})
}

// EmbeddedHTML returns the embedded dashboard bytes. Mostly useful for tests
// that want to assert the binary actually contains a non-trivial HTML page.
func EmbeddedHTML() []byte {
	return indexHTML
}
