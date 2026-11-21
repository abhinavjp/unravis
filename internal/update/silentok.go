package update

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// SilentNoUAC says whether an update of the install in dir (the folder that holds unravis.exe) can be applied with no
// administrator prompt: a just-for-me install with no Blackbox. Anything else needs a Windows approval that nobody is there to give
// during an automatic update, so the person is asked instead. It reads setup-state.json; a missing or unreadable file says no.
func SilentNoUAC(dir string) bool {
	b, err := os.ReadFile(filepath.Join(dir, "setup-state.json"))
	if err != nil || len(b) > maxStateFile*4 {
		return false
	}
	var s struct {
		Scope    string          `json:"scope"`
		Blackbox json.RawMessage `json:"blackbox"`
	}
	if json.Unmarshal(b, &s) != nil {
		return false
	}
	return s.Scope == "user" && (len(s.Blackbox) == 0 || string(s.Blackbox) == "null")
}
