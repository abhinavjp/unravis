package server

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"time"

	"api-audit-explorer/internal/casestore"
)

const (
	seriesBinaryMagic    = "URS1"
	seriesBinaryVersion  = 1
	seriesBinaryColumns  = 8
	seriesBinaryHeadCap  = 1 << 20
	seriesBinaryCellSize = seriesBinaryColumns * 8
)

type seriesBinaryHeader struct {
	Version  int                  `json:"version"`
	From     time.Time            `json:"from"`
	To       time.Time            `json:"to"`
	BucketMs int64                `json:"bucketMs"`
	Buckets  int                  `json:"buckets"`
	Width    int                  `json:"width,omitempty"`
	Partial  bool                 `json:"partial"`
	Scanned  int64                `json:"scanned"`
	Series   []seriesBinarySeries `json:"series"`
}

type seriesBinarySeries struct {
	Sel       string                `json:"sel"`
	Unit      string                `json:"unit"`
	UnitMixed bool                  `json:"unitMixed"`
	Count     int64                 `json:"count"`
	Points    int                   `json:"points"`
	Offset    int64                 `json:"offset"`
	ByClass   []seriesBinaryByClass `json:"byClass,omitempty"`
}

type seriesBinaryByClass struct {
	Point  int              `json:"point"`
	Counts map[string]int64 `json:"counts"`
}

func writeSeriesBinary(w http.ResponseWriter, res casestore.SeriesResult) {
	header := seriesBinaryHeader{
		Version: seriesBinaryVersion, From: res.From, To: res.To, BucketMs: res.BucketMs,
		Buckets: res.Buckets, Width: res.Width, Partial: res.Partial, Scanned: res.Scanned, Series: make([]seriesBinarySeries, len(res.Series)),
	}
	var offset int64
	for i, item := range res.Series {
		encoded := seriesBinarySeries{Sel: item.Sel, Unit: item.Unit, UnitMixed: item.UnitMixed, Count: item.Count, Points: len(item.Points), Offset: offset}
		for p, point := range item.Points {
			if point.ByClass != nil {
				encoded.ByClass = append(encoded.ByClass, seriesBinaryByClass{Point: p, Counts: point.ByClass})
			}
		}
		header.Series[i] = encoded
		offset += int64(len(item.Points))
	}
	metadata, err := json.Marshal(header)
	if err != nil || len(metadata) > seriesBinaryHeadCap {
		writeError(w, 500, "Internal server error.")
		return
	}
	padded := (len(metadata) + 7) &^ 7
	payloadBytes := offset * seriesBinaryCellSize
	if padded > maxResponseBytes-12 || payloadBytes > maxResponseBytes-12-int64(padded) {
		writeError(w, 413, "Response exceeds a configured limit.")
		return
	}
	body := make([]byte, 8+padded+int(payloadBytes))
	copy(body, seriesBinaryMagic)
	binary.LittleEndian.PutUint32(body[4:8], uint32(padded))
	copy(body[8:], metadata)
	for i := len(metadata); i < padded; i++ {
		body[8+i] = ' '
	}
	at := 8 + padded
	for _, item := range res.Series {
		for _, point := range item.Points {
			values := [seriesBinaryColumns]float64{float64(point.T), point.Min, point.Mean, point.Max, float64(point.Count), math.NaN(), math.NaN(), math.NaN()}
			if point.First != nil {
				values[5] = *point.First
			}
			if point.Last != nil {
				values[6] = *point.Last
			}
			if point.Q != nil {
				values[7] = *point.Q
			}
			for _, value := range values {
				binary.LittleEndian.PutUint64(body[at:at+8], math.Float64bits(value))
				at += 8
			}
		}
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(200)
	_, _ = w.Write(body)
}
