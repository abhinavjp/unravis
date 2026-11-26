package update

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// servicesInstalled is a variable so a test can say what is installed: the answer is about the PC the test runs on.
var servicesInstalled = anyServiceInstalled

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
	if s.Scope != "user" || (len(s.Blackbox) != 0 && string(s.Blackbox) != "null") {
		return false
	}
	// Setup also asks for approval when a key keeper or Blackbox service is left from an earlier install for everyone; it decides
	// the same way, from the service list (no administrator rights are needed to ask).
	return !servicesInstalled("UnravisKeeper", "UnravisBlackbox")
}
