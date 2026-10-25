package importer_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
)

// Every head below is built here from magic bytes. No real file is opened.

func pad(head string, n int) []byte { return append([]byte(head), make([]byte, n)...) }

// bsonHead is an int32 length, an element type, a field name and padding.
func bsonHead(length byte, elem byte, name string) []byte {
	b := []byte{length, 0, 0, 0, elem}
	b = append(b, name...)
	b = append(b, 0)
	return append(b, make([]byte, 24)...)
}

func familyHeads() map[importer.Reason][]byte {
	return map[importer.Reason][]byte{
		importer.ReasonBinaryEVTX:     pad("ElfFile\x00", 64),
		importer.ReasonBinaryNettrace: append([]byte("Nettrace\x14\x00\x00\x00"), "!FastSerialization.1"...),
		importer.ReasonBinaryRDB:      append([]byte("REDIS0011"), 0xfa, 0x09),
		importer.ReasonBinaryBLG:      pad("PERF\x01\x00\x00\x00", 64),
		importer.ReasonBinaryBSON:     bsonHead(0x40, 0x07, "_id"), // mongodump: _id ObjectId first
	}
}

func reasonOf(t *testing.T, err error) importer.Reason {
	t.Helper()
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || !errors.Is(err, importer.ErrQuarantine) {
		t.Fatalf("not a quarantine: %v", err)
	}
	return qe.Reason
}

func TestBinaryFamiliesByMagic(t *testing.T) {
	for want, head := range familyHeads() {
		for _, name := range []string{"", "export.csv", "export.txt", "report.log"} {
			err := importer.SniffNamed(head, name)
			if err == nil {
				t.Errorf("%s named %q: not refused", want, name)
				continue
			}
			if got := reasonOf(t, err); got != want {
				t.Errorf("named %q: reason %s, want %s", name, got, want)
			}
		}
		// Sniff is the same check with no name.
		if got := reasonOf(t, importer.Sniff(head)); got != want {
			t.Errorf("Sniff: reason %s, want %s", got, want)
		}
	}
}

// The clumsy-user case: the name says .csv, the bytes say otherwise. Content wins.
func TestBinaryContentBeatsTheName(t *testing.T) {
	heads := familyHeads()
	for _, name := range []string{"counters.csv", "COUNTERS.CSV", "notes.txt", "data.json"} {
		if got := reasonOf(t, importer.SniffNamed(heads[importer.ReasonBinaryEVTX], name)); got != importer.ReasonBinaryEVTX {
			t.Errorf("%s: %s", name, got)
		}
	}
	// A different family in the name does not override the content either.
	if got := reasonOf(t, importer.SniffNamed(heads[importer.ReasonBinaryRDB], "dump.blg")); got != importer.ReasonBinaryRDB {
		t.Errorf("rdb bytes named .blg: %s", got)
	}
	// Archives are still refused first.
	if got := reasonOf(t, importer.SniffNamed([]byte("PK\x03\x04\x00\x00"), "x.rdb")); got != importer.ReasonArchive {
		t.Errorf("zip named .rdb: %s", got)
	}
}

// Decision: a name is only a tie-break, and only for a head that is binary (a NUL
// byte, no UTF-16 byte order mark). A text file named .blg is never refused for its name.
func TestBinaryExtensionIsATieBreakOnBinaryHeadsOnly(t *testing.T) {
	binary := []byte("\x01\x02\x03\x00\x04\x05 no known signature \x00\xff\xfe")
	ext := map[string]importer.Reason{
		"a.blg": importer.ReasonBinaryBLG, "a.etl": importer.ReasonBinaryETL, "a.nettrace": importer.ReasonBinaryNettrace,
		"a.evtx": importer.ReasonBinaryEVTX, "a.bson": importer.ReasonBinaryBSON, "a.xel": importer.ReasonBinaryXEL,
		"a.rdb": importer.ReasonBinaryRDB,
		// Case, folders of either slash, and the spaces and dots Windows drops.
		"C:\\temp\\LOG.BLG": importer.ReasonBinaryBLG, "/tmp/x.y/trace.Etl": importer.ReasonBinaryETL, "session.xel. ": importer.ReasonBinaryXEL,
	}
	for name, want := range ext {
		if got := reasonOf(t, importer.SniffNamed(binary, name)); got != want {
			t.Errorf("binary %q: %s, want %s", name, got, want)
		}
	}
	// The same bytes under a name with no family are not refused here (the importers judge them).
	for _, name := range []string{"", "a.csv", "a.blg.csv", "blg", ".gitignore", "dir.blg/file", "dir.blg\\file"} {
		if err := importer.SniffNamed(binary, name); err != nil {
			t.Errorf("binary %q: %v", name, err)
		}
	}
	// A text file with a family name is not refused: it is judged by its content.
	text := []byte("\"(PDH-CSV 4.0)\",\"\\\\WEB-A\\Memory\\Available MBytes\"\n\"10/24/2001 13:00:00.000\",\"100\"\n")
	for name := range ext {
		if err := importer.SniffNamed(text, name); err != nil {
			t.Errorf("text named %q: %v", name, err)
		}
	}
	// UTF-16 text (byte order mark, so NUL bytes) named .xel or .blg is text too.
	utf16 := []byte{0xff, 0xfe, 'a', 0, ',', 0, 'b', 0, '\n', 0}
	for _, name := range []string{"a.xel", "a.blg", "a.etl", "a.rdb"} {
		if err := importer.SniffNamed(utf16, name); err != nil {
			t.Errorf("utf-16 text named %q: %v", name, err)
		}
	}
	// A short head with no NUL is not binary.
	if err := importer.SniffNamed([]byte("abc"), "a.blg"); err != nil {
		t.Errorf("short text: %v", err)
	}
	if err := importer.SniffNamed(nil, "a.blg"); err != nil {
		t.Errorf("empty: %v", err)
	}
}

