package server

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/casestore"
)

type bbBinaryPointRef struct {
	N int `json:"n"`
}

type bbBinaryHeader struct {
	BucketSeconds int `json:"bucketSeconds"`
	Lines         []struct {
		Key    string           `json:"key"`
		Points bbBinaryPointRef `json:"points"`
	} `json:"lines"`
	Processes []struct {
		Name string           `json:"name"`
		CPU  bbBinaryPointRef `json:"cpu"`
		WS   bbBinaryPointRef `json:"workingSet"`
		Priv bbBinaryPointRef `json:"privateBytes"`
	} `json:"processes"`
}

func decodeBBSeriesBinary(t *testing.T, body []byte) bbM4Answer {
	t.Helper()
	if len(body) < bbSeriesBinaryPrefix {
		t.Fatalf("short binary body: %d", len(body))
	}
	if string(body[:4]) != "UBB1" || binary.LittleEndian.Uint16(body[4:6]) != 1 {
		t.Fatalf("frame starts %q version %d, want UBB1 version 1", body[:4], binary.LittleEndian.Uint16(body[4:6]))
	}
	head := int(binary.LittleEndian.Uint32(body[6:10]))
	if head > len(body)-bbSeriesBinaryPrefix || (len(body)-bbSeriesBinaryPrefix-head)%bbSeriesBinaryCell != 0 {
		t.Fatalf("bad binary lengths: header %d, body %d", head, len(body))
	}
	var meta bbBinaryHeader
	if err := json.Unmarshal(body[bbSeriesBinaryPrefix:bbSeriesBinaryPrefix+head], &meta); err != nil {
		t.Fatal(err)
	}
	at := bbSeriesBinaryPrefix + head
	read := func(n int) [][]float64 {
		points := make([][]float64, n)
		for i := range points {
			points[i] = make([]float64, bbSeriesBinaryColumns)
			for j := range points[i] {
				points[i][j] = math.Float64frombits(binary.LittleEndian.Uint64(body[at+j*8:]))
			}
			at += bbSeriesBinaryCell
		}
		return points
	}
	out := bbM4Answer{BucketSeconds: meta.BucketSeconds, Lines: make([]struct {
		Key    string      `json:"key"`
		Points [][]float64 `json:"points"`
	}, len(meta.Lines))}
	for i, line := range meta.Lines {
		out.Lines[i].Key = line.Key
		out.Lines[i].Points = read(line.Points.N)
	}
	out.Processes = make([]struct {
		Name string      `json:"name"`
		CPU  [][]float64 `json:"cpu"`
		WS   [][]float64 `json:"workingSet"`
	}, len(meta.Processes))
	for i, proc := range meta.Processes {
		out.Processes[i].Name = proc.Name
		out.Processes[i].CPU = read(proc.CPU.N)
		out.Processes[i].WS = read(proc.WS.N)
		read(proc.Priv.N)
	}
	if at != len(body) {
		t.Fatalf("binary body has %d trailing bytes", len(body)-at)
	}
	return out
}

func TestBlackboxSeriesBinaryMatchesM4JSON(t *testing.T) {
	start := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
	samples := []blackbox.Sample{fullSample(start, 1), fullSample(start.Add(30*time.Second), 2), fullSample(start.Add(time.Minute), 3)}
	answer := buildBBSeriesShape(blackbox.Manifest{IntervalSeconds: 30}, samples, blackbox.ReadReport{}, blackbox.StateInfo{}, bbWindow{Start: start, End: start.Add(2 * time.Minute)}, 3, true, true)
	jsonBody, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	var want bbM4Answer
	if err := json.Unmarshal(jsonBody, &want); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	writeBBSeriesBinary(rec, answer)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != bbSeriesBinaryType || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("binary response = %d %v", rec.Code, rec.Header())
	}
	if got := decodeBBSeriesBinary(t, rec.Body.Bytes()); !reflect.DeepEqual(got, want) {
		t.Fatalf("binary M4 answer differs from JSON:\n got: %#v\nwant: %#v", got, want)
	}

	empty := buildBBSeriesShape(blackbox.Manifest{IntervalSeconds: 30}, nil, blackbox.ReadReport{}, blackbox.StateInfo{}, bbWindow{Start: start, End: start.Add(time.Minute)}, 2, true, true)
	rec = httptest.NewRecorder()
	writeBBSeriesBinary(rec, empty)
	if got := decodeBBSeriesBinary(t, rec.Body.Bytes()); len(got.Lines) != 0 || len(got.Processes) != 0 {
		t.Fatalf("empty binary answer = %#v", got)
	}
}

