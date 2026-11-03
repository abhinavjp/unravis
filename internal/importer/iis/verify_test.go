package iis

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
)

// sampleDir returns the folder holding VF-01 sample captures, or "" when
// none exists on this machine. UNRAVIS_VF01_SAMPLES overrides the default
// location, <repo root>/.dev/samples/vf01 (the repo root is found by walking
// up from the working directory to the folder holding go.mod). The folder
// is never created by this helper and never checked into the repo.
func sampleDir(t testing.TB) string {
	t.Helper()
	if d := os.Getenv("UNRAVIS_VF01_SAMPLES"); d != "" {
		return d
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for dir := wd; ; {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			d := filepath.Join(dir, ".dev", "samples", "vf01")
			if info, err := os.Stat(d); err == nil && info.IsDir() {
				return d
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func TestVerifyNotes(t *testing.T) {
	notes := VerifyNotes()
	if len(notes) != 8 {
		t.Fatalf("len(VerifyNotes()) = %d, want 8", len(notes))
	}
	wantIDs := []string{"VF-01a", "VF-01b", "VF-01c", "VF-01d", "VF-01e", "VF-01f", "VF-01g", "VF-01h"}
	ipLike := regexp.MustCompile(`\d+\.\d+\.\d+\.\d+`)
	for i, n := range notes {
		if n.ID != wantIDs[i] {
			t.Errorf("note %d: ID = %q, want %q", i, n.ID, wantIDs[i])
		}
		switch n.Status {
		case VerifyConfirmed, VerifyRefuted, VerifyUnmeasured:
		default:
			t.Errorf("note %s: status %q is not one of the three consts", n.ID, n.Status)
		}
		if n.Claim == "" {
			t.Errorf("note %s: empty claim", n.ID)
		}
		if n.Reason == "" {
			t.Errorf("note %s: empty reason", n.ID)
		}
		// Windows absolute paths (the shape a real sample location would
		// take) use backslashes; a forward slash alone also names the
		// committed, non-sensitive .dev/samples/vf01 default location, so
		// only the backslash form is treated as a leaked path.
		if strings.Contains(n.Reason, `\`) {
			t.Errorf("note %s: reason %q contains a path separator", n.ID, n.Reason)
		}
		if ipLike.MatchString(n.Reason) {
			t.Errorf("note %s: reason %q contains an IP-like string", n.ID, n.Reason)
		}
	}
	if verifiedTimeTakenScope != (Vf01a.Status == VerifyConfirmed) {
		t.Fatalf("verifiedTimeTakenScope = %v, want %v", verifiedTimeTakenScope, Vf01a.Status == VerifyConfirmed)
	}
}

func TestVerifyW3CTimeIsEndMatchesNote(t *testing.T) {
	switch Vf01b.Status {
	case VerifyRefuted:
		if w3cTimeIsEnd {
			t.Fatalf("note %s is REFUTED but w3cTimeIsEnd = true", Vf01b.ID)
		}
	case VerifyConfirmed, VerifyUnmeasured:
		if !w3cTimeIsEnd {
			t.Fatalf("note %s is %s but w3cTimeIsEnd = false", Vf01b.ID, Vf01b.Status)
		}
	}
}

// checkNote compares a freshly measured verdict against the note recorded
// in verify_notes.go. It never rewrites the file; a mismatch names the fix.
func checkNote(t *testing.T, note VerifyNote, measured VerifyStatus, detail string) {
	t.Helper()
	t.Logf("%s: measured %s (%s)", note.ID, measured, detail)
	if measured != note.Status {
		t.Errorf("update verify_notes.go: %s measured %s, recorded %s", note.ID, measured, note.Status)
	}
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	ys := append([]float64(nil), xs...)
	sort.Float64s(ys)
	mid := len(ys) / 2
	if len(ys)%2 == 1 {
		return ys[mid]
	}
	return (ys[mid-1] + ys[mid]) / 2
}

// pairedW3CRequest is one client-side observation from paired-w3c.json.
type pairedW3CRequest struct {
	Method        string  `json:"method"`
	Stem          string  `json:"stem"`
	Status        any     `json:"status"`
	StartUtc      string  `json:"startUtc"`
	EndUtc        string  `json:"endUtc"`
	ClientTotalMs float64 `json:"clientTotalMs"`
	RttMs         float64 `json:"rttMs"`
}

type pairedW3CFile struct {
	Requests []pairedW3CRequest `json:"requests"`
}

type w3cPair struct {
	ev    evidence.Event
	req   pairedW3CRequest
	start time.Time
	end   time.Time
}

// matchPairedW3C walks evs and reqs together in order, consuming a paired
// request whenever the next log row agrees with it on method, stem and
// status. Rows between matches (retries, unrelated traffic) are skipped.
func matchPairedW3C(evs []evidence.Event, reqs []pairedW3CRequest) []w3cPair {
	var out []w3cPair
	j := 0
	for _, e := range evs {
		if j >= len(reqs) {
			break
		}
		r := reqs[j]
		if e.Attrs["method"] != r.Method || e.Attrs["stem"] != r.Stem || e.Attrs["status"] != fmt.Sprint(r.Status) {
			continue
		}
		st, err1 := time.Parse(time.RFC3339, r.StartUtc)
		en, err2 := time.Parse(time.RFC3339, r.EndUtc)
		j++
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, w3cPair{ev: e, req: r, start: st.UTC(), end: en.UTC()})
	}
	return out
}

// TestVerifyMeasureW3C measures VF-01a, VF-01b and VF-01e against any real
// W3C captures found by sampleDir. It reads file bytes only through
// os.ReadFile and never logs log content, file paths or host names: test
// output carries counts and verdicts only.
func TestVerifyMeasureW3C(t *testing.T) {
	dir := sampleDir(t)
	if dir == "" {
		t.Skip("no VF-01 samples: set UNRAVIS_VF01_SAMPLES")
	}
	files, err := filepath.Glob(filepath.Join(dir, "w3c*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no w3c*.log samples found")
	}

	type parsedFile struct {
		evs []evidence.Event
	}
	var parsed []parsedFile
	opts := baseOpts()
	opts.Hosts = nil // accept whatever single machine name each capture carries
	for _, f := range files {
		// A read or parse failure here would otherwise surface the real
		// sample path in a Fatal message; skip the file instead, silently.
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		evs, _, err := parse(t, NewW3C(), data, opts)
		if err != nil {
			continue // a file this importer cannot parse contributes nothing
		}
		parsed = append(parsed, parsedFile{evs: evs})
	}

	// Check e: first row's time-taken against 5x the median of the rest, on
	// files with at least 50 rows.
	eConfirmed, eTotal := 0, 0
	for _, pf := range parsed {
		if len(pf.evs) < 50 {
			continue
		}
		first := pf.evs[0].DurationMs
		if first == nil {
			continue
		}
		rest := make([]float64, 0, len(pf.evs)-1)
		for _, e := range pf.evs[1:] {
			if e.DurationMs != nil {
				rest = append(rest, *e.DurationMs)
			}
		}
		if len(rest) == 0 {
			continue
		}
		eTotal++
		if *first >= 5*median(rest) {
			eConfirmed++
		}
	}
	var eStatus VerifyStatus
	var eReason string
	switch {
	case eTotal >= 3 && eConfirmed >= 2:
		eStatus, eReason = VerifyConfirmed, fmt.Sprintf("%d of %d eligible files show a first-row spike", eConfirmed, eTotal)
	case eTotal >= 3 && eConfirmed == 0:
		eStatus, eReason = VerifyRefuted, fmt.Sprintf("0 of %d eligible files show a first-row spike", eTotal)
	default:
		eStatus, eReason = VerifyUnmeasured, fmt.Sprintf("%d eligible files, %d with a spike (need at least 3 files)", eTotal, eConfirmed)
	}
	checkNote(t, Vf01e, eStatus, eReason)

	// Checks a and b need the paired-w3c.json sidecar and at least 30
	// matched rows; without either, both stay at whatever verify_notes.go
	// already records.
	var aStatus, bStatus VerifyStatus
	var aReason, bReason string
	sidecar, err := os.ReadFile(filepath.Join(dir, "paired-w3c.json"))
	switch {
	case err != nil:
		aStatus, aReason = VerifyUnmeasured, "no paired-w3c.json sidecar"
		bStatus, bReason = aStatus, aReason
	default:
		var pf pairedW3CFile
		if err := json.Unmarshal(sidecar, &pf); err != nil {
			aStatus, aReason = VerifyUnmeasured, "paired-w3c.json present but not readable"
			bStatus, bReason = aStatus, aReason
			break
		}
		var allEvs []evidence.Event
		for _, p := range parsed {
			allEvs = append(allEvs, p.evs...)
		}
		pairs := matchPairedW3C(allEvs, pf.Requests)
		if len(pairs) < 30 {
			aStatus, aReason = VerifyUnmeasured, fmt.Sprintf("%d paired rows matched (need at least 30)", len(pairs))
			bStatus, bReason = aStatus, aReason
			break
		}
		n := len(pairs)
		endCount, startCount := 0, 0
		var diffs, rtts []float64
		for _, p := range pairs {
			logged := p.ev.End // the raw logged time, recovered independently of the current constant
			if !w3cTimeIsEnd {
				logged = p.ev.Start
			}
			if !logged.Before(p.end.Add(-2*time.Second)) && !logged.After(p.end.Add(1*time.Second)) {
				endCount++
			}
			if !logged.Before(p.start.Add(-2*time.Second)) && !logged.After(p.start.Add(1*time.Second)) {
				startCount++
			}
			if p.ev.DurationMs != nil {
				diffs = append(diffs, p.req.ClientTotalMs-*p.ev.DurationMs)
				rtts = append(rtts, p.req.RttMs)
			}
		}
		switch {
		case float64(endCount)/float64(n) >= 0.95:
			bStatus, bReason = VerifyConfirmed, fmt.Sprintf("%d paired requests, %d within 1 s of client end", n, endCount)
		case float64(startCount)/float64(n) >= 0.95:
			bStatus, bReason = VerifyRefuted, fmt.Sprintf("%d paired requests, %d within 1 s of client start", n, startCount)
		default:
			bStatus, bReason = VerifyUnmeasured, fmt.Sprintf("%d paired requests, %d within end window, %d within start window", n, endCount, startCount)
		}
		if len(diffs) == 0 {
			aStatus, aReason = VerifyUnmeasured, "no time-taken values among the paired rows"
			break
		}
		med := median(diffs)
		threshold := 50.0
		if rt := 2 * median(rtts); rt > threshold {
			threshold = rt
		}
		if math.Abs(med) <= threshold {
			aStatus, aReason = VerifyConfirmed, fmt.Sprintf("%d paired rows, median residual %.0f ms within %.0f ms", len(diffs), med, threshold)
		} else {
			aStatus, aReason = VerifyRefuted, fmt.Sprintf("%d paired rows, median residual %.0f ms exceeds %.0f ms", len(diffs), med, threshold)
		}
	}
	checkNote(t, Vf01a, aStatus, aReason)
	checkNote(t, Vf01b, bStatus, bReason)
}

// firstNonBlankLine returns the first non-blank line of data, CR trimmed.
func firstNonBlankLine(data []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) != "" {
			return line
		}
	}
	return ""
}

// TestVerifyMeasureHTTPERR measures VF-01c and VF-01d against any real
// HTTPERR captures found by sampleDir.
func TestVerifyMeasureHTTPERR(t *testing.T) {
	dir := sampleDir(t)
	if dir == "" {
		t.Skip("no VF-01 samples: set UNRAVIS_VF01_SAMPLES")
	}
	files, err := filepath.Glob(filepath.Join(dir, "httperr*.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no httperr*.log samples found")
	}

	withFields, total := 0, 0
	dOK, dTotal := 0, 0
	for _, f := range files {
		// Skip, rather than Fatal, on a read error: a Fatal message would
		// otherwise surface the real sample path in test output.
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		total++
		line := firstNonBlankLine(data)
		if !strings.HasPrefix(line, "#Fields:") {
			continue
		}
		withFields++
		names := strings.Fields(strings.TrimPrefix(line, "#Fields:"))
		dTotal++
		if len(names) > 0 && strings.EqualFold(names[len(names)-1], "s-queuename") {
			dOK++
		}
	}

	var cStatus VerifyStatus
	var cReason string
	switch {
	case withFields == 0:
		cStatus, cReason = VerifyConfirmed, fmt.Sprintf("0 of %d files have a #Fields header", total)
	case withFields == total:
		cStatus, cReason = VerifyRefuted, fmt.Sprintf("%d of %d files have a #Fields header", withFields, total)
	default:
		cStatus, cReason = VerifyUnmeasured, fmt.Sprintf("%d of %d files have a #Fields header", withFields, total)
	}
	checkNote(t, Vf01c, cStatus, cReason)

	var dStatus VerifyStatus
	var dReason string
	switch {
	case dTotal == 0:
		dStatus, dReason = VerifyUnmeasured, "no file carries a #Fields header to check"
	case dOK == dTotal:
		dStatus, dReason = VerifyConfirmed, fmt.Sprintf("%d of %d #Fields headers end in s-queuename", dOK, dTotal)
	case dOK == 0:
		dStatus, dReason = VerifyRefuted, fmt.Sprintf("0 of %d #Fields headers end in s-queuename", dTotal)
	default:
		dStatus, dReason = VerifyUnmeasured, fmt.Sprintf("%d of %d #Fields headers end in s-queuename", dOK, dTotal)
	}
	checkNote(t, Vf01d, dStatus, dReason)
}

// TestVerifyMeasureQuerystore measures VF-01h against any real Query Store
// export found by sampleDir, paired with paired-querystore.json.
func TestVerifyMeasureQuerystore(t *testing.T) {
	dir := sampleDir(t)
	if dir == "" {
		t.Skip("no VF-01 samples: set UNRAVIS_VF01_SAMPLES")
	}
	files, err := filepath.Glob(filepath.Join(dir, "querystore*.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no querystore*.csv samples found")
	}
	sidecar, err := os.ReadFile(filepath.Join(dir, "paired-querystore.json"))
	if err != nil {
		t.Skip("no paired-querystore.json sidecar")
	}
	var pf struct {
		Queries []struct {
			QueryID  int64   `json:"queryId"`
			ServerMs float64 `json:"serverMs"`
		} `json:"queries"`
	}
	if err := json.Unmarshal(sidecar, &pf); err != nil {
		t.Fatal("paired-querystore.json: not valid JSON in the expected shape")
	}

	avgByQuery := map[int64]float64{}
	for _, f := range files {
		// Skip, rather than Fatal, on a read error: a Fatal message would
		// otherwise surface the real sample path in test output.
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		r := csv.NewReader(bytes.NewReader(data))
		header, err := r.Read()
		if err != nil {
			continue
		}
		qiCol, adCol := -1, -1
		for i, h := range header {
			switch strings.ToLower(strings.TrimSpace(h)) {
			case "query_id":
				qiCol = i
			case "avg_duration":
				adCol = i
			}
		}
		if qiCol < 0 || adCol < 0 {
			continue
		}
		for {
			rec, err := r.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			qid, err1 := strconv.ParseInt(strings.TrimSpace(rec[qiCol]), 10, 64)
			ad, err2 := strconv.ParseFloat(strings.TrimSpace(rec[adCol]), 64)
			if err1 != nil || err2 != nil {
				continue
			}
			avgByQuery[qid] = ad
		}
	}

	var ratios []float64
	for _, q := range pf.Queries {
		ad, ok := avgByQuery[q.QueryID]
		if !ok || ad == 0 {
			continue
		}
		ratios = append(ratios, q.ServerMs/(ad/1000))
	}

	var hStatus VerifyStatus
	var hReason string
	switch {
	case len(ratios) < 10:
		hStatus, hReason = VerifyUnmeasured, fmt.Sprintf("%d matched queries (need at least 10)", len(ratios))
	default:
		med := median(ratios)
		switch {
		case med >= 0.95 && med <= 1.05:
			hStatus, hReason = VerifyConfirmed, fmt.Sprintf("%d matched queries, median ratio %.3f", len(ratios), med)
		case med >= 900 && med <= 1100, med >= 0.0009 && med <= 0.0011:
			hStatus, hReason = VerifyRefuted, fmt.Sprintf("%d matched queries, median ratio %.3f", len(ratios), med)
		default:
			hStatus, hReason = VerifyUnmeasured, fmt.Sprintf("%d matched queries, median ratio %.3f", len(ratios), med)
		}
	}
	checkNote(t, Vf01h, hStatus, hReason)
}
