package blackbox

import (
	"errors"
	"fmt"
	"math"
	"time"
)

// PageState is what a Blackbox is doing, as the page shows it (decision A36). Exactly
// one state is shown for a Blackbox at a time.
type PageState string

// The nine states of A36.
const (
	SRunning      PageState = "running"
	SRunningLate  PageState = "running-late"
	SStopped      PageState = "stopped"
	SDiskLow      PageState = "disk-low"
	SClockDiffers PageState = "clock-differs"
	SNotFound     PageState = "not-found"
	SUnreachable  PageState = "unreachable"
	SIncomplete   PageState = "incomplete"
	SNotPlaced    PageState = "not-placed"
)

// AllStates lists the states in the order the page documents them.
func AllStates() []PageState {
	return []PageState{SRunning, SRunningLate, SStopped, SDiskLow, SClockDiffers,
		SNotFound, SUnreachable, SIncomplete, SNotPlaced}
}

// Limits of A36.
const (
	// DiskLowPercent: free space below this is Disk low.
	DiskLowPercent = 10.0
	// ClockSkewLimit: a recorder clock that differs from this PC by more than this
	// is Clock differs.
	ClockSkewLimit = time.Second
)

// A5 status words, repeated here so a state can say how usable the source is.
const (
	WordReady         = "Ready"
	WordPartlyReady   = "Partly ready"
	WordCannotTestYet = "Cannot test yet"
)

// Tones the page turns into sticker colours.
const (
	ToneGood = "good"
	ToneWarn = "warn"
	ToneBad  = "bad"
	ToneIdle = "idle"
)

// StateInfo is one state with the words that go with it.
type StateInfo struct {
	State PageState `json:"state"`
	Label string    `json:"label"` // the sticker
	Tone  string    `json:"tone"`
	// Detail is one plain sentence about why.
	Detail string `json:"detail"`
	// Readiness is the fixed A5 word for using this Blackbox as a source now.
	Readiness string `json:"readiness"`
	// Reason refines Unreachable: "key" (read key refused), "wait" (too many wrong
	// keys), "busy", "newer-format", "share-dropped", "timeout"; empty otherwise.
	Reason string `json:"reason,omitempty"`
	// SkewSeconds is the recorder clock minus this PC's clock, when both are known.
	SkewSeconds *float64 `json:"skewSeconds,omitempty"`
}

// StateInput is everything the state depends on.
type StateInput struct {
	// Placed is false when no Blackbox has been put on the PC the entry stands for
	// (a this-PC entry whose service and folder are not there).
	Placed bool
	// Err is the failure of the read, if it failed.
	Err error
	// Reading is the read, when it worked.
	Reading *Reading
	// Report is the report of the samples read, when samples were read.
	Report *ReadReport
}

var stateLabels = map[PageState]string{
	SRunning:      "Running",
	SRunningLate:  "Running late",
	SStopped:      "Stopped",
	SDiskLow:      "Disk low",
	SClockDiffers: "Clock differs",
	SNotFound:     "Not found",
	SUnreachable:  "Unreachable",
	SIncomplete:   "Incomplete",
	SNotPlaced:    "Not placed",
}

// Label is the sticker word of a state.
func (s PageState) Label() string { return stateLabels[s] }

func info(s PageState, tone, readiness, detail string) StateInfo {
	return StateInfo{State: s, Label: s.Label(), Tone: tone, Readiness: readiness, Detail: detail}
}

// recordingFileCount says how many recording files, with correct singular/plural
// wording: "1 recording file" vs "N recording files".
func recordingFileCount(n int) string {
	if n == 1 {
		return "1 recording file"
	}
	return fmt.Sprintf("%d recording files", n)
}

