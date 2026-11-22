package releasedash

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"api-audit-explorer/internal/update"
)

// StepDetail is one step of a release with its times and the part of the run log that belongs to it.
type StepDetail struct {
	Step
	Start   string `json:"start,omitempty"`
	End     string `json:"end,omitempty"`
	Seconds int    `json:"seconds,omitempty"`
	Log     string `json:"log,omitempty"`  // run log file holding this step
	From    int    `json:"from,omitempty"` // first line in that log (0 based)
	To      int    `json:"to,omitempty"`   // one past the last line
}

// Detail is everything the release page shows that comes from this PC. GitHub parts are separate calls (they can be slow).
type Detail struct {
	Version  string       `json:"version"`
	Tag      string       `json:"tag"`
	Channel  string       `json:"channel"`
	Commit   string       `json:"commit,omitempty"`
	KeyID    string       `json:"keyId,omitempty"`
	Recorded bool         `json:"recorded"`
	Running  bool         `json:"running"`
	Steps    []StepDetail `json:"steps"`
	Failure  *Failure     `json:"failure,omitempty"`
	Logs     []LogRef     `json:"logs"` // run logs of this release (newest first), then its CI logs
	Note     string       `json:"note,omitempty"`
}

var errBadVersion = errors.New("not a release version")

func ts(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}

