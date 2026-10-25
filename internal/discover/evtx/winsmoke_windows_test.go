//go:build windows

package evtx

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Real-OS smoke checks of the wevtapi reader. They run only with
// UNRAVIS_WINSMOKE=1 and assert on counts and shapes, never on event content.

func winSmoke(t *testing.T) {
	t.Helper()
	if os.Getenv("UNRAVIS_WINSMOKE") != "1" {
		t.Skip("set UNRAVIS_WINSMOKE=1 to run the real-OS smoke checks")
	}
}

func handleCount(t *testing.T) uint32 {
	t.Helper()
	var n uint32
	r, _, e := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetProcessHandleCount").Call(uintptr(windows.CurrentProcess()), uintptr(unsafe.Pointer(&n)))
	if r == 0 {
		t.Skipf("handle count: %v", e)
	}
	return n
}

func TestWinSmokeQueryRecentWindow(t *testing.T) {
	winSmoke(t)
	now := time.Now().UTC()
	from := now.Add(-time.Hour)
	for _, ch := range []string{"Application", "System", "Microsoft-Windows-TaskScheduler/Operational"} {
		var out bytes.Buffer
		start := time.Now()
		res, err := Query(context.Background(), &out, ch, from, now, 0)
		if errors.Is(err, fs.ErrPermission) {
			t.Logf("%s: denied for this account", ch)
			continue
		}
		if err != nil {
			t.Fatalf("channel %q query: %v", ch, err)
		}
		t.Logf("%s: rows=%d skipped=%d truncated=%v in %v", ch, res.Rows, res.Skipped, res.Truncated, time.Since(start).Round(time.Millisecond))
		if res.Skipped > res.Rows/2+5 {
			t.Errorf("%s: %d events skipped against %d rows", ch, res.Skipped, res.Rows)
		}
		rows := readCSV(t, out.Bytes())
		if len(rows)-1 != res.Rows {
			t.Fatalf("%s: csv has %d rows, result says %d", ch, len(rows)-1, res.Rows)
		}
		if res.Rows == 0 {
			// An idle channel in the last hour is a valid answer. Widen once so
			// the decoder still meets real events of this channel.
			out.Reset()
			wide, err := Query(context.Background(), &out, ch, now.AddDate(0, 0, -30), now, 0)
			if err != nil {
				t.Fatalf("channel %q wide query: %v", ch, err)
			}
			t.Logf("%s: 30 day window rows=%d skipped=%d truncated=%v", ch, wide.Rows, wide.Skipped, wide.Truncated)
			if wide.Rows > 0 {
				checkRealCSV(t, out.Bytes(), wide, ch)
			}
			continue
		}
		checkRealCSV(t, out.Bytes(), res, ch)
		var prevID uint64
		for i, row := range rows[1:] {
			ts, _ := time.Parse(time.RFC3339Nano, row[0])
			// The window is [from, to): allow a second of formatting slack.
			if ts.Before(from.Add(-time.Second)) || ts.After(now.Add(time.Second)) {
				t.Fatalf("%s row %d is outside the one hour window", ch, i)
			}
			_ = prevID
		}
		// The same window read again, from the watermark, adds only newer ids.
		var again bytes.Buffer
		res2, err := Query(context.Background(), &again, ch, from, now, res.LastRecordID)
		if err != nil {
			t.Fatalf("%s continue: %v", ch, err)
		}
		if res2.Rows != 0 {
			t.Errorf("%s: %d rows past the watermark inside a closed window", ch, res2.Rows)
		}
	}
}

func TestWinSmokeSecurityChannelRefused(t *testing.T) {
	winSmoke(t)
	for _, name := range []string{"Security", "security", "SECURITY", "Security/Operational"} {
		if err := CanRead(name); !errors.Is(err, ErrChannelRefused) {
			t.Errorf("CanRead of a Security-shaped name (len %d): %v", len(name), err)
		}
		if _, err := Query(context.Background(), &bytes.Buffer{}, name, time.Time{}, time.Time{}, 0); !errors.Is(err, ErrChannelRefused) {
			t.Errorf("Query of a Security-shaped name (len %d): %v", len(name), err)
		}
		if _, err := QueryFiltered(context.Background(), &bytes.Buffer{}, name, time.Time{}, time.Time{}, 0, nil); !errors.Is(err, ErrChannelRefused) {
			t.Errorf("QueryFiltered of a Security-shaped name (len %d): %v", len(name), err)
		}
	}
	// Defence in depth: the refusal is the package's own, and does not depend on
	// the OS. For an account that is not elevated the OS refuses too.
	h, err := evtQuery("Security", "*")
	if err == nil {
		evtClose(h)
		t.Log("this account can open the Security channel at the OS level; only the package refusal protects it")
		return
	}
	if !errors.Is(err, ErrAccessDenied) && !errors.Is(err, fs.ErrPermission) {
		t.Errorf("OS refusal of Security is not an access-denied error: %v", err)
	}
}

