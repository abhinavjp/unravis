package blackbox

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DiagFinding is one thing the log shows, in plain words.
type DiagFinding struct {
	Code   string `json:"code"`
	Cause  bool   `json:"cause"` // true: can make the recorder late; false: only worth knowing
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// DiagReport is what the log says about "Running late".
type DiagReport struct {
	Headline string        `json:"headline"`
	Findings []DiagFinding `json:"findings"`
	Lines    int           `json:"lines"`
	Newest   *time.Time    `json:"newest,omitempty"`
}

// diagLine is one parsed log line.
type diagLine struct {
	T    time.Time
	Kind string
	KV   map[string]string
}

func parseDiag(text string) []diagLine {
	var out []diagLine
	for _, ln := range strings.Split(text, "\n") {
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, f[0])
		if err != nil {
			continue
		}
		l := diagLine{T: t.UTC(), Kind: f[1], KV: map[string]string{}}
		for _, p := range f[2:] {
			if k, v, ok := strings.Cut(p, "="); ok {
				l.KV[k] = v
			}
		}
		out = append(out, l)
	}
	return out
}

func (l diagLine) num(k string) float64 {
	v, _ := strconv.ParseFloat(l.KV[k], 64)
	return v
}

const diagLookback = 30 * time.Minute

// DiagnoseLate reads the log text and says, in plain words, what slowed the
// recorder. now is this PC's clock (to tell a log that stopped); base is the
// configured interval in seconds, used when the log does not say.
func DiagnoseLate(text string, now time.Time, base int) DiagReport {
	lines := parseDiag(text)
	rep := DiagReport{Lines: len(lines)}
	if len(lines) == 0 {
		rep.Headline = "No diagnostics yet."
		rep.Findings = []DiagFinding{{Code: "no-log", Title: "There is no diagnostics log.",
			Detail: "Either this Blackbox is older than the log (update it), or it has only just started. Wait five minutes and look again."}}
		return rep
	}
	newest := lines[len(lines)-1].T
	rep.Newest = &newest
	var fs []DiagFinding
	add := func(code string, cause bool, title, detail string) {
		fs = append(fs, DiagFinding{Code: code, Cause: cause, Title: title, Detail: detail})
	}

	if last := lines[len(lines)-1]; last.Kind == "stop" && !now.IsZero() && now.UTC().Sub(newest) > 2*time.Minute {
		add("stopped", false, "The Blackbox was stopped.", "The log ends with a normal stop. It was stopped on purpose, or the service was restarted. Start it again to keep recording.")
	} else if !now.IsZero() && now.UTC().Sub(newest) > 12*time.Minute {
		add("log-stopped", true, "The Blackbox stopped writing its log.",
			fmt.Sprintf("The last line is %s old. It writes at least every 5 minutes. So the recorder is stopped or frozen: the service was stopped, the PC slept, or it hung. Restart the UnravisBlackbox service.", roundAge(now.UTC().Sub(newest))))
	}

	// Window: the last diagLookback of the log.
	from := newest.Add(-diagLookback)
	var win []diagLine
	for _, l := range lines {
		if !l.T.Before(from) {
			win = append(win, l)
		}
	}
	interval, eff := base, base
	var rounds, missed int
	var lagMax, appendMax, hkMax, totalMax, avgSum float64
	var avgN int
	freeMin := 101.0
	errs := map[string]int{}
	type gs struct {
		count int
		max   float64
		to    int
	}
	groups := map[string]*gs{}
	stalls, stallMax := 0, 0.0
	resolveSlow, resolveMax := 0, 0.0
	overReason := map[string]int{}
	widened := ""
	suppressed := 0
	for _, l := range win {
		switch l.Kind {
		case "beat":
			rounds += int(l.num("rounds"))
			missed += int(l.num("missed"))
			suppressed += int(l.num("suppressed"))
			lagMax = maxf(lagMax, l.num("lagmax"))
			appendMax = maxf(appendMax, l.num("appendmax"))
			hkMax = maxf(hkMax, l.num("hkmax"))
			totalMax = maxf(totalMax, l.num("max"))
			if n := l.num("rounds"); n > 0 {
				avgSum += l.num("avg") * n
				avgN += int(n)
			}
			if v := int(l.num("interval")); v > 0 {
				interval = v
			}
			if v := int(l.num("effective")); v > 0 {
				eff = v
			}
			if _, ok := l.KV["free"]; ok && l.KV["free"] != "-" {
				freeMin = minf(freeMin, l.num("free"))
			}
			for _, part := range strings.Split(l.KV["slow"], ",") {
				p := strings.Split(part, ":")
				if len(p) == 3 && strings.HasPrefix(p[2], "x") {
					g := groups[p[0]]
					if g == nil {
						g = &gs{}
						groups[p[0]] = g
					}
					ms, _ := strconv.ParseFloat(strings.TrimSuffix(p[1], "ms"), 64)
					n, _ := strconv.Atoi(strings.TrimPrefix(p[2], "x"))
					g.count += n
					g.max = maxf(g.max, ms)
				}
			}
		case "round":
			lagMax = maxf(lagMax, l.num("lag"))
			// A round whose collect took seconds while everything it timed
			// (target resolving, the host read, all groups) was quick: the
			// whole process was not running.
			if r := l.num("resolve"); r >= 2000 {
				resolveSlow++
				resolveMax = maxf(resolveMax, r)
			}
			if c := l.num("collect"); c >= 3000 {
				acc := l.num("host") + l.num("resolve") + l.num("groupsAll")
				if acc < c/2 {
					stalls++
					stallMax = maxf(stallMax, c)
				}
			}
			if e := l.KV["err"]; e != "" && e != "-" {
				errs[e]++
			}
			for _, part := range strings.Split(l.KV["groups"], ",") {
				p := strings.Split(part, ":")
				if len(p) == 3 && p[2] == "timeout" {
					if g := groups[p[0]]; g != nil {
						g.to++
					} else {
						groups[p[0]] = &gs{to: 1, max: 1000}
					}
				}
			}
		case "budget":
			if l.KV["window"] == "over" {
				overReason[l.KV["reason"]]++
			}
			if l.KV["action"] == "widen" {
				widened = l.KV["reason"]
			}
		}
	}

	if n := errs["host-slow"] + errs["host-busy"]; n > 0 {
		add("host-slow", true, "Reading the PC itself was too slow.",
			fmt.Sprintf("%d round(s) gave up because the basic read (CPU, memory, disk, network, process list) took longer than %d seconds, so no sample was written. Usual causes: antivirus scanning the Blackbox, a very busy PC, or thousands of processes.", n, int(hostBudget/time.Second)))
	}
	if n := errs["write-failed"] + errs["disk-full"]; n > 0 {
		add("write-failed", true, "Writing a sample to the recordings drive failed.",
			fmt.Sprintf("%d round(s). The drive may be full, read-only, or a network share that dropped.", n))
	}
	if n := errs["permission-denied"]; n > 0 {
		add("permission", true, "The Blackbox was refused access.", fmt.Sprintf("%d round(s) were refused. Check the permissions of the recordings folder.", n))
	}
	if n := errs["collect-failed"] + errs["panic"]; n > 0 {
		add("collect-failed", true, "Reading the counters failed.", fmt.Sprintf("%d round(s) failed for another reason. Windows performance counters may be damaged (run lodctr /R as administrator).", n))
	}
	var names []string
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		g := groups[n]
		if g.count >= 3 || g.to > 0 {
			add("group-slow:"+n, true, fmt.Sprintf("Counter group \"%s\" is slow.", n),
				fmt.Sprintf("Up to %.0f ms, slow %d time(s)%s. Groups run one after another, so a slow one delays the sample. After two slow rounds the Blackbox sets an optional group aside for 10 minutes.", g.max, maxi(g.count, g.to), timeoutText(g.to)))
		}
	}
	if resolveSlow > 0 {
		add("resolve-slow", true, "Finding the selected programs was too slow.",
			fmt.Sprintf("%d round(s) spent up to %.0f s listing processes to find the selected targets. A PC with very many processes, or antivirus, does this. Select fewer targets or use a longer interval.", resolveSlow, resolveMax/1000))
	}
	if stalls > 0 {
		add("stalled", true, "The whole Blackbox stopped for a while.",
			fmt.Sprintf("%d round(s) took up to %.0f s while no counter and no read was slow. The process itself was not given CPU time: the PC was overloaded (the Blackbox runs at low priority on purpose), asleep, or paused by antivirus.", stalls, stallMax/1000))
	}
	if appendMax >= 1000 {
		add("write-slow", true, "Writing to the recordings drive is slow.",
			fmt.Sprintf("One write took up to %.0f ms. The drive is busy, it is a network share, or antivirus is scanning the file.", appendMax))
	}
	if hkMax >= 1000 {
		add("cleanup-slow", true, "Cleaning old recordings is slow.",
			fmt.Sprintf("One cleanup took up to %.0f ms and holds up the sample after it. Lower the days kept or the size limit, or use a faster drive.", hkMax))
	}
	if lagMax >= 2000 {
		add("cpu-starved", true, "The PC did not give the recorder time.",
			fmt.Sprintf("A round started up to %.1f s after it was due. The PC was overloaded or asleep. The Blackbox runs at low priority on purpose, so a busy PC starves it first.", lagMax/1000))
	}
	if avgN > 0 && eff > 0 {
		avg := avgSum / float64(avgN)
		if avg >= float64(eff)*1000*0.8 || totalMax >= float64(eff)*1000 {
			add("too-much-work", true, "Each round takes about as long as the interval.",
				fmt.Sprintf("Average %.0f ms, longest %.0f ms, against an interval of %d s. Too many counters or processes for this interval. Use a longer interval or pick fewer targets.", avg, totalMax, eff))
		}
	}
	if missed > 0 {
		add("missed", true, "Rounds were skipped.", fmt.Sprintf("%d scheduled round(s) were skipped because the one before ran over.", missed))
	}
	if eff > interval || widened != "" {
		reason := widened
		if reason == "" {
			for r := range overReason {
				reason = r
			}
		}
		add("self-throttle", true, "The Blackbox slowed itself down on purpose.",
			fmt.Sprintf("It now takes a sample every %d s instead of %d s%s. It does this to stay light on a busy PC. This is by design, not a fault, and it steps back when things are calm.", eff, interval, throttleWhy(reason)))
	} else if len(overReason) > 0 {
		add("budget-over", false, "The Blackbox was over its own budget.", "It used more than its limit in some windows. If this keeps up it will widen the interval.")
	}
	if freeMin <= 10 {
		add("disk-low", true, "The recordings drive is almost full.", fmt.Sprintf("Free space went down to %.0f%%. Recording pauses when it runs out.", freeMin))
	}
	if suppressed > 0 {
		add("suppressed", false, "Many slow rounds.", fmt.Sprintf("%d more slow rounds happened than the log has room to list one by one.", suppressed))
	}

	// Causes first, then facts.
	sort.SliceStable(fs, func(i, j int) bool { return fs[i].Cause && !fs[j].Cause })
	rep.Findings = fs
	switch {
	case len(fs) > 0 && fs[0].Cause:
		rep.Headline = fs[0].Title
	default:
		avg := 0.0
		if avgN > 0 {
			avg = avgSum / float64(avgN)
		}
		rep.Headline = "The recorder looks healthy in its own log."
		rep.Findings = append(rep.Findings, DiagFinding{Code: "healthy", Title: "Nothing slow was found.",
			Detail: fmt.Sprintf("In the last %d minutes: %d round(s), average %.0f ms, interval %d s. If the page still says Running late, the delay is in reading it: a slow network share, or the clocks of the two PCs differ.", int(diagLookback.Minutes()), rounds, avg, eff)})
	}
	return rep
}

