package server

import (
	"net/http"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
)

// Synthetic heads only: each is built here from magic bytes.
func binaryUploads() []struct {
	name    string
	content string
	reason  importer.Reason
} {
	return []struct {
		name    string
		content string
		reason  importer.Reason
	}{
		{"counters.blg", "PERF\x01\x00\x00\x00" + strings.Repeat("\x00", 64), importer.ReasonBinaryBLG},
		{"trace.etl", "\x01\x02\x00\x03" + strings.Repeat("\x00", 64), importer.ReasonBinaryETL}, // by name, on binary bytes
		{"app.nettrace", "Nettrace\x14\x00\x00\x00!FastSerialization.1", importer.ReasonBinaryNettrace},
		{"System.evtx", "ElfFile\x00" + strings.Repeat("\x00", 64), importer.ReasonBinaryEVTX},
		{"profile.bson", "\x40\x00\x00\x00\x07_id\x00" + strings.Repeat("\x00", 32), importer.ReasonBinaryBSON},
		{"health.xel", "\x01\x02\x00\x03" + strings.Repeat("\x00", 64), importer.ReasonBinaryXEL},
		{"dump.rdb", "REDIS0011\xfa\x09redis-ver", importer.ReasonBinaryRDB},
		// The clumsy case: a binary export renamed to look like a text one.
		{"counters.csv", "ElfFile\x00" + strings.Repeat("\x00", 64), importer.ReasonBinaryEVTX},
		{"notes.txt", "REDIS0011\xfa\x09redis-ver", importer.ReasonBinaryRDB},
	}
}

func TestBinaryExportsAreRefusedWithTheirFamily(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Binary case")
	paths := map[string]string{
		"detect preview":         "/api/cases/" + id + "/imports?preview=1&host=web-a",
		"named importer preview": "/api/cases/" + id + "/imports?preview=1&host=web-a&importer=toy",
		"named importer commit":  "/api/cases/" + id + "/imports?host=web-a&importer=toy",
	}
	for label, path := range paths {
		for _, u := range binaryUploads() {
			fields := ackFields
			if strings.Contains(path, "preview=1") {
				fields = nil
			}
			res := e.upload(path, fields, &upload{u.name, u.content})
			if res.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s, %s: status %d %s", label, u.name, res.Code, res.Body)
				continue
			}
			f := failureOf(t, res)
			if f.Reason != string(u.reason) || f.Column != "" {
				t.Errorf("%s, %s: reason %q column %q, want %q", label, u.name, f.Reason, f.Column, u.reason)
			}
			// Neither the file name nor its bytes come back.
			body := res.Body.String()
			for _, leak := range []string{u.name, "ElfFile", "REDIS", "Nettrace", "PERF"} {
				if strings.Contains(body, leak) {
					t.Errorf("%s, %s: response holds %q: %s", label, u.name, leak, body)
				}
			}
		}
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("refused files stored %d imports", n)
	}
}

// A text file with a family name is not refused for its name: the importers judge it.
func TestTextNamedLikeABinaryIsNotRefusedByName(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Text case")
	for _, name := range []string{"toy.blg", "toy.etl", "toy.xel", "toy.rdb", "toy.bson", "toy.nettrace", "toy.evtx"} {
		res := e.upload("/api/cases/"+id+"/imports?preview=1&host=web-a", nil, &upload{name, toyCSV(toyRow(1, "web-a", "alpha", 5))})
		if res.Code != http.StatusOK {
			t.Errorf("%s: %d %s", name, res.Code, res.Body)
		}
	}
}
