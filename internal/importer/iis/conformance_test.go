package iis

import (
	"bytes"
	"testing"

	"api-audit-explorer/internal/importer/importertest"
)

// clean swaps the sentinel planted in dropped columns of the shared fixtures
// for a neutral word: the harness insists Good carries no sentinel.
func clean(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte(importertest.Sentinel), []byte("DROPPED-VALUE"))
}

func w3cFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good:    clean(fixture(t, "w3c_good.log")),
		Bad:     w3cFile(40, "2001-02-03 04:05:59 WEB-A GET /a "+importertest.Sentinel+" 5"),
		Options: baseOpts(),
		// every attribute is a typed token, number, allowlisted word, normalised
		// route or pseudonym; none is free text from the file
		NoFreeText: true,
	}
}

func httperrFixtures(t testing.TB) importertest.Fixtures {
	return importertest.Fixtures{
		Good: clean(fixture(t, "httperr_good.log")),
		Bad: httperrFile("2001-03-17 10:01:01 192.0.2.50 GET /a " + importertest.Sentinel +
			" QueueFull pool-alpha"),
		Options: httperrOpts(),
		// as for W3C: typed or allowlisted values only
		NoFreeText: true,
	}
}

func TestW3CConformance(t *testing.T) {
	importertest.Run(t, NewW3C(), w3cFixtures(t))
}

func TestHTTPERRConformance(t *testing.T) {
	importertest.Run(t, NewHTTPERR(), httperrFixtures(t))
}

func FuzzW3CParse(f *testing.F) {
	importertest.FuzzParse(f, NewW3C(), w3cFixtures(f))
}

func FuzzHTTPERRParse(f *testing.F) {
	importertest.FuzzParse(f, NewHTTPERR(), httperrFixtures(f))
}
