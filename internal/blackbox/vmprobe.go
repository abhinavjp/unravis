package blackbox

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// BL-113 vm-probe: a read-only capture of which VMware performance counters a
// Windows host has, and what they read. It exists so the owner (or anyone with a
// VMware guest) can show the real names, units and values before the vm group
// grows (CPU steal in particular). It checks a fixed list of 18 counter names and
// nothing else, and prints names, PDH status numbers and numbers only: no text
// from the machine reaches the output except the fixed English paths below.
//
// The names come from a 2020 VMware document and are NOT verified on a real
// guest. A wrong name shows as "absent", which is itself the answer.

const (
	// VMProbeDefaultSamples and VMProbeMaxSamples bound the --samples option.
	VMProbeDefaultSamples = 5
	VMProbeMaxSamples     = 10

	vmProbeInterval = time.Second
)

// vmProbePaths is the whole list the probe may ever ask for.
var vmProbePaths = []string{
	`\VM Memory\Memory Active in MB`,
	`\VM Memory\Memory Ballooned in MB`,
	`\VM Memory\Memory Limit in MB`,
	`\VM Memory\Memory Mapped in MB`,
	`\VM Memory\Memory Overhead in MB`,
	`\VM Memory\Memory Reservation in MB`,
	`\VM Memory\Memory Shared in MB`,
	`\VM Memory\Memory Shared Saved in MB`,
	`\VM Memory\Memory Shares`,
	`\VM Memory\Memory Swapped in MB`,
	`\VM Memory\Memory Used in MB`,
	`\VM Processor\% Processor Time`,
	`\VM Processor\Effective VM Speed in MHz`,
	`\VM Processor\Host processor speed in MHz`,
	`\VM Processor\Limit in MHz`,
	`\VM Processor\Reservation in MHz`,
	`\VM Processor\Shares`,
	`\VM Processor\CPU stolen time`,
}

