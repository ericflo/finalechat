// Package webassets embeds the built progressive web app. The dist directory
// is produced by `npm run build` in web/ and is empty in a fresh checkout.
package webassets

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built app rooted at dist/.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// Built reports whether an index.html exists, i.e. the frontend was built.
func Built() bool {
	_, err := fs.Stat(FS(), "index.html")
	return err == nil
}
