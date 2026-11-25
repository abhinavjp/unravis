package server

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
)

const (
	bbSeriesBinaryType    = "application/vnd.unravis.series+f64"
	bbSeriesBinaryColumns = 6
	bbSeriesBinaryCell    = bbSeriesBinaryColumns * 8
	// The frame starts with a 4-byte magic and a u16 version, so a reader refuses a frame whose layout it does not know.
	bbSeriesBinaryMagic   = "UBB1"
	bbSeriesBinaryVersion = 1
	// magic (4) + version (2) + header length (4)
	bbSeriesBinaryPrefix = 10
)

func writeBBSeriesBinary(w http.ResponseWriter, answer bbSeriesAnswer) {
	answer.Lines = append([]bbSeriesLine(nil), answer.Lines...)
	for i := range answer.Lines {
		answer.Lines[i].Binary = true
	}
	answer.Processes = append([]bbSeriesProc(nil), answer.Processes...)
	for i := range answer.Processes {
		answer.Processes[i].Binary = true
	}
	metadata, err := json.Marshal(answer)
	if err != nil || len(metadata) > seriesBinaryHeadCap {
		writeError(w, http.StatusInternalServerError, "Internal server error.")
		return
	}
	var cells int64
	for _, line := range answer.Lines {
		cells += int64(len(line.Points) + len(line.M4))
	}
	for _, proc := range answer.Processes {
		cells += int64(len(proc.CPU) + len(proc.M4CPU) + len(proc.WS) + len(proc.M4WS) + len(proc.Priv) + len(proc.M4Priv))
	}
	payloadBytes := cells * bbSeriesBinaryCell
	if int64(len(metadata)) > maxResponseBytes-bbSeriesBinaryPrefix || payloadBytes > maxResponseBytes-bbSeriesBinaryPrefix-int64(len(metadata)) {
		writeError(w, http.StatusRequestEntityTooLarge, "Response exceeds a configured limit.")
		return
	}
	body := make([]byte, bbSeriesBinaryPrefix+len(metadata)+int(payloadBytes))
	copy(body, bbSeriesBinaryMagic)
	binary.LittleEndian.PutUint16(body[4:6], bbSeriesBinaryVersion)
	binary.LittleEndian.PutUint32(body[6:10], uint32(len(metadata))) //nolint:gosec // len(metadata) <= seriesBinaryHeadCap (1 MiB), checked above
	copy(body[bbSeriesBinaryPrefix:], metadata)
	at := bbSeriesBinaryPrefix + len(metadata)
	putPoint := func(point [bbSeriesBinaryColumns]float64) {
		for _, value := range point {
			binary.LittleEndian.PutUint64(body[at:at+8], math.Float64bits(value))
			at += 8
		}
	}
	// BL-430's live M4 shape keeps the bucket average as well as first/last.
	for _, line := range answer.Lines {
		for _, point := range line.Points {
			putPoint([bbSeriesBinaryColumns]float64{point[0], point[1], point[2], point[3], point[1], point[1]})
		}
		for _, point := range line.M4 {
			putPoint(point)
		}
	}
	for _, proc := range answer.Processes {
		for _, points := range [][]bbSeriesPointM4{proc.M4CPU, proc.M4WS, proc.M4Priv} {
			for _, point := range points {
				putPoint(point)
			}
		}
		for _, points := range [][]bbSeriesPoint{proc.CPU, proc.WS, proc.Priv} {
			for _, point := range points {
				putPoint([bbSeriesBinaryColumns]float64{point[0], point[1], point[2], point[3], point[1], point[1]})
			}
		}
	}
	w.Header().Set("Content-Type", bbSeriesBinaryType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
