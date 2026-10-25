//go:build windows

package evtx

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/all"
)

// These tests read the real Application and System logs of the machine that
// runs them. They assert on shape only, never on content, and skip when the
// account is denied or the window is empty.

func TestRealListAndCanRead(t *testing.T) {
	names, err := ListChannels()
	if errors.Is(err, fs.ErrPermission) {
		t.Skip("listing channels denied")
	}
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, n := range names {
		have[strings.ToLower(n)] = true
	}
	if !have["application"] || !have["system"] {
		t.Fatalf("Application or System missing from %d channels", len(names))
	}
	if err := CanRead("Application"); err != nil && !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("CanRead Application: %v", err)
	}
	if err := CanRead("No-Such-Channel/Operational"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing channel: %v", err)
	}
	if err := CanRead("Security"); !errors.Is(err, ErrChannelRefused) {
		t.Fatalf("Security: %v", err)
	}
}

func TestRealQueryApplicationShape(t *testing.T) {
	now := time.Now().UTC()
	var out bytes.Buffer
	res, err := Query(context.Background(), &out, "Application", now.Add(-time.Hour), now, 0)
	if errors.Is(err, fs.ErrPermission) {
		t.Skip("Application channel denied for this account")
	}
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows == 0 {
		// Nothing in the last hour; widen only to get a sample to check.
		out.Reset()
		if res, err = Query(context.Background(), &out, "Application", now.Add(-7*24*time.Hour), now, 0); err != nil {
			t.Fatal(err)
		}
		if res.Rows == 0 {
			t.Skip("no Application events to check")
		}
	}
	checkRealCSV(t, out.Bytes(), res, "Application")

	// The importer reads the same bytes.
	imp, _ := all.Registry().ByName("win-events")
	machine := firstMachine(t, out.Bytes())
	opts := importer.Options{
		CaseID: "abcdefgh12345678abcdefgh12", ImportNonce: "n1",
		Hosts: func(o string) (importer.HostHint, bool) {
			return importer.HostHint{Label: "web-a", Role: "iis", TZ: time.UTC}, strings.EqualFold(o, machine)
		},
	}
	events, rep, err := importer.Collect(context.Background(), imp, bytes.NewReader(out.Bytes()), opts)
	if err != nil {
		t.Fatalf("importer: %v", err)
	}
	if int(rep.Emitted) != res.Rows || len(events) != res.Rows {
		t.Fatalf("emitted %d of %d rows (rejected %d)", len(events), res.Rows, rep.Rejected)
	}
}

func TestRealQueryWatermarkContinues(t *testing.T) {
	now := time.Now().UTC()
	var first bytes.Buffer
	res, err := Query(context.Background(), &first, "Application", now.Add(-7*24*time.Hour), now, 0)
	if errors.Is(err, fs.ErrPermission) {
		t.Skip("denied")
	}
	if err != nil || res.Rows == 0 {
		t.Skipf("no sample (rows=%d err=%v)", res.Rows, err)
	}
	var second bytes.Buffer
	res2, err := Query(context.Background(), &second, "Application", now.Add(-7*24*time.Hour), now, res.LastRecordID)
	if err != nil {
		t.Fatal(err)
	}
	if res2.Rows != 0 && res2.LastRecordID <= res.LastRecordID {
		t.Fatalf("watermark %d did not move past %d", res2.LastRecordID, res.LastRecordID)
	}
}

func TestRealQueryWASAppPool(t *testing.T) {
	now := time.Now().UTC()
	var out bytes.Buffer
	res, err := QueryFiltered(context.Background(), &out, "System", now.AddDate(0, 0, -90), now, 0,
		[]Filter{{Provider: WASProvider}})
	if errors.Is(err, fs.ErrPermission) {
		t.Skip("System channel denied")
	}
	if err != nil {
		t.Fatal(err)
	}
	if res.Rows == 0 {
		t.Skip("no WAS events on this machine")
	}
	checkRealCSV(t, out.Bytes(), res, "System")
	for _, row := range readCSV(t, out.Bytes())[1:] {
		if row[3] != WASProvider {
			t.Fatalf("provider filter let %q through", row[3])
		}
	}
}

func TestRealQueryCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Query(ctx, &bytes.Buffer{}, "Application", time.Time{}, time.Time{}, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("err %v", err)
	}
}

func firstMachine(t *testing.T, b []byte) string {
	t.Helper()
	rows := readCSV(t, b)
	return rows[1][1]
}

// checkRealCSV asserts the shape of a real pull: the fixed header, seven cells
// per row, the asked-for channel, numeric ids, and no Message or EventData
// column or cell. AppPool is empty for every provider except WAS.
func checkRealCSV(t *testing.T, b []byte, res Result, channel string) {
	t.Helper()
	rows := readCSV(t, b)
	if strings.Join(rows[0], ",") != strings.Join(Header, ",") {
		t.Fatalf("header %v", rows[0])
	}
	if len(rows)-1 != res.Rows {
		t.Fatalf("rows %d, result says %d", len(rows)-1, res.Rows)
	}
	var prev time.Time
	for i, row := range rows[1:] {
		if len(row) != len(Header) {
			t.Fatalf("row %d has %d cells", i, len(row))
		}
		ts, err := time.Parse(time.RFC3339Nano, row[0])
		if err != nil || !strings.HasSuffix(row[0], "Z") {
			t.Fatalf("row %d time %q", i, row[0])
		}
		if ts.Before(prev.Add(-2 * time.Minute)) {
			t.Fatalf("row %d is far out of order", i) // oldest first, give or take clock changes
		}
		prev = ts
		if !strings.EqualFold(row[2], channel) {
			t.Fatalf("row %d channel %q", i, row[2])
		}
		if _, err := strconv.ParseUint(row[4], 10, 32); err != nil {
			t.Fatalf("row %d id %q", i, row[4])
		}
		if _, err := strconv.ParseUint(row[5], 10, 8); err != nil {
			t.Fatalf("row %d level %q", i, row[5])
		}
		if !IsWAS(row[3]) && row[6] != "" {
			t.Fatalf("row %d: app pool %q on non-WAS provider %q", i, row[6], row[3])
		}
		if len(row[6]) > maxTextRunes*4 {
			t.Fatalf("row %d app pool too long", i)
		}
	}
	if res.LastRecordID == 0 {
		t.Fatal("no last record id")
	}
}
