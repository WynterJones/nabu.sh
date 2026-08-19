package webassets

import (
	"embed"
	"io/fs"
)

// The `all:` prefix keeps the placeholder inside dist/ eligible, since embed
// skips files beginning with a dot by default. Without it the package fails to
// compile on a clean checkout, before the frontend has been built. A real
// build always populates dist/ first, and `nabu doctor` reports an empty one.
//
//go:embed all:dist
var embedded embed.FS

func FS() fs.FS {
	assets, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return assets
}