func timeoutText(n int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(", ran out of time %d time(s)", n)
}

func throttleWhy(r string) string {
	switch r {
	case "cpu":
		return " because it used too much CPU"
	case "heap", "private":
		return " because it used too much memory"
	case "slow-group":
		return " because a counter group was slow"
	}
	return ""
}

func maxf(a, b float64) float64 {
	if b > a {
		return b
	}
	return a
}
func minf(a, b float64) float64 {
	if b < a {
		return b
	}
	return a
}
func maxi(a, b int) int {
	if b > a {
		return b
	}
	return a
}

// FormatDiagText builds the text to copy: a header, the findings in words, then
// the newest log lines (at most maxLines).
func FormatDiagText(header []string, rep DiagReport, text string, maxLines int) string {
	var b strings.Builder
	b.WriteString("Unravis Blackbox diagnostics\n")
	for _, h := range header {
		b.WriteString(h + "\n")
	}
	b.WriteString("\nLikely cause: " + rep.Headline + "\n")
	for _, f := range rep.Findings {
		mark := "-"
		if f.Cause {
			mark = "*"
		}
		b.WriteString(fmt.Sprintf("%s %s %s\n", mark, f.Title, f.Detail))
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	b.WriteString("\nLog (newest last):\n")
	b.WriteString(strings.Join(lines, "\n"))
	b.WriteString("\n")
	return b.String()
}
