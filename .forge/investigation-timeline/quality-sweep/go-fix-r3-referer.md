# GO-R2-14 safeReferer non-canonical IPv4 hosts

Fix (internal/importer/iis/common.go): new helper `numericHost(host)`. After the existing `netip.ParseAddr` check, `safeReferer` also drops a referer whose hostname (one trailing dot ignored) has a last dot-separated label that is all digits or `0x`/`0X` plus hex digits. A single all-digit label (`2130706433`) is covered by the same test, since it is its own last label. Matrix-parameter stripping is unchanged.

Rejected now: 2130706433, 0x7f.1, 0177.0.0.1, 127.1, 1.2.3, 0x7f000001, 0X7F.0.0.1, 127.0.0.1., 10.1, a.test.0x1.
Still accepted: example.com, a.b.example.co.uk, localhost (with port), 1host.example.com, xn--bcher-kva.example, 0xample.com.

Tests: table cases added to TestSafeReferer in internal/importer/iis/privacy_helpers_test.go.

Second half of the finding (raw path kept when scrubbing is off): NOT changed, by instruction. safeReferer has no scrub setting; the importer (w3c.go) always stores the cleaned scheme://host/path (no userinfo, query, fragment, matrix params, no IP host). The scrub is applied at case export (internal/evidence/caseexport.go, caseexport_enums.go): with scrubbing on, `referer` is dropped from every event; with scrubbing off, it is exported as stored, and the file headline (`caseHeadlineScrubOff`) says referer web addresses may name hosts and paths. So the path is intentionally exported unscrubbed when scrubbing is off and is disclosed in the export statement.

Verification: go vet ./internal/importer/... clean; go test ./internal/importer/... all ok; GOOS=linux go vet ./internal/... ./cmd/... printed nothing. CRLF line endings preserved.
