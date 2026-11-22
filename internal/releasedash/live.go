package releasedash

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Live is the release process that is running on this PC right now. It exists from the moment Release.ps1 takes its lock, long
// before state.json says anything (state.json is only written when a step finishes) and before a version is known.
// Only plain words are kept: no process id, lock id, nonce, requester or machine name ever reach the page.
type Live struct {
	Version string `json:"version,omitempty"` // empty while the script is still choosing (waiting for CI on master)
	Step    string `json:"step"`              // one of StageNames: the step it is working on now
	Label   string `json:"label"`             // the same, in a plain sentence
	Started string `json:"started"`
	Gone    bool   `json:"gone,omitempty"` // the process ended without saying it was done or stopped
}

// lockInfo is lock-info.json, cut down to the two fields that are safe to read. The file also holds the run's secret nonce, so it
// is decoded into this struct only and never passed on whole.
type lockInfo struct {
	Started string `json:"started"`
	PID     int    `json:"pid"`
}

const maxLiveAge = 24 * time.Hour

// processAlive says whether pid is a running process that started no later than the lock did (a reused pid started later).
// A variable so tests can stand in for it. It never opens release.lock or run.lock: those are share-none and a probe could make the
// release's own attempt fail.
var processAlive = pidAlive

// stepLabel is what each step says while it runs.
var stepLabel = map[string]string{
	"Preflight": "Preflight: checks, and waiting for CI to be green",
	"Tag":       "Tagging the release",
	"Build":     "Building: CI is building the release",
	"Sign":      "Signing the update",
	"Publish":   "Publishing to GitHub",
	"Feed":      "Updating the signed feed",
	"Verify":    "Verifying on a clean machine",
}

// nextStep is the step that follows the one an audit event finishes.
var nextStep = map[string]string{
	"preflight-ok": "Tag", "tagging": "Tag", "tagged": "Build", "built": "Sign", "signed": "Publish",
	"published": "Feed", "feed-updated": "Verify",
}

func readLive(dir string, events []Event, now time.Time) *Live {
	sp := filepath.Join(dir, "lock-info.json")
	fi, err := os.Stat(sp)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > 16<<10 {
		return nil
	}
	b, err := os.ReadFile(sp)
	if err != nil {
		return nil
	}
	var li lockInfo
	if json.Unmarshal(b, &li) != nil {
		return nil
	}
	started, err := time.Parse(time.RFC3339Nano, li.Started)
	if err != nil || started.After(now.Add(time.Minute)) || now.Sub(started) > maxLiveAge {
		return nil
	}
	lv := &Live{Step: "Preflight", Label: "Starting: waiting for CI on master to be green", Started: started.UTC().Format(time.RFC3339)}
	finished := false
	for _, e := range events {
		at, err := time.Parse(time.RFC3339Nano, e.At)
		if err != nil || at.Before(started.Add(-2*time.Second)) { // only this run's lines
			continue
		}
		switch e.Name {
		case "pinned":
			lv.Label = "Commit chosen: starting the release"
		case "run-start":
			lv.Version, lv.Step = e.Version, "Preflight"
			if e.From == "Wait" {
				lv.Step = "Build"
			} else if _, ok := stepLabel[e.From]; ok {
				lv.Step = e.From
			}
			lv.Label = stepLabel[lv.Step]
		case "preflight-ok", "tagging", "tagged", "built", "signed", "published", "feed-updated":
			lv.Step = nextStep[e.Name]
			lv.Label = stepLabel[lv.Step]
		case "verified", "done", "stopped", "withdrawn", "lock-released":
			finished = true // state.json (or the withdrawn mark) tells the rest
		}
	}
	if finished {
		return nil
	}
	if !processAlive(li.PID, started) {
		lv.Gone = true
		lv.Label = "The release process ended without finishing (" + strings.ToLower(lv.Label[:1]) + lv.Label[1:] + ")"
	}
	return lv
}
