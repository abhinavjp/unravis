package explorer

import (
	"embed"
	"io/fs"
)

//go:embed web/dist
var embedded embed.FS

// Assets returns the compiled dashboard filesystem embedded into the executable.
func Assets() fs.FS {
	assets, err := fs.Sub(embedded, "web/dist")
	if err != nil {
		panic("embedded dashboard assets are unavailable")
	}
	return assets
}
