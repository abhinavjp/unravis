package perfdata

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ManifestVersion changes when the manifest layout changes.
const ManifestVersion = 1

// ManifestName is the file name inside the output folder.
const ManifestName = "manifest.json"

// Totals are row counts per lane.
type Totals struct {
	IIS int64 `json:"iis"`
	SQL int64 `json:"sql"`
}

// DayCount is the truth of one UTC day: the rows emitted into the case.
type DayCount struct {
	Day         string `json:"day"` // YYYY-MM-DD
	IIS         int64  `json:"iis"`
	SQL         int64  `json:"sql"`
	Errors5xxIS int64  `json:"errors5xxIis"`
}

// HourCount is the same for one UTC hour; only non-empty hours are listed.
type HourCount struct {
	Hour        string `json:"hour"` // RFC3339, UTC, on the hour
	IIS         int64  `json:"iis"`
	SQL         int64  `json:"sql"`
	Errors5xxIS int64  `json:"errors5xxIis"`
}

// Busiest is the hour with the most IIS rows.
type Busiest struct {
	Hour string `json:"hour"`
	IIS  int64  `json:"iis"`
}

// Manifest is what manifest.json holds. It has no clock reading, so two runs of
// the same Config differ only in CaseID.
type Manifest struct {
	Version     int         `json:"version"`
	Seed        uint64      `json:"seed"`
	Preset      string      `json:"preset"`
	CaseID      string      `json:"caseId"`
	CaseTitle   string      `json:"caseTitle"`
	WindowStart time.Time   `json:"windowStart"`
	WindowEnd   time.Time   `json:"windowEnd"`
	ChunkDays   int         `json:"chunkDays"`
	Totals      Totals      `json:"totals"`
	PerDay      []DayCount  `json:"perDay"`
	PerHour     []HourCount `json:"perHour"`
	Bursts      []Burst     `json:"bursts"`
	Outages     []Outage    `json:"outages"`
	BusiestHour Busiest     `json:"busiestHour"`
}

// Lane names for the sum helpers.
const (
	LaneIIS = "iis"
	LaneSQL = "sql"
)

// Save writes manifest.json into dir.
func (m Manifest) Save(dir string) error {
	b, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, ManifestName), b, 0o644)
}

// LoadManifest reads manifest.json from dir.
func LoadManifest(dir string) (Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return Manifest{}, err
	}
	if m.Version != ManifestVersion {
		return Manifest{}, fmt.Errorf("manifest version %d is not supported", m.Version)
	}
	return m, nil
}

func pick(lane string, iis, sql int64) int64 {
	if lane == LaneSQL {
		return sql
	}
	return iis
}

// SumDays is the rows of a lane on the UTC days from..to-1 (both at midnight).
func (m Manifest) SumDays(lane string, from, to time.Time) (int64, error) {
	if !onDay(from) || !onDay(to) {
		return 0, errors.New("SumDays needs UTC midnight bounds")
	}
	var n int64
	for _, d := range m.PerDay {
		t, err := time.Parse("2006-01-02", d.Day)
		if err != nil {
			return 0, err
		}
		if !t.Before(from) && t.Before(to) {
			n += pick(lane, d.IIS, d.SQL)
		}
	}
	return n, nil
}

func onDay(t time.Time) bool { return t.UTC().Equal(t.UTC().Truncate(24 * time.Hour)) }

// Sum is the rows of a lane in [from, to) where both bounds are on a whole UTC
// hour. errors counts the IIS 5xx rows (the lane argument is ignored for it).
func (m Manifest) Sum(lane string, from, to time.Time) (rows, errors5xx int64, err error) {
	if from.UTC().Truncate(time.Hour) != from.UTC() || to.UTC().Truncate(time.Hour) != to.UTC() {
		return 0, 0, errors.New("Sum needs whole-hour bounds")
	}
	for _, h := range m.PerHour {
		t, perr := time.Parse(time.RFC3339, h.Hour)
		if perr != nil {
			return 0, 0, perr
		}
		if !t.Before(from) && t.Before(to) {
			rows += pick(lane, h.IIS, h.SQL)
			errors5xx += h.Errors5xxIS
		}
	}
	return rows, errors5xx, nil
}
