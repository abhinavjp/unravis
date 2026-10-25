package storetest

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

func discoverer(t *testing.T, c casestore.Case) casestore.Discoverer {
	t.Helper()
	d, ok := c.(casestore.Discoverer)
	if !ok {
		t.Fatalf("%T does not implement casestore.Discoverer", c)
	}
	return d
}

// discoverTables is the contract of the host discovery tables: a per-case path
// key, a watermark table that round-trips, an audit table that never holds more
// than the row it was given, and a handle that fails closed after Erase.
func discoverTables(t *testing.T, f Factory, caps casestore.Caps) {
	e := newEnv(t, f, caps)
	a, b := e.newCase("a"), e.newCase("b")
	da, db := discoverer(t, a), discoverer(t, b)

	ka, kb := da.DiscoverKey(), db.DiscoverKey()
	if len(ka) < 16 || bytes.Equal(ka, kb) {
		t.Fatalf("discover keys: len %d, equal across cases %v", len(ka), bytes.Equal(ka, kb))
	}
	if !bytes.Equal(ka, da.DiscoverKey()) {
		t.Error("the discover key is not stable within a case")
	}
	ka[0] ^= 0xff
	if bytes.Equal(ka, da.DiscoverKey()) {
		t.Error("DiscoverKey returned the stored key, not a copy")
	}

	key := casestore.DiscoverFileKey{VolumeSerial: 0xdeadbeef, FileIndex: 1<<63 + 5}
	key.PathHMAC[0], key.PathHMAC[31] = 1, 2
	if _, ok, err := da.DiscoverMark(e.ctx, key); ok || err != nil {
		t.Fatalf("empty table: ok=%v err=%v", ok, err)
	}
	mark := casestore.DiscoverMark{Offset: 4242, LastLine: t0.Add(time.Hour), HeadLen: 100}
	mark.HeadSHA[0], mark.HeadSHA[31] = 7, 9
	if err := da.PutDiscoverMark(e.ctx, key, mark); err != nil {
		t.Fatal(err)
	}
	got, ok, err := da.DiscoverMark(e.ctx, key)
	if err != nil || !ok || got.Offset != 4242 || got.HeadLen != 100 || got.HeadSHA != mark.HeadSHA || !got.LastLine.Equal(mark.LastLine) {
		t.Fatalf("round trip: %+v ok=%v err=%v", got, ok, err)
	}
	mark.Offset = 9000
	mark.LastLine = time.Time{}
	if err := da.PutDiscoverMark(e.ctx, key, mark); err != nil {
		t.Fatal(err)
	}
	got, _, _ = da.DiscoverMark(e.ctx, key)
	if got.Offset != 9000 || !got.LastLine.IsZero() {
		t.Errorf("overwrite: %+v", got)
	}
	if _, ok, _ := db.DiscoverMark(e.ctx, key); ok {
		t.Error("a mark leaked into another case")
	}
	other := key
	other.FileIndex++
	if _, ok, _ := da.DiscoverMark(e.ctx, other); ok {
		t.Error("a different file index matched the mark")
	}
	if err := da.DeleteDiscoverMark(e.ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := da.DiscoverMark(e.ctx, key); ok {
		t.Error("mark survived Delete")
	}
	if err := da.DeleteDiscoverMark(e.ctx, key); err != nil {
		t.Errorf("deleting a missing mark: %v", err)
	}

	row := casestore.DiscoverRead{
		At: t0.Add(2 * time.Hour), Detector: "iis", Importer: "iis-w3c", Kind: "log-set", Display: "W3SVC1 (u_ex010911.log)",
		PathHMAC: strings.Repeat("ab", 32), RangeStart: 10, RangeEnd: 200, FileSize: 300, FileModified: t0,
		BytesSHA256: strings.Repeat("cd", 32), Elevated: true, WindowFrom: t0, WindowTo: t0.Add(24 * time.Hour), Result: "ok",
	}
	if err := da.RecordDiscoverRead(e.ctx, row); err != nil {
		t.Fatal(err)
	}
	reads, err := da.DiscoverReads(e.ctx)
	if err != nil || len(reads) != 1 {
		t.Fatalf("reads: %v %v", reads, err)
	}
	g := reads[0]
	if g.Detector != "iis" || g.Importer != "iis-w3c" || g.Kind != "log-set" || g.PathHMAC != row.PathHMAC || g.RangeEnd != 200 ||
		g.FileSize != 300 || !g.Elevated || g.Result != "ok" || !g.At.Equal(row.At) || !g.WindowTo.Equal(row.WindowTo) || g.BytesSHA256 != row.BytesSHA256 {
		t.Errorf("audit row round trip: %+v", g)
	}
	if r, _ := db.DiscoverReads(e.ctx); len(r) != 0 {
		t.Error("an audit row leaked into another case")
	}
	bad := row
	bad.PathHMAC = "C:\\Sentinel-7f3a\\x"
	if err := da.RecordDiscoverRead(e.ctx, bad); !errors.Is(err, casestore.ErrBadDiscoverRead) && err == nil {
		t.Error("a path in the HMAC field was accepted")
	}
	bad = row
	bad.PathHMAC = strings.Repeat("zz", 32)
	if err := da.RecordDiscoverRead(e.ctx, bad); err == nil {
		t.Error("a non-hex HMAC was accepted")
	}
	if r, _ := da.DiscoverReads(e.ctx); len(r) != 1 {
		t.Errorf("a refused row was stored: %d rows", len(r))
	}

	// Cases do not share rows after a close either.
	if err := e.store.Erase(e.ctx, a.Info().ID); err != nil {
		t.Fatal(err)
	}
	if k := da.DiscoverKey(); k != nil {
		t.Error("an erased case still hands out its discover key")
	}
	if _, _, err := da.DiscoverMark(e.ctx, key); err == nil {
		t.Error("an erased case answered a watermark read")
	}
	if err := da.RecordDiscoverRead(e.ctx, row); err == nil {
		t.Error("an erased case took an audit row")
	}
}
