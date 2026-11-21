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
// grows (CPU steal in particular). It checks a fixed list of 17 counter names and
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

// VMProbePaths returns a copy of the fixed list of counter paths.
func VMProbePaths() []string { return append([]string(nil), vmProbePaths...) }

// VMProbeOpen opens one counter on its own. A counter that does not exist is an
// error (for PDH it carries the status code). The probe closes what it opens.
type VMProbeOpen func(path string) (CounterSource, error)

// VMProbeSample is one reading of one counter. Value is nil when the number is
// not usable (not read, NaN or infinite); Status is the PDH status in hex.
type VMProbeSample struct {
	Value  *float64 `json:"value"`
	Status string   `json:"status"`
	OK     bool     `json:"ok"`
}

// VMProbeLine is one counter of the fixed list.
type VMProbeLine struct {
	Path  string `json:"path"`
	Found bool   `json:"found"`
	// AbsentStatus is the PDH status the add failed with, in hex. Empty when the
	// failure carried no PDH status (see Failure).
	AbsentStatus string `json:"absentStatus,omitempty"`
	// Failure is a fixed word, never machine text: "no-status" when the add failed
	// without a PDH status.
	Failure string          `json:"failure,omitempty"`
	Samples []VMProbeSample `json:"samples,omitempty"`
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
		src, err := open(path)
		switch {
		case err == nil && src != nil:
			line.Found = true
			srcs[i] = src
			res.Found++
		default:
			var se *pdhStatusErr
			if err != nil && errors.As(err, &se) {
				line.AbsentStatus = hex32(se.code)
			} else {
				line.Failure = "no-status"
				noStatus++
			}
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
			res.Counters[i].Samples = append(res.Counters[i].Samples, readProbeSample(srcs[i], vmProbePaths[i]))
		}
	}
	return res
}

func readProbeSample(src CounterSource, path string) VMProbeSample {
	readings, err := src.Collect()
	if err != nil {
		return VMProbeSample{Status: "none"}
	}
	for _, r := range readings {
		if r.Path != path {
			continue
		}
		s := VMProbeSample{Status: hex32(r.Status), OK: r.OK}
		if !math.IsNaN(r.Value) && !math.IsInf(r.Value, 0) {
			v := r.Value
			s.Value = &v
		} else {
			s.OK = false
		}
		return s
	}
	return VMProbeSample{Status: "none"}
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
				parts = append(parts, fmt.Sprintf("%s [%s]", v, s.Status))
			}
			fmt.Fprintf(&b, "%s: found; %s\n", c.Path, strings.Join(parts, ", "))
		case c.AbsentStatus != "":
			fmt.Fprintf(&b, "%s: absent (PDH status %s)\n", c.Path, c.AbsentStatus)
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
