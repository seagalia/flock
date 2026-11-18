// Package webui embeds the dashboard that `make webui` builds in dist/app, the controller serves it at /dashboard.
package webui

import (
	"embed"
	"io/fs"
)

// dist only holds .gitkeep in git: go builds work without node, the dashboard is then not built
//
//go:embed all:dist
var dist embed.FS

const browserDir = "dist/app/browser"

// Browser gives the files of the built dashboard, it has no index.html when the dashboard is not built.
func Browser() fs.FS {
	files, err := fs.Sub(dist, browserDir)
	if err != nil {
		// fs.Sub only fails on an invalid path, browserDir is a valid one
		panic(err)
	}
	return files
}

// IsBuilt tells if the binary embeds a built dashboard
func IsBuilt() bool {
	_, err := fs.Stat(Browser(), "index.html")
	return err == nil
}
