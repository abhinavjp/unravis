package clicore

import (
	"path/filepath"
	"strings"
)

// InstallDir is the install folder for the path of this program: the thin command line sits in `<install>\bin`, the app program in
// `<install>`. One rule for the attach, the doctor and the legacy-option message (spec D5).
func InstallDir(exe string) string {
	dir := filepath.Dir(exe)
	if strings.EqualFold(filepath.Base(dir), "bin") {
		return filepath.Dir(dir)
	}
	return dir
}