func fmtT(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// Release builds the step table for one version from the audit log and the saved run logs.
func (d *Dash) Release(v string, now time.Time) (Detail, error) {
	ch, ok := chanOf(v)
	if !ok {
		return Detail{}, errBadVersion
	}
	loc := ReadLocal(d.DataDir)
	det := Detail{Version: v, Tag: "v" + v, Channel: ch, Steps: []StepDetail{}, Logs: []LogRef{}}
	var ev []Event
	for _, e := range loc.Events {
		if e.Version == v {
			ev = append(ev, e)
		}
	}
	r := &Release{Version: v}
	fillSteps(r, ev, loc.Run, loc.Live)
	det.Recorded = r.Recorded
	det.Running = loc.Live != nil && !loc.Live.Gone && loc.Live.Version == v
	if !loc.Found {
		det.Note = "No release log on this PC. Step times and logs only show on the PC that makes releases."
	}
	for _, e := range ev {
		if e.Commit != "" && (e.Name == "tagged" || det.Commit == "") {
			det.Commit = e.Commit
		}
		if e.Name == "preflight-ok" && len(e.KeyPub) == 64 {
			if b, err := hex.DecodeString(e.KeyPub); err == nil {
				det.KeyID = update.KeyID(ed25519.PublicKey(b))
			}
		}
	}
	det.Logs = d.logsFor(det.Tag, ev, det.Running, now)
	var runLogs []LogRef
	for _, l := range det.Logs {
		if l.Kind == "run" {
			runLogs = append(runLogs, l)
		}
	}
	// Step times: a step ends when its event was written and starts when the step before it ended (or when the run began, for the
	// first step after a resume).
	var starts []time.Time
	for _, e := range ev {
		if e.Name == "run-start" {
			if t, ok := ts(e.At); ok {
				starts = append(starts, t)
			}
		}
	}
	var prevEnd time.Time
	for _, s := range r.Steps {
		sd := StepDetail{Step: s}
		end, hasEnd := ts(s.At)
		if s.State == "done" || s.State == "failed" {
			if hasEnd {
				sd.End = fmtT(end)
			}
		}
		if s.State == "done" || s.State == "failed" || s.State == "running" {
			start := prevEnd
			ref := end
			if !hasEnd || s.State == "running" {
				ref = now
			}
			for _, rs := range starts { // a resumed run begins at its own start, not at the end of the earlier attempt
				if !rs.After(ref) && rs.After(start) {
					start = rs
				}
			}
			if !start.IsZero() {
				sd.Start = fmtT(start)
				if !ref.Before(start) && (s.State != "running" || det.Running) {
					sd.Seconds = int(ref.Sub(start).Seconds() + 0.5)
				}
			}
		}
		if hasEnd && (s.State == "done" || s.State == "failed") {
			prevEnd = end
		}
		det.Steps = append(det.Steps, sd)
	}
	// The newest run log that holds a step gives its line range, and the exact time the script started it. Each log is read and cut
	// into steps once for the whole page, not once for every step.
	sliced := map[string][]StepLog{}
	slicesOf := func(name string) []StepLog {
		sl, ok := sliced[name]
		if !ok {
			if lines, _, err := ReadLog(d.DataDir, name, loc.Names); err == nil {
				sl = SliceSteps(lines)
			}
			sliced[name] = sl
		}
		return sl
	}
	for i := range det.Steps {
		for _, l := range runLogs {
			for _, sl := range slicesOf(l.Name) {
				if sl.Stage != det.Steps[i].Name {
					continue
				}
				det.Steps[i].Log, det.Steps[i].From, det.Steps[i].To = l.Name, sl.From, sl.To
				if sl.Start != "" && det.Steps[i].State != "pending" {
					if t, ok := ts(sl.Start); ok {
						det.Steps[i].Start = fmtT(t)
						if e, ok := ts(det.Steps[i].End); ok && !e.Before(t) {
							det.Steps[i].Seconds = int(e.Sub(t).Seconds() + 0.5)
						}
					}
				}
				break
			}
			if det.Steps[i].Log != "" {
				break
			}
		}
	}
	det.Failure = d.failure(det.Steps, ev, runLogs, loc.Names)
	return det, nil
}

// logsFor picks the saved logs that belong to a release: its CI logs by tag, and the run logs written while the audit log shows
// this release being worked on.
func (d *Dash) logsFor(tag string, ev []Event, live bool, now time.Time) []LogRef {
	all := ListLogs(d.DataDir)
	var first, last time.Time
	for _, e := range ev {
		if t, ok := ts(e.At); ok {
			if first.IsZero() || t.Before(first) {
				first = t
			}
			if t.After(last) {
				last = t
			}
		}
	}
	if live {
		last = now
	}
	runs, cis := []LogRef{}, []LogRef{} // never nil: the page reads the JSON as a list
	for _, l := range all {
		switch {
		case l.Kind == "run" && !first.IsZero():
			s, ok1 := ts(l.Start)
			e, ok2 := ts(l.End)
			// The script writes its log lines before the first audit line for the version (preflight) and after the last one.
			if ok1 && ok2 && !s.After(last.Add(5*time.Second)) && !e.Add(5*time.Second).Before(first.Add(-90*time.Second)) {
				runs = append(runs, l)
			}
		case l.Kind != "run" && l.Tag == tag:
			cis = append(cis, l)
		}
	}
	sort.SliceStable(cis, func(i, j int) bool { return cis[i].Kind < cis[j].Kind })
	return append(runs, cis...)
}

// failure finds the stopped step: the "STOPPED at" line in the newest run log that has one, else the reason the audit log kept.
func (d *Dash) failure(steps []StepDetail, ev []Event, runLogs []LogRef, names []string) *Failure {
	var failed *StepDetail
	for i := range steps {
		if steps[i].State == "failed" {
			failed = &steps[i]
			break
		}
	}
	if failed == nil {
		return nil
	}
	for _, l := range runLogs {
		lines, _, err := ReadLog(d.DataDir, l.Name, names)
		if err != nil {
			continue
		}
		if f := FindFailure(lines); f != nil && f.Stage == failed.Name {
			f.Log = l.Name
			return f
		}
	}
	msg := failed.Reason
	if msg == "" {
		msg = "The step did not finish."
	}
	return &Failure{Stage: failed.Name, Message: msg}
}

// TestRun is the test result read from one saved CI run log.
type TestRun struct {
	Kind    string      `json:"kind"` // ci, release or verify
	Run     string      `json:"run"`
	URL     string      `json:"url"`
	Results TestResults `json:"results"`
}

// TestReport is every saved run for one release.
type TestReport struct {
	Runs []TestRun `json:"runs"`
	Note string    `json:"note,omitempty"`
}

// Tests reads the tests that ran for a release from the CI logs saved at release time.
func (d *Dash) Tests(v string) (TestReport, error) {
	if _, ok := chanOf(v); !ok {
		return TestReport{}, errBadVersion
	}
	loc := ReadLocal(d.DataDir)
	rep := TestReport{Runs: []TestRun{}}
	for _, l := range ListLogs(d.DataDir) {
		if l.Kind == "run" || l.Tag != "v"+v {
			continue
		}
		res, ok := parsedTests(d.DataDir, l, loc.Names)
		if !ok {
			continue
		}
		rep.Runs = append(rep.Runs, TestRun{Kind: l.Kind, Run: l.Run, URL: RepoURL + "actions/runs/" + l.Run, Results: res})
	}
	sort.SliceStable(rep.Runs, func(i, j int) bool { return rep.Runs[i].Kind < rep.Runs[j].Kind })
	if len(rep.Runs) == 0 {
		rep.Note = "No test results were saved for this release. The release manager saves the CI test output when it finishes, so releases made with an older version of it have none. The CI checks tab still shows what GitHub ran."
	}
	return rep, nil
}

// ArtifactInfo is what was published for a release.
type ArtifactInfo struct {
	Assets      []Asset `json:"assets"`
	KeyID       string  `json:"keyId,omitempty"`
	ManifestSHA string  `json:"manifestSha,omitempty"`
	Note        string  `json:"note,omitempty"`
}

// Artifacts lists the files on the GitHub release with the checksums GitHub recorded, the signing key id and the manifest hash.
func (d *Dash) Artifacts(ctx context.Context, v string) (ArtifactInfo, error) {
	if _, ok := chanOf(v); !ok {
		return ArtifactInfo{}, errBadVersion
	}
	info := ArtifactInfo{Assets: []Asset{}}
	rels, _, _ := d.GH.Get(ctx, false)
	for _, r := range rels {
		if r.Tag == "v"+v {
			info.Assets = append(info.Assets, r.Assets...)
		}
	}
	if len(info.Assets) == 0 {
		info.Note = "This release has no files on GitHub (yet)."
	}
	loc := ReadLocal(d.DataDir)
	for _, e := range loc.Events {
		if e.Version != v {
			continue
		}
		if e.Name == "preflight-ok" && len(e.KeyPub) == 64 {
			if b, err := hex.DecodeString(e.KeyPub); err == nil {
				info.KeyID = update.KeyID(ed25519.PublicKey(b))
			}
		}
		if e.Name == "feed-updated" && e.Manifest != "" {
			info.ManifestSHA = e.Manifest
		}
	}
	return info, nil
}

// Checks reads the CI check runs for the release commit.
func (d *Dash) Checks(ctx context.Context, v string) ([]Check, bool, error) {
	if _, ok := chanOf(v); !ok {
		return nil, false, errBadVersion
	}
	return d.GH.Checks(ctx, "v"+v, ttlFor(d, ctx, v))
}

// Changes compares the release with the one before it in the same channel.
func (d *Dash) Changes(ctx context.Context, v string) (Changes, bool, error) {
	if _, ok := chanOf(v); !ok {
		return Changes{}, false, errBadVersion
	}
	rels, _, _ := d.GH.Get(ctx, false)
	prev := previousTag(rels, v)
	return d.GH.Changes(ctx, prev, "v"+v, ttlFor(d, ctx, v))
}

// ttlFor: a release that is on GitHub does not change, so its answers are kept a day; one still being made is asked again soon.
func ttlFor(d *Dash, ctx context.Context, v string) time.Duration {
	rels, _, _ := d.GH.Get(ctx, false)
	for _, r := range rels {
		if r.Tag == "v"+v && !r.Draft {
			if strings.Contains(r.Published, "T") {
				return finishedTTL
			}
		}
	}
	return liveTTL
}

// parsedTests is ParseTests over one saved CI log, kept while the file is unchanged: the page asks again every few seconds, and a big
// CI log takes seconds to unpack and parse.
func parsedTests(dir string, l LogRef, names []string) (TestResults, bool) {
	path := filepath.Join(dir, logsDir, l.Name)
	fi, err := os.Stat(path)
	if err != nil {
		return TestResults{}, false
	}
	key := logKey{path: path, names: strings.Join(names, "\x00"), size: fi.Size(), mod: fi.ModTime().UnixNano()}
	res, err := testCache.get(key, func(TestResults) int { return 1 }, func() (TestResults, error) {
		rc, err := openCI(dir, l.Name)
		if err != nil {
			return TestResults{}, err
		}
		defer func() { _ = rc.Close() }()
		return ParseTests(rc, names), nil
	})
	return res, err == nil
}

var testCache = memo[TestResults]{maxItems: 12, maxWeight: 12}