func TestBlackboxSeriesBinaryRouteRequiresM4AndAccept(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put(share, bbRec("web-b", nil))
	it := b.mustAdd(share, "")
	win := `"start":"` + invT0.Add(-30*time.Minute).Format(time.RFC3339) + `","end":"` + invT0.Add(3*time.Hour).Format(time.RFC3339) + `"`
	path := "/api/blackbox/" + it.ID + "/series"
	for _, tc := range []struct {
		name, extra, accept, contentType string
	}{
		{"binary m4", `,"width":60,"shape":"m4"`, bbSeriesBinaryType, bbSeriesBinaryType},
		{"json without accept", `,"width":60,"shape":"m4"`, "", "application/json"},
		{"json without m4", `,"width":60`, bbSeriesBinaryType, "application/json"},
		{"json for a wildcard accept", `,"width":60,"shape":"m4"`, "*/*", "application/json"},
		{"json for another type", `,"width":60,"shape":"m4"`, "application/json", "application/json"},
		{"binary when the type is second in a list", `,"width":60,"shape":"m4"`, "application/json, " + bbSeriesBinaryType, bbSeriesBinaryType},
		{"binary with a q-value", `,"width":60,"shape":"m4"`, bbSeriesBinaryType + ";q=0.9, */*;q=0.1", bbSeriesBinaryType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{` + win + tc.extra + `}`
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
			req.Header.Set("X-Audit-Token", testToken)
			req.Header.Set("Content-Type", "application/json")
			if tc.accept != "" {
				req.Header.Set("Accept", tc.accept)
			}
			rec := b.e.serve(req)
			if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), tc.contentType) {
				t.Fatalf("response = %d %v %s; request %s", rec.Code, rec.Header(), rec.Body, body)
			}
			if tc.contentType == bbSeriesBinaryType {
				decodeBBSeriesBinary(t, rec.Body.Bytes())
			}
		})
	}
}

func TestBlackboxSeriesBinaryRefusesAnAnswerOverTheLimit(t *testing.T) {
	// (maxResponseBytes - prefix) / cell, plus one: the payload alone no longer fits.
	cells := maxResponseBytes/bbSeriesBinaryCell + 1
	rec := httptest.NewRecorder()
	writeBBSeriesBinary(rec, bbSeriesAnswer{Lines: []bbSeriesLine{{Key: "cpu", M4: make([]bbSeriesPointM4, cells)}}})
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("too many points = %d %v", rec.Code, rec.Header())
	}

	// A header over its own 1 MiB cap is a server fault, still JSON, never a frame.
	rec = httptest.NewRecorder()
	writeBBSeriesBinary(rec, bbSeriesAnswer{Lines: []bbSeriesLine{{Key: strings.Repeat("k", seriesBinaryHeadCap+1)}}})
	if rec.Code != http.StatusInternalServerError || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("oversize header = %d %v", rec.Code, rec.Header())
	}

	// Just under the limit still answers.
	rec = httptest.NewRecorder()
	writeBBSeriesBinary(rec, bbSeriesAnswer{Lines: []bbSeriesLine{{Key: "cpu", M4: make([]bbSeriesPointM4, 1000)}}})
	if rec.Code != http.StatusOK {
		t.Fatalf("small answer = %d", rec.Code)
	}
}

func TestCaseSeriesBinaryRefusesAnAnswerOverTheLimit(t *testing.T) {
	cells := maxResponseBytes/seriesBinaryCellSize + 1
	rec := httptest.NewRecorder()
	writeSeriesBinary(rec, casestore.SeriesResult{Series: []casestore.SeriesOut{{Sel: "kind=toy", Points: make([]casestore.SeriesPoint, cells)}}})
	if rec.Code != http.StatusRequestEntityTooLarge || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("too many points = %d %v", rec.Code, rec.Header())
	}

	rec = httptest.NewRecorder()
	writeSeriesBinary(rec, casestore.SeriesResult{Series: []casestore.SeriesOut{{Sel: strings.Repeat("k", seriesBinaryHeadCap+1)}}})
	if rec.Code != http.StatusInternalServerError || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("oversize header = %d %v", rec.Code, rec.Header())
	}

	rec = httptest.NewRecorder()
	writeSeriesBinary(rec, casestore.SeriesResult{Series: []casestore.SeriesOut{{Sel: "kind=toy", Points: make([]casestore.SeriesPoint, 10)}}})
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("small answer = %d %v", rec.Code, rec.Header())
	}
}