func TestWinSmokeListedChannelsAreAllProbeable(t *testing.T) {
	winSmoke(t)
	names, err := ListChannels()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) < 100 {
		t.Fatalf("only %d channels", len(names))
	}
	var ok, denied, refused, other int
	for _, n := range names {
		err := CanRead(n)
		switch {
		case err == nil:
			ok++
		case errors.Is(err, fs.ErrPermission):
			denied++
		case errors.Is(err, ErrChannelRefused):
			refused++
		default:
			other++
			t.Errorf("probe of a listed channel failed with a non-contract error: %v", err)
			if other > 3 {
				return
			}
		}
	}
	t.Logf("channels=%d readable=%d denied=%d refused=%d", len(names), ok, denied, refused)
	if ok == 0 {
		t.Error("no channel is readable")
	}
}

// Queries must not leak wevtapi handles or goroutines.
func TestWinSmokeNoHandleLeak(t *testing.T) {
	winSmoke(t)
	run := func() {
		now := time.Now().UTC()
		var out bytes.Buffer
		if _, err := Query(context.Background(), &out, "Application", now.Add(-24*time.Hour), now, 0); err != nil && !errors.Is(err, fs.ErrPermission) {
			t.Fatal(err)
		}
		CanRead("System")
		CanRead("No-Such-Channel/Operational")
		ListChannels()
	}
	// Warm up: the runtime and wevtapi settle a few dozen handles of their own
	// (threads, caches) in the first rounds. A leak of one handle per query or
	// per event would add far more than the margin over the measured rounds.
	for i := 0; i < 40; i++ {
		run()
	}
	before := handleCount(t)
	for i := 0; i < 100; i++ {
		run()
	}
	after := handleCount(t)
	if after > before+30 {
		t.Errorf("process handles grew from %d to %d over 100 rounds", before, after)
	}
}

func TestWinSmokeCancelStopsAQuery(t *testing.T) {
	winSmoke(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	var out bytes.Buffer
	start := time.Now()
	// The whole channel: large enough to be mid-flight when the cancel lands, and
	// if it is small the query simply finishes.
	_, err := Query(ctx, &out, "System", time.Time{}, time.Time{}, 0)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("err %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("cancel took %v", d)
	}
}

func TestWinSmokeConcurrentQueries(t *testing.T) {
	winSmoke(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ch := []string{"Application", "System"}[i%2]
			now := time.Now().UTC()
			var out bytes.Buffer
			if _, err := Query(context.Background(), &out, ch, now.Add(-2*time.Hour), now, 0); err != nil && !errors.Is(err, fs.ErrPermission) {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestWinSmokeFilteredQueryOnlyPassesTheProvider(t *testing.T) {
	winSmoke(t)
	now := time.Now().UTC()
	var probe bytes.Buffer
	res, err := Query(context.Background(), &probe, "System", now.AddDate(0, 0, -7), now, 0)
	if err != nil || res.Rows == 0 {
		t.Skipf("no System sample (rows=%d err=%v)", res.Rows, err)
	}
	prov := readCSV(t, probe.Bytes())[1][3]
	var out bytes.Buffer
	if _, err := QueryFiltered(context.Background(), &out, "System", now.AddDate(0, 0, -7), now, 0, []Filter{{Provider: prov}}); err != nil {
		t.Fatalf("filtered query: %v", err)
	}
	rows := readCSV(t, out.Bytes())
	if len(rows) < 2 {
		t.Fatal("the provider seen in the sample gave no rows")
	}
	for _, r := range rows[1:] {
		if r[3] != prov {
			t.Fatal("provider filter let another provider through")
		}
	}
}