// vmProbeVariants lists the exact PDH paths tried, in order, for one fixed name.
// VMware Tools publishes VM Processor per vCPU plus a total, so a path without an
// instance may not exist on a real guest even though the counter does. The three
// shapes below are the only ones tried; the first that opens is used.
func vmProbeVariants(path string) []string {
	const obj = `\VM Processor\`
	if !strings.HasPrefix(path, obj) {
		return []string{path}
	}
	name := strings.TrimPrefix(path, obj)
	return []string{path, `\VM Processor(_Total)\` + name, `\VM Processor(*)\` + name}
}

// VMProbePaths returns a copy of the fixed list of counter paths.
func VMProbePaths() []string { return append([]string(nil), vmProbePaths...) }

// VMProbeOpen opens one counter on its own. A counter that does not exist is an
// error (for PDH it carries the status code). The probe closes what it opens.
type VMProbeOpen func(path string) (CounterSource, error)

// VMProbeSample is one reading of one counter. Value is nil when the number is
// not usable (not read, NaN or infinite); Status is the PDH status in hex.
type VMProbeSample struct {
	// Path is the counter path this reading came from when it differs from the
	// line's fixed path (an instance such as (_Total) or one vCPU).
	Path   string   `json:"path,omitempty"`
	Value  *float64 `json:"value"`
	Status string   `json:"status"`
	OK     bool     `json:"ok"`
}

// VMProbeLine is one counter of the fixed list.
type VMProbeLine struct {
	Path  string `json:"path"`
	Found bool   `json:"found"`
	// Variant is the instance form that opened ("" when the plain path did).
	Variant string `json:"variant,omitempty"`
	// AbsentStatus is the PDH status the add failed with, in hex. Empty when the
	// failure carried no PDH status (see Failure).
	AbsentStatus string `json:"absentStatus,omitempty"`
	// Failure is a fixed word, never machine text: "no-status" when the add failed
	// without a PDH status.
	Failure string `json:"failure,omitempty"`
	// AlsoTried are the other instance forms tried after the plain path, with
	// their PDH status in hex ("none" when the failure carried no status).
	AlsoTried []VMProbeTry    `json:"alsoTried,omitempty"`
	Samples   []VMProbeSample `json:"samples,omitempty"`
}

// VMProbeTry is one instance form that was tried and did not give a reading.
type VMProbeTry struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	// Opened is true when the form was accepted by PDH but its first read gave no
	// usable value (Status is then the status of that read).
	Opened bool `json:"opened,omitempty"`
}

// VMProbeResult is the whole capture.
type VMProbeResult struct {
	Counters []VMProbeLine `json:"counters"`
	Found    int           `json:"found"`
	Samples  int           `json:"samples"`
	// PDHUnavailable is true when no counter could be tried at all (PDH did not
	// load or open): "absent" would be a wrong answer there.
	PDHUnavailable bool `json:"pdhUnavailable,omitempty"`
}

func hex32(code uint32) string { return fmt.Sprintf("0x%X", code) }

// RunVMProbe opens each fixed counter, then reads the found ones n times,
// vmProbeInterval apart (sleep is injected by tests). n is clamped to 1..10.
func RunVMProbe(open VMProbeOpen, n int, sleep func(time.Duration)) VMProbeResult {
	if n < 1 {
		n = 1
	}
	if n > VMProbeMaxSamples {
		n = VMProbeMaxSamples
	}
	res := VMProbeResult{Samples: n}
	srcs := make([]CounterSource, len(vmProbePaths))
	defer func() {
		for _, s := range srcs {
			if s != nil {
				_ = s.Close()
			}
		}
	}()
	noStatus := 0
	for i, path := range vmProbePaths {
		line := VMProbeLine{Path: path}
		var failedNoStatus bool
		var chosen, fallback CounterSource
		chosenK, fallbackK := 0, 0
		variants := vmProbeVariants(path)
		for k, variant := range variants {
			src, err := open(variant)
			if err == nil && src != nil {
				// PDH often accepts a path without an instance on a multi-instance
				// object and only fails the read (PDH_CSTATUS_NO_INSTANCE), so a form
				// counts as found only when its first read gives a usable value.
				status, ok := firstProbeRead(src)
				if ok {
					chosen, chosenK = src, k
					break
				}
				line.AlsoTried = append(line.AlsoTried, VMProbeTry{Path: variant, Status: status, Opened: true})
				if fallback == nil {
					fallback, fallbackK = src, k // keep it: better "found, unreadable" than "absent"
				} else {
					_ = src.Close()
				}
				continue
			}
			var se *pdhStatusErr
			hasStatus := err != nil && errors.As(err, &se)
			switch {
			case k == 0 && hasStatus:
				line.AbsentStatus = hex32(se.code)
			case k == 0:
				line.Failure = "no-status"
				failedNoStatus = true
			case hasStatus:
				line.AlsoTried = append(line.AlsoTried, VMProbeTry{Path: variant, Status: hex32(se.code)})
			default:
				line.AlsoTried = append(line.AlsoTried, VMProbeTry{Path: variant, Status: "none"})
			}
		}
		if chosen == nil && fallback != nil {
			chosen, chosenK = fallback, fallbackK
			fallback = nil
		}
		if fallback != nil {
			_ = fallback.Close()
		}
		if chosen != nil {
			line.Found = true
			if chosenK > 0 {
				line.Variant = variants[chosenK]
			}
			srcs[i] = chosen
			res.Found++
			line.AbsentStatus, line.Failure = "", ""
			kept := line.AlsoTried[:0]
			for _, t := range line.AlsoTried {
				if t.Opened { // forms that opened but could not be read stay on the line
					kept = append(kept, t)
				}
			}
			line.AlsoTried = kept
			if len(line.AlsoTried) == 0 {
				line.AlsoTried = nil
			}
		} else if failedNoStatus {
			noStatus++
		}
		res.Counters = append(res.Counters, line)
	}
	res.PDHUnavailable = noStatus == len(vmProbePaths)
	for k := 0; k < n && res.Found > 0; k++ {
		sleep(vmProbeInterval)
		for i := range res.Counters {
			if srcs[i] == nil {
				continue
			}
			res.Counters[i].Samples = append(res.Counters[i].Samples, readProbeSamples(srcs[i], vmProbePaths[i])...)
		}
	}
	return res
}

// maxProbeInstances bounds how many instances (vCPUs) one round may report.
const maxProbeInstances = 256

// firstProbeRead takes the verifying read of a freshly opened form: ok when at
// least one reading is usable. status is the first reading's PDH status in hex.
func firstProbeRead(src CounterSource) (status string, ok bool) {
	readings, err := src.Collect()
	if err != nil || len(readings) == 0 {
		return "none", false
	}
	for _, r := range readings {
		if r.OK && CounterStatusOK(r.Status) && !math.IsNaN(r.Value) && !math.IsInf(r.Value, 0) {
			return hex32(r.Status), true
		}
	}
	return hex32(readings[0].Status), false
}

func readProbeSamples(src CounterSource, base string) []VMProbeSample {
	readings, err := src.Collect()
	if err != nil || len(readings) == 0 {
		return []VMProbeSample{{Status: "none"}}
	}
	if len(readings) > maxProbeInstances {
		readings = readings[:maxProbeInstances]
	}
	out := make([]VMProbeSample, 0, len(readings))
	for _, r := range readings {
		s := VMProbeSample{Status: hex32(r.Status), OK: r.OK}
		if r.Path != base {
			s.Path = r.Path
		}
		if !math.IsNaN(r.Value) && !math.IsInf(r.Value, 0) {
			v := r.Value
			s.Value = &v
		} else {
			s.OK = false
		}
		out = append(out, s)
	}
	return out
}

// FormatVMProbeText renders the capture, one line per counter.
func FormatVMProbeText(r VMProbeResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "VMware counter probe (read-only): %d fixed counters, %d samples 1 s apart\n", len(r.Counters), r.Samples)
	for _, c := range r.Counters {
		switch {
		case c.Found:
			parts := make([]string, 0, len(c.Samples))
			for _, s := range c.Samples {
				v := "n/a"
				if s.Value != nil {
					v = strconv.FormatFloat(*s.Value, 'g', -1, 64)
				}
				if s.Path != "" {
					v = s.Path + "=" + v
				}
				parts = append(parts, fmt.Sprintf("%s [%s]", v, s.Status))
			}
			fmt.Fprintf(&b, "%s: found; %s%s\n", c.Path, strings.Join(parts, ", "), foundNote(c))
		case c.AbsentStatus != "":
			word := "failed"
			if code, err := strconv.ParseUint(strings.TrimPrefix(c.AbsentStatus, "0x"), 16, 32); err == nil && pdhAbsentStatus(uint32(code)) {
				word = "absent"
			}
			also := alsoTriedText(c.AlsoTried)
			fmt.Fprintf(&b, "%s: %s (PDH status %s%s)\n", c.Path, word, c.AbsentStatus, also)
		default:
			fmt.Fprintf(&b, "%s: not read (no PDH status)\n", c.Path)
		}
	}
	switch {
	case r.PDHUnavailable:
		b.WriteString("Performance counters (PDH) could not be opened on this host, so nothing was checked.\n")
	case r.Found == 0:
		b.WriteString("No VMware counters on this host.\n")
	default:
		fmt.Fprintf(&b, "Found %d of %d VMware counters.\n", r.Found, len(r.Counters))
	}
	return b.String()
}

func alsoTriedText(tries []VMProbeTry) string {
	var b strings.Builder
	for _, t := range tries {
		if t.Opened {
			fmt.Fprintf(&b, "; %s: opened, read status %s", t.Path, t.Status)
		} else {
			fmt.Fprintf(&b, "; %s: %s", t.Path, t.Status)
		}
	}
	return b.String()
}

// foundNote says which form gave the value when it was not the plain path, and
// which forms opened but could not be read.
func foundNote(c VMProbeLine) string {
	if c.Variant == "" && len(c.AlsoTried) == 0 {
		return ""
	}
	note := ""
	if c.Variant != "" {
		note = " (read through " + c.Variant + ")"
	}
	if len(c.AlsoTried) > 0 {
		note += " (not readable" + alsoTriedText(c.AlsoTried) + ")"
	}
	return note
}
