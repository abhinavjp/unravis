package evtx

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/all"
)

const sentinel = "SENTINEL-7f3a"

var t0 = time.Date(2001, 9, 11, 8, 46, 30, 123_000_000, time.UTC)

func readCSV(t *testing.T, b []byte) [][]string {
	t.Helper()
	rows, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
	if err != nil {
		t.Fatalf("csv: %v\n%s", err, b)
	}
	return rows
}

func TestRowWriterHeaderAndRows(t *testing.T) {
	var out bytes.Buffer
	rw := NewRowWriter(&out)
	recs := []Record{
		{Time: t0, Computer: "WEB-A", Channel: "System", Provider: "Microsoft-Windows-WAS", EventID: 5002, Level: 2, RecordID: 41, AppPool: "pool-alpha"},
		{Time: t0.Add(time.Second), Computer: "WEB-A", Channel: "Application", Provider: "Application Error", EventID: 1000, Level: 2, RecordID: 42},
	}
	for _, r := range recs {
		if err := rw.Write(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := rw.Flush(); err != nil {
		t.Fatal(err)
	}
	rows := readCSV(t, out.Bytes())
	if len(rows) != 3 || strings.Join(rows[0], ",") != strings.Join(Header, ",") {
		t.Fatalf("rows %v", rows)
	}
	if rows[1][0] != "2001-09-11T08:46:30.1230000Z" || rows[1][4] != "5002" || rows[1][6] != "pool-alpha" || rows[2][6] != "" {
		t.Fatalf("row %v / %v", rows[1], rows[2])
	}
	if rw.Rows() != 2 || rw.LastRecordID() != 42 {
		t.Fatalf("rows=%d last=%d", rw.Rows(), rw.LastRecordID())
	}
	for _, h := range Header {
		if strings.EqualFold(h, "Message") {
			t.Fatal("header has a Message column")
		}
	}
}

func TestRowWriterCellsCannotBreakRows(t *testing.T) {
	var out bytes.Buffer
	rw := NewRowWriter(&out)
	err := rw.Write(Record{
		Time: t0, Computer: "web-a\r\n\"injected\",1,2", Channel: "System",
		Provider: "P‮roviderq", EventID: 1, Level: 4, RecordID: 1,
		AppPool: "a\x00b​c" + strings.Repeat("x", 400),
	})
	if err != nil {
		t.Fatal(err)
	}
	rw.Flush()
	rows := readCSV(t, out.Bytes())
	if len(rows) != 2 || len(rows[1]) != len(Header) {
		t.Fatalf("shape %v", rows)
	}
	if strings.ContainsAny(rows[1][1], "\r\n") || strings.ContainsRune(rows[1][3], '‮') {
		t.Fatalf("control or bidi char survived: %q %q", rows[1][1], rows[1][3])
	}
	if got := rows[1][6]; !strings.HasPrefix(got, "abc") || len(got) != maxTextRunes {
		t.Fatalf("app pool %q (len %d)", got, len(got))
	}
}

func TestRowCapStopsWithoutWriting(t *testing.T) {
	var out bytes.Buffer
	rw := newRowWriter(&out, 2)
	for i := uint64(1); i <= 2; i++ {
		if err := rw.Write(Record{Time: t0, Computer: "c", Channel: "System", Provider: "p", EventID: 1, RecordID: i}); err != nil {
			t.Fatal(err)
		}
	}
	if err := rw.Write(Record{Time: t0, Computer: "c", Channel: "System", Provider: "p", EventID: 1, RecordID: 3}); !errors.Is(err, ErrRowCap) {
		t.Fatalf("err %v", err)
	}
	rw.Flush()
	if rw.Rows() != 2 || rw.LastRecordID() != 2 || len(readCSV(t, out.Bytes())) != 3 {
		t.Fatalf("rows=%d last=%d", rw.Rows(), rw.LastRecordID())
	}
	if RowCap != 2_000_000 {
		t.Fatalf("RowCap %d", RowCap)
	}
}

func TestLastRecordIDIsHighest(t *testing.T) {
	rw := NewRowWriter(&bytes.Buffer{})
	for _, id := range []uint64{7, 9, 8} {
		rw.Write(Record{Time: t0, Computer: "c", Channel: "System", Provider: "p", RecordID: id})
	}
	if rw.LastRecordID() != 9 {
		t.Fatalf("last %d", rw.LastRecordID())
	}
}

func TestNextWatermark(t *testing.T) {
	cases := []struct {
		prev, last uint64
		rows       int
		want       uint64
	}{
		{0, 0, 0, 0},       // nothing read, nothing known
		{100, 0, 0, 100},   // empty pull keeps the watermark
		{100, 250, 0, 100}, // no rows written: never move
		{100, 250, 12, 250},
		{0, 5, 1, 5},
		{100, 0, 3, 100}, // rows but no ID: keep, never go back to 0
	}
	for _, c := range cases {
		if got := NextWatermark(c.prev, c.last, c.rows); got != c.want {
			t.Errorf("NextWatermark(%d,%d,%d) = %d, want %d", c.prev, c.last, c.rows, got, c.want)
		}
	}
}

func TestBuildXPath(t *testing.T) {
	from := time.Date(2001, 9, 11, 8, 0, 0, 0, time.UTC)
	to := time.Date(2001, 9, 12, 8, 0, 0, 0, time.UTC)
	got, err := BuildXPath(from, to, 0, nil)
	want := "*[System[TimeCreated[@SystemTime>='2001-09-11T08:00:00.000Z' and @SystemTime<'2001-09-12T08:00:00.000Z']]]"
	if err != nil || got != want {
		t.Fatalf("got %q err %v", got, err)
	}
	got, _ = BuildXPath(from, to, 77, nil)
	if !strings.HasSuffix(got, " and EventRecordID>77]]") {
		t.Fatalf("watermark missing: %q", got)
	}
	// A non-UTC zone is converted.
	est := time.FixedZone("x", -5*3600)
	got, _ = BuildXPath(from.In(est), time.Time{}, 0, nil)
	if got != "*[System[TimeCreated[@SystemTime>='2001-09-11T08:00:00.000Z']]]" {
		t.Fatalf("zone: %q", got)
	}
	if got, _ := BuildXPath(time.Time{}, time.Time{}, 0, nil); got != "*" {
		t.Fatalf("empty: %q", got)
	}
	if _, err := BuildXPath(to, from, 0, nil); err == nil {
		t.Fatal("reversed window accepted")
	}
	got, err = BuildXPath(time.Time{}, time.Time{}, 0, []Filter{{Provider: "Application Error", IDs: []uint16{1000}}, {Provider: "EventLog"}})
	if err != nil || got != "*[System[((Provider[@Name='Application Error'] and (EventID=1000)) or (Provider[@Name='EventLog']))]]" {
		t.Fatalf("filters: %q %v", got, err)
	}
	for _, bad := range []string{"x' or '1'='1", "a]]|*[", "", `a"b`, strings.Repeat("a", 200)} {
		if _, err := BuildXPath(time.Time{}, time.Time{}, 0, []Filter{{Provider: bad}}); err == nil {
			t.Errorf("provider %q accepted", bad)
		}
	}
}

func TestValidateChannelRefusesSecurity(t *testing.T) {
	for _, n := range []string{"Security", "security", " Security", "SECURITY/Foo", "", "App\nlication", strings.Repeat("a", 300)} {
		if err := ValidateChannel(n); !errors.Is(err, ErrChannelRefused) {
			t.Errorf("%q: %v", n, err)
		}
	}
	for _, n := range []string{"Application", "System", "Microsoft-Windows-TaskScheduler/Operational", "Microsoft-Windows-Security-Auditing-Fake"} {
		if err := ValidateChannel(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}
	// Query refuses before it touches the system, on every OS.
	if _, err := Query(context.Background(), &bytes.Buffer{}, "Security", time.Time{}, time.Time{}, 0); !errors.Is(err, ErrChannelRefused) {
		t.Fatalf("Query Security: %v", err)
	}
}

func TestQueryRespectsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Query(ctx, &bytes.Buffer{}, "Application", time.Time{}, time.Time{}, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

// --- renderer, on synthetic rendered values ---

const synthBase = 0x7ff0_0000

// synth builds a buffer the way EvtRender fills one: an array of variants and,
// after it, the text they point at.
type synth struct {
	variants [][16]byte
	data     []byte
	base     uint64
	nvar     int
}

func newSynth(n int) *synth {
	return &synth{variants: make([][16]byte, n), base: synthBase, nvar: n}
}

func (s *synth) set(i int, typ uint32, raw uint64) {
	binary.LittleEndian.PutUint64(s.variants[i][0:8], raw)
	binary.LittleEndian.PutUint32(s.variants[i][12:16], typ)
}

// str stores text after the variants and points variant i at it.
func (s *synth) str(i int, text string) {
	off := s.nvar*variantSize + len(s.data)
	for _, u := range utf16.Encode([]rune(text)) {
		s.data = binary.LittleEndian.AppendUint16(s.data, u)
	}
	s.data = binary.LittleEndian.AppendUint16(s.data, 0)
	s.set(i, varString, s.base+uint64(off))
}

func (s *synth) bytes() []byte {
	var b []byte
	for _, v := range s.variants {
		b = append(b, v[:]...)
	}
	return append(b, s.data...)
}

func filetimeOf(t time.Time) uint64 {
	return uint64(t.Unix()+filetimeToUnixSeconds)*10_000_000 + uint64(t.Nanosecond()/100)
}

// systemBuf is a full 18 value System render. Slots a reader must ignore
// (UserID, ActivityID, a stray string) carry the sentinel.
func systemBuf(provider, channel, computer string, id uint16, level uint8, ts time.Time, rid uint64) *synth {
	s := newSynth(sysPropertyEnd)
	s.str(sysProviderName, provider)
	s.set(sysEventID, varUInt16, uint64(id))
	s.set(sysLevel, varByte, uint64(level))
	s.set(sysTimeCreated, varFileTime, filetimeOf(ts))
	s.set(sysRecordID, varUInt64, rid)
	s.str(sysChannel, channel)
	s.str(sysComputer, computer)
	s.str(10, sentinel+" activity")
	s.str(16, sentinel+" user")
	s.str(17, sentinel+" message")
	return s
}

func TestDecodeSystem(t *testing.T) {
	s := systemBuf("Application Error", "Application", "WEB-A", 1000, 2, t0, 4242)
	rec, err := decodeSystem(s.bytes(), s.base, sysPropertyEnd)
	if err != nil {
		t.Fatal(err)
	}
	want := Record{Time: t0, Computer: "WEB-A", Channel: "Application", Provider: "Application Error", EventID: 1000, Level: 2, RecordID: 4242}
	if rec != want {
		t.Fatalf("got %+v\nwant %+v", rec, want)
	}
	if rec.AppPool != "" {
		t.Fatal("app pool set from a System render")
	}
}

func TestDecodeSystemNullLevelAndQualifiersOK(t *testing.T) {
	s := systemBuf("EventLog", "System", "WEB-A", 6008, 0, t0, 9)
	s.set(sysLevel, varNull, 0) // classic events can have no level
	rec, err := decodeSystem(s.bytes(), s.base, sysPropertyEnd)
	if err != nil || rec.Level != 0 || rec.EventID != 6008 {
		t.Fatalf("%+v %v", rec, err)
	}
}

func TestDecodeSystemRejectsMalformed(t *testing.T) {
	good := func() *synth {
		return systemBuf("p", "System", "c", 1, 4, t0, 5)
	}
	cases := map[string]func(s *synth) (buf []byte, count int){
		"short count": func(s *synth) ([]byte, int) { return s.bytes(), 10 },
		"truncated array": func(s *synth) ([]byte, int) {
			return s.bytes()[:sysPropertyEnd*variantSize-1], sysPropertyEnd
		},
		"pointer below base": func(s *synth) ([]byte, int) {
			s.set(sysProviderName, varString, s.base-2)
			return s.bytes(), sysPropertyEnd
		},
		"pointer past end": func(s *synth) ([]byte, int) {
			s.set(sysProviderName, varString, s.base+uint64(len(s.bytes())+64))
			return s.bytes(), sysPropertyEnd
		},
		"odd pointer": func(s *synth) ([]byte, int) {
			s.set(sysChannel, varString, s.base+uint64(sysPropertyEnd*variantSize)+1)
			return s.bytes(), sysPropertyEnd
		},
		"no terminator": func(s *synth) ([]byte, int) {
			// Point at the last unit of the buffer and make it non-zero.
			n := len(s.bytes())
			s.set(sysComputer, varString, s.base+uint64(n-2))
			b := s.bytes()
			b[n-2], b[n-1] = 'A', 0 // the unit 'A' is the last in the buffer: no NUL follows
			return b, sysPropertyEnd
		},
		"time is not a filetime": func(s *synth) ([]byte, int) { s.set(sysTimeCreated, varUInt64, 5); return s.bytes(), sysPropertyEnd },
		"zero filetime":          func(s *synth) ([]byte, int) { s.set(sysTimeCreated, varFileTime, 0); return s.bytes(), sysPropertyEnd },
		"event id is text":       func(s *synth) ([]byte, int) { s.str(sysEventID, "1"); return s.bytes(), sysPropertyEnd },
		"event id too big":       func(s *synth) ([]byte, int) { s.set(sysEventID, varUInt32, 70000); return s.bytes(), sysPropertyEnd },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			buf, n := mutate(good())
			if _, err := decodeSystem(buf, synthBase, n); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestSentinelNeverReachesOutput(t *testing.T) {
	// A System render whose unused slots carry the sentinel, written through the
	// row writer: the reader takes seven values and nothing else.
	s := systemBuf("Microsoft-Windows-WAS", "System", "WEB-A", 5002, 2, t0, 11)
	rec, err := decodeSystem(s.bytes(), s.base, sysPropertyEnd)
	if err != nil {
		t.Fatal(err)
	}
	// AppPoolID values render: one wanted value, then other EventData a wider
	// query would have returned.
	p := newSynth(3)
	p.str(0, "pool-alpha")
	p.str(1, sentinel+" other eventdata")
	p.str(2, sentinel+" connection string")
	if !IsWAS(rec.Provider) {
		t.Fatal("WAS not recognised")
	}
	rec.AppPool = decodeAppPool(p.bytes(), p.base, 1)
	if rec.AppPool != "pool-alpha" {
		t.Fatalf("pool %q", rec.AppPool)
	}
	// Even if the render reported all three properties, only value 0 is read.
	if got := decodeAppPool(p.bytes(), p.base, 3); got != "pool-alpha" {
		t.Fatalf("pool from 3 values %q", got)
	}
	var out bytes.Buffer
	rw := NewRowWriter(&out)
	if err := rw.Write(rec); err != nil {
		t.Fatal(err)
	}
	rw.Flush()
	if strings.Contains(out.String(), "SENTINEL") {
		t.Fatalf("sentinel leaked:\n%s", out.String())
	}
}

func TestDecodeAppPool(t *testing.T) {
	p := newSynth(1)
	p.str(0, "pool\r\nbeta‮")
	if got := decodeAppPool(p.bytes(), p.base, 1); got != "poolbeta" {
		t.Fatalf("got %q", got)
	}
	n := newSynth(1) // value missing: null variant
	if got := decodeAppPool(n.bytes(), n.base, 1); got != "" {
		t.Fatalf("null: %q", got)
	}
	u := newSynth(1)
	u.set(0, varUInt32, 7) // not text
	if got := decodeAppPool(u.bytes(), u.base, 1); got != "" {
		t.Fatalf("non-text: %q", got)
	}
	b := newSynth(1)
	b.set(0, varString, b.base+9999) // bad pointer
	if got := decodeAppPool(b.bytes(), b.base, 1); got != "" {
		t.Fatalf("bad pointer: %q", got)
	}
	if got := decodeAppPool(nil, 0, 1); got != "" {
		t.Fatalf("empty buffer: %q", got)
	}
}

func TestFileTime(t *testing.T) {
	got, ok := fileTime(filetimeOf(t0))
	if !ok || !got.Equal(t0) {
		t.Fatalf("got %v", got)
	}
	if _, ok := fileTime(0); ok {
		t.Fatal("zero accepted")
	}
}

// --- the real importer reads what the writer wrote ---

func TestWriterOutputImportsWithWinEvents(t *testing.T) {
	var out bytes.Buffer
	rw := NewRowWriter(&out)
	recs := []Record{
		{Time: t0, Computer: "web-a", Channel: "System", Provider: "Microsoft-Windows-WAS", EventID: 5002, Level: 2, RecordID: 1, AppPool: "pool-alpha"},
		{Time: t0.Add(time.Second), Computer: "web-a", Channel: "System", Provider: "Microsoft-Windows-WAS", EventID: 5011, Level: 3, RecordID: 2},
		{Time: t0.Add(2 * time.Second), Computer: "web-a", Channel: "Application", Provider: "Application Error", EventID: 1000, Level: 2, RecordID: 3},
		{Time: t0.Add(3 * time.Second), Computer: "web-a", Channel: "Microsoft-Windows-TaskScheduler/Operational", Provider: "Microsoft-Windows-TaskScheduler", EventID: 100, Level: 4, RecordID: 4},
	}
	for _, r := range recs {
		if err := rw.Write(r); err != nil {
			t.Fatal(err)
		}
	}
	rw.Flush()

	imp, ok := all.Registry().ByName("win-events")
	if !ok {
		t.Fatal("win-events not registered")
	}
	if imp.Detect(out.Bytes(), "") < 50 {
		t.Fatal("importer does not recognise the header")
	}
	opts := importer.Options{
		CaseID: "abcdefgh12345678abcdefgh12", ImportNonce: "n1",
		Hosts: func(o string) (importer.HostHint, bool) {
			return importer.HostHint{Label: "web-a", Role: "iis", TZ: time.UTC}, strings.EqualFold(o, "web-a")
		},
	}
	events, rep, err := importer.Collect(context.Background(), imp, bytes.NewReader(out.Bytes()), opts)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 0 || len(events) != 4 {
		t.Fatalf("rejected=%d events=%d errors=%v", rep.Rejected, len(events), rep.Errors)
	}
	if !events[0].Start.Equal(t0) {
		t.Fatalf("time %v", events[0].Start)
	}
	if events[0].Attrs["app-pool"] != "pool-alpha" || events[1].Attrs["app-pool"] != "" {
		t.Fatalf("app pool attrs %v / %v", events[0].Attrs, events[1].Attrs)
	}
	allowed := imp.AllowedAttrs()
	for _, e := range events {
		for k := range e.Attrs {
			if !allowed[k] || k == "message" {
				t.Fatalf("unexpected attribute %q", k)
			}
		}
	}
	if events[2].Kind != "app-crash" || events[3].Kind != "task-start" {
		t.Fatalf("kinds %q %q", events[2].Kind, events[3].Kind)
	}
	for _, g := range rep.KnownGaps {
		if strings.Contains(g, "no task events") {
			t.Fatalf("task gap raised although a task row was present")
		}
	}
}