// DeriveState decides the one state to show. The order is: nothing placed, the
// read failed, part of the data was not read, stopped, paused for disk, late,
// disk low, clock differs, running. The first that fits wins, so the most serious
// cause is the one shown.
func DeriveState(in StateInput) StateInfo {
	if !in.Placed {
		return info(SNotPlaced, ToneIdle, WordCannotTestYet, "No Blackbox has been placed on this PC yet. Place one to record the host while a slowdown happens.")
	}
	if in.Err != nil {
		return errState(in.Err)
	}
	if in.Reading == nil {
		return info(SUnreachable, ToneBad, WordCannotTestYet, "The Blackbox has not been read yet.")
	}
	r := in.Reading
	m := r.Manifest
	if in.Report != nil && in.Report.Incomplete() {
		n := len(in.Report.Dropped) + len(in.Report.Missing)
		return info(SIncomplete, ToneWarn, WordPartlyReady,
			fmt.Sprintf("%s could not be read to the end. What was read is used; the rest counts as a gap, never as zero.", recordingFileCount(n)))
	}

	interval := m.IntervalSeconds
	if interval <= 0 {
		interval = DefaultIntervalSecond
	}
	// The recorder's clock is the one that dated the samples, so lateness is judged
	// on it when it is known.
	clock := r.ReadAt
	if !r.ServerTime.IsZero() {
		clock = r.ServerTime
	}
	var skew *float64
	if !r.ServerTime.IsZero() && !r.ReadAt.IsZero() {
		d := r.ServerTime.Sub(r.ReadAt).Seconds()
		skew = &d
	}
	with := func(si StateInfo) StateInfo { si.SkewSeconds = skew; return si }

	if m.State == ManifestStopped {
		return with(info(SStopped, ToneIdle, WordPartlyReady, "The Blackbox was stopped. Its recordings are kept and can still be read; nothing new is recorded."))
	}
	if pausedForDisk(m) {
		return with(info(SDiskLow, ToneBad, WordPartlyReady, "The Blackbox paused recording because the drive is almost full. Free some space on that drive."))
	}
	if late, age := isLate(m, clock, interval); late {
		return with(info(SRunningLate, ToneWarn, WordPartlyReady,
			fmt.Sprintf("No sample for %s, which is more than %d intervals. The recorder may be stuck or the PC was busy.", roundAge(age), LateIntervals)))
	}
	if r.Status != nil && r.Status.FreePercent != nil && *r.Status.FreePercent < DiskLowPercent {
		return with(info(SDiskLow, ToneWarn, WordPartlyReady,
			fmt.Sprintf("The recordings drive has %.0f%% free, under %.0f%%. The Blackbox will pause when it runs out of room.", *r.Status.FreePercent, DiskLowPercent)))
	}
	if skew != nil && math.Abs(*skew) > ClockSkewLimit.Seconds() {
		return with(info(SClockDiffers, ToneWarn, WordPartlyReady,
			fmt.Sprintf("The Blackbox clock differs from this PC by %.1f seconds. Times from it are shifted by that much until the clocks agree.", math.Abs(*skew))))
	}
	detail := "Recording every " + fmt.Sprint(interval) + " seconds."
	if r.Status == nil {
		detail += " The clock was not compared: a folder or share has no second clock."
	}
	return with(info(SRunning, ToneGood, WordReady, detail))
}

// errState maps a failed read to its state.
func errState(err error) StateInfo {
	var re *ReadError
	if !errors.As(err, &re) {
		return info(SUnreachable, ToneBad, WordCannotTestYet, "The Blackbox could not be read.")
	}
	switch re.Kind {
	case ReadNotFound:
		return info(SNotFound, ToneBad, WordCannotTestYet, re.Message)
	case ReadKeyRefused:
		si := info(SUnreachable, ToneBad, WordCannotTestYet, re.Message)
		si.Reason = "key"
		return si
	case ReadThrottled:
		si := info(SUnreachable, ToneBad, WordCannotTestYet, re.Message)
		si.Reason = "wait"
		return si
	case ReadBusy:
		si := info(SUnreachable, ToneWarn, WordCannotTestYet, re.Message)
		si.Reason = "busy"
		return si
	case ReadNewerFormat:
		si := info(SUnreachable, ToneBad, WordCannotTestYet, re.Message)
		si.Reason = "newer-format"
		return si
	}
	si := info(SUnreachable, ToneBad, WordCannotTestYet, re.Message)
	si.Reason = "timeout"
	return si
}

// pausedForDisk is true when the newest of the pause and resume events is a pause.
func pausedForDisk(m Manifest) bool {
	for i := len(m.Events) - 1; i >= 0; i-- {
		switch m.Events[i].Kind {
		case EventPausedDiskLow:
			return true
		case EventResumed:
			return false
		}
	}
	return false
}

// isLate says whether the newest sample is more than LateIntervals intervals
// older than clock, and how old it is. A recorder with no sample yet is late only
// once it has had that long since it started.
func isLate(m Manifest, clock time.Time, interval int) (bool, time.Duration) {
	limit := time.Duration(LateIntervals*interval) * time.Second
	var ref time.Time
	switch {
	case m.LastSample != nil:
		ref = *m.LastSample
	case len(m.Runs) > 0:
		ref = m.Runs[len(m.Runs)-1].Start
	default:
		ref = m.Created
	}
	if ref.IsZero() {
		return false, 0
	}
	age := clock.Sub(ref)
	return age > limit, age
}

func roundAge(d time.Duration) string {
	switch {
	case d < 2*time.Minute:
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	case d < 2*time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	}
}
