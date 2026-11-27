package releasedash

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"
)

var ghRunIDRE = regexp.MustCompile(`^[0-9]{1,20}$`)

type ghRun struct {
	Name       string `json:"name"`
	Title      string `json:"display_title"`
	Event      string `json:"event"`
	Branch     string `json:"head_branch"`
	SHA        string `json:"head_sha"`
	Number     int    `json:"run_number"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Created    string `json:"created_at"`
	Started    string `json:"run_started_at"`
	Updated    string `json:"updated_at"`
	URL        string `json:"html_url"`
	Actor      struct {
		Login string `json:"login"`
	} `json:"actor"`
	Path string `json:"path"`
}
type ghStep struct {
	Number     int    `json:"number"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Start      string `json:"started_at"`
	End        string `json:"completed_at"`
}
type ghJob struct {
	ID         json.Number `json:"id"`
	Name       string      `json:"name"`
	Status     string      `json:"status"`
	Conclusion string      `json:"conclusion"`
	Start      string      `json:"started_at"`
	End        string      `json:"completed_at"`
	URL        string      `json:"html_url"`
	Steps      []ghStep    `json:"steps"`
}

func ghStatus(status, conclusion string) string {
	if status != "completed" {
		if status == "in_progress" {
			return stRunning
		}
		return stWaiting
	}
	switch conclusion {
	case "success":
		return stPassed
	case "failure", "timed_out", "action_required", "startup_failure":
		return stFailed
	case "cancelled":
		return stStopped
	case "skipped":
		return stSkipped
	default:
		return stFinished
	}
}
func ghDuration(start, end string, live bool) int64 {
	a, err := time.Parse(time.RFC3339Nano, start)
	if err != nil {
		return 0
	}
	var b time.Time
	if end == "" && live {
		b = time.Now()
	} else {
		b, err = time.Parse(time.RFC3339Nano, end)
		if err != nil {
			return 0
		}
	}
	if b.Before(a) {
		return 0
	}
	return b.Sub(a).Milliseconds()
}
func ghURL(s string) string {
	if strings.HasPrefix(s, RepoURL) {
		return s
	}
	return ""
}
func ghText(s string) string { return clip(NewRedactor().Line(s)) }

// GHRun reads the run and its jobs without changing either GitHub or the local store.
func (d *Dash) GHRun(ctx context.Context, id string) (*CIDetail, bool, error) {
	p := "/actions/runs/" + id
	// A cached completed run can use the longer TTL; unknown and live runs refresh frequently.
	ttl := liveTTL
	d.GH.jmu.Lock()
	if e := d.GH.jc[p]; e != nil {
		var cached ghRun
		if json.Unmarshal(e.body, &cached) == nil && cached.Status == "completed" {
			ttl = finishedTTL
		}
	}
	d.GH.jmu.Unlock()
	var raw ghRun
	stale, err := d.GH.GetJSON(ctx, p, ttl, &raw)
	if err != nil {
		return nil, false, err
	}
	live := raw.Status != "completed"
	if live {
		ttl = liveTTL
	} else {
		ttl = finishedTTL
		// A jobs response cached while the run was live must not be treated as
		// final for a whole day after the run completes.
		d.GH.jmu.Lock()
		if e := d.GH.jc[p+"/jobs?per_page=100"]; e != nil {
			var cached struct {
				Jobs []ghJob `json:"jobs"`
			}
			if json.Unmarshal(e.body, &cached) == nil {
				for _, job := range cached.Jobs {
					if job.Status != "completed" {
						ttl = liveTTL
						break
					}
				}
			}
		}
		d.GH.jmu.Unlock()
	}
	var jobs struct {
		Jobs []ghJob `json:"jobs"`
	}
	jobsStale, err := d.GH.GetJSON(ctx, p+"/jobs?per_page=100", ttl, &jobs)
	if err != nil {
		return nil, false, err
	}
	start := raw.Started
	if start == "" {
		start = raw.Created
	}
	end := ""
	if !live {
		end = raw.Updated
	}
	status := ghStatus(raw.Status, raw.Conclusion)
	run := CIRun{ID: id, Kind: "run", Source: "github", URL: ghURL(raw.URL), Status: status, Word: statusWords[status], Workflow: ghText(path.Base(strings.SplitN(raw.Path, "@", 2)[0])), Event: ghText(raw.Event), Branch: ghText(raw.Branch), SHA: ghText(raw.SHA), HeadSHA: ghText(raw.SHA), Author: ghText(raw.Actor.Login), Repo: "abhinavjp/unravis", Start: ghText(start), End: ghText(end), Ms: ghDuration(start, end, live)}
	if raw.Path == "" {
		run.Workflow = ""
	}
	if raw.Event == "push" && strings.HasPrefix(raw.Branch, "v") && len(raw.Branch) > 1 && raw.Branch[1] >= '0' && raw.Branch[1] <= '9' {
		run.Release = ghText(raw.Branch)
	}
	out := &CIDetail{Run: run, Live: live, Jobs: make([]CIJob, 0, len(jobs.Jobs)), Posted: []CIPosted{}}
	for _, j := range jobs.Jobs {
		st := ghStatus(j.Status, j.Conclusion)
		name := ghText(j.Name)
		job := CIJob{Key: "gh-" + j.ID.String(), ID: name, Label: name, URL: ghURL(j.URL), Status: st, Word: statusWords[st], Start: ghText(j.Start), End: ghText(j.End), Ms: ghDuration(j.Start, j.End, j.Status != "completed"), Steps: make([]CIStep, 0, len(j.Steps))}
		for _, s := range j.Steps {
			st := ghStatus(s.Status, s.Conclusion)
			job.Steps = append(job.Steps, CIStep{Index: s.Number, Name: ghText(s.Name), Status: st, Word: statusWords[st], Start: ghText(s.Start), End: ghText(s.End), Ms: ghDuration(s.Start, s.End, s.Status != "completed")})
		}
		out.Jobs = append(out.Jobs, job)
	}
	return out, stale || jobsStale, nil
}
func (d *Dash) registerGHRun(mux *http.ServeMux) {
	mux.HandleFunc("/api/ghrun", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if !ghRunIDRE.MatchString(id) {
			fail(w, http.StatusBadRequest, errBadID.Error())
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		out, stale, err := d.GHRun(ctx, id)
		if err != nil {
			code := http.StatusBadGateway
			if err.Error() == "GitHub does not have this" {
				code = http.StatusNotFound
			}
			fail(w, code, err.Error())
			return
		}
		if stale {
			w.Header().Set("X-Stale", "1")
		}
		writeJSON(w, out)
	})
}
