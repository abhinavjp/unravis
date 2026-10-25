package discover

// Wording for the host detector's sources that wording.go does not list yet. The
// nettrace keys are new here. The controller moves them into wording.go at merge
// and regenerates the golden file; until then fake.AssertFindingsValid cannot
// resolve them, so the host tests check them through hostWordingText.

// hostShows says what a host source records. Keys are "discover.<source>.shows".
var hostShows = map[string]string{
	"discover.nettrace.shows": "It holds a binary trace of .NET runtime and CPU events for the app process.",
}

// hostHow gives the exact step for a host source Unravis does not read directly.
// Keys are "discover.<source>.how".
var hostHow = map[string]string{
	"discover.nettrace.how": "Unravis cannot read this file. Run dotnet-counters collect with the csv format, then import the CSV file.",
}

func init() {
	for k, v := range hostShows {
		shows[k] = v
	}
	for k, v := range hostHow {
		how[k] = v
	}
}

// hostWordingText returns the sentence for a key in hostShows or hostHow.
func hostWordingText(key string) (string, bool) {
	if s, ok := hostShows[key]; ok {
		return s, true
	}
	s, ok := hostHow[key]
	return s, ok
}