func TestBinarySniffHasNoFalsePositivesOnText(t *testing.T) {
	for name, head := range map[string]string{
		"perf word":      "PERF report for the week\n",
		"perf nul late":  "PERF report" + strings.Repeat(" ", 20) + "\x00",
		"nettrace word":  "Nettrace is the file format dotnet-trace writes\n",
		"redis word":     "REDIS2024 notes\n",
		"redis version":  "REDIS0011 notes\n",
		"redis too few":  "REDIS001",
		"elf word":       "ElfFile is a name\n",
		"json":           "{\"t\":\"2001-01-01T00:00:00Z\",\"msg\":\"x\"}\n",
		"csv":            "timestamp_utc,name,duration\n2001-01-01T00:00:00Z,a,1\n",
		"iis":            "#Software: Microsoft Internet Information Services 10.0\n",
		"utf16 no bom":   "a\x00b\x00c\x00d\x00e\x00f\x00",
		"utf16 bom":      "\xff\xfea\x00b\x00c\x00d\x00",
		"tabs":           "a\tb\tc\n",
		"short":          "ab",
		"bson too short": "\x10\x00\x00\x00\x07_",
	} {
		if err := importer.SniffNamed([]byte(head), "export.csv"); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// BSON look-alikes that are not documents.
	for name, head := range map[string][]byte{
		"length too small":    bsonHead(4, 0x07, "_id"),
		"length over 16 MiB": {0x40, 0x00, 0x00, 0x01, 0x07, '_', 'i', 'd', 0, 0, 0, 0},
		"bad element type":    bsonHead(0x40, 0x20, "_id"),
		"empty field name":    bsonHead(0x40, 0x07, ""),
		"unprintable name":    bsonHead(0x40, 0x07, "a\x01b"),
		"name never ends":     append([]byte{0x40, 0, 0, 0, 0x07}, bytes.Repeat([]byte("n"), 80)...),
		"name ends past head": {0x40, 0, 0, 0, 0x07, 'a', 'b', 'c'},
	} {
		if err := importer.Sniff(head); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Refusal comes from the first few KiB only.
func TestBinarySniffLooksAtTheFirstKiBOnly(t *testing.T) {
	filler := bytes.Repeat([]byte("a,b\n"), 2048) // 8 KiB of text
	for want, head := range familyHeads() {
		late := append(append([]byte(nil), filler...), head...)
		if err := importer.SniffNamed(late, "a.csv"); err != nil {
			t.Errorf("%s magic after 8 KiB of text: %v", want, err)
		}
	}
	// A NUL past the first KiB does not make a .blg binary.
	late := append(bytes.Repeat([]byte("x"), 2048), 0)
	if err := importer.SniffNamed(late, "a.blg"); err != nil {
		t.Errorf("late NUL: %v", err)
	}
	// A very large head is cut to HeadSize before anything else is looked at.
	big := append(bytes.Repeat([]byte("x"), importer.HeadSize), "ElfFile\x00"...)
	if err := importer.SniffNamed(big, ""); err != nil {
		t.Errorf("magic past the head: %v", err)
	}
}

// Nothing from the file or its name reaches the error: the reason is a fixed enum value.
func TestBinaryRefusalNeverEchoes(t *testing.T) {
	const secret = "SENTINEL-7f3a"
	head := append(pad("ElfFile\x00", 8), secret...)
	name := secret + ".blg"
	err := importer.SniffNamed(head, name)
	if err == nil {
		t.Fatal("not refused")
	}
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Column != "" {
		t.Fatalf("column set: %+v", qe)
	}
	if strings.Contains(err.Error(), "SENTINEL") || strings.Contains(qe.Reason.Text(), "SENTINEL") {
		t.Errorf("echoed: %q", err)
	}
	// The name alone (binary head, no signature) is not echoed either.
	err = importer.SniffNamed([]byte("\x00\x01"), name)
	if err == nil || strings.Contains(err.Error(), "SENTINEL") {
		t.Errorf("name echoed or not refused: %v", err)
	}
}

func TestBinaryReasonsAreFixedValuesWithText(t *testing.T) {
	want := []importer.Reason{
		importer.ReasonBinaryBLG, importer.ReasonBinaryETL, importer.ReasonBinaryNettrace, importer.ReasonBinaryEVTX,
		importer.ReasonBinaryBSON, importer.ReasonBinaryXEL, importer.ReasonBinaryRDB,
	}
	known := map[importer.Reason]bool{}
	for _, r := range importer.AllReasons() {
		known[r] = true
	}
	texts := map[string]bool{}
	for _, r := range want {
		if !known[r] || !r.Valid() || !strings.HasPrefix(string(r), "binary-") {
			t.Errorf("%s not a valid binary reason", r)
		}
		if r.Text() == "refused" || r.Text() == "" || texts[r.Text()] {
			t.Errorf("%s text %q", r, r.Text())
		}
		texts[r.Text()] = true
	}
}
