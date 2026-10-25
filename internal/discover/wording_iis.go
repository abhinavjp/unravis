package discover

// Wording keys the IIS detectors use. The sentences live in wording.go and the
// golden file; this file only names the keys, so a detector cannot misspell one.
// No new key is added here, so the golden file does not change.
const (
	wordingIISShows     = "discover.iis.shows"
	wordingHTTPErrShows = "discover.httperr.shows"
	wordingFrebShows    = "discover.freb.shows"
	wordingANCMShows    = "discover.ancm.shows"
)
