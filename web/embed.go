// Package web holds the built UI, embedded into the kayly binary. Build it with
// `npm run build` in this directory before `go build`; without a build the daemon serves
// the API only.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// UI returns the built UI, or nil when dist holds no index.html.
func UI() fs.FS {
	sub, _ := fs.Sub(dist, "dist")
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
