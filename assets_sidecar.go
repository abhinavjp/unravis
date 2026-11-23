//go:build tauri_sidecar

package explorer

import (
	"embed"
	"io/fs"
)

// emptyAssets holds no files: a sidecar built with the tauri_sidecar tag does not embed the dashboard (BL-422).
var emptyAssets embed.FS

// Assets returns an empty filesystem in a sidecar build. The desktop shell brings its own copy of the UI (BL-317);
// no program in this repository ships this build yet (docs/desktop/spec/07-performance.md, decision D5), the tag only keeps it compiling.
func Assets() fs.FS { return emptyAssets }
