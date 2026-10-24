package server

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/join"
	"api-audit-explorer/internal/resource/rules"
)

// maxResourceEpisodes is the most ep values one resource request may carry.
const maxResourceEpisodes = 200

// maxResourceSpan is the widest from-to range a resource request accepts, the
// same bound the series answer uses.
const maxResourceSpan = 200 * 365 * 24 * time.Hour

type resourceEpisodeDTO struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type resourceCoverageDTO struct {
	Metric   string  `json:"metric"`
	Fraction float64 `json:"fraction"`
}

// resourceProcessDTO is one ranked process. sel maps a process measure id to
// a selector GET /api/cases/{id}/series accepts.
type resourceProcessDTO struct {
	Name string            `json:"name"`
	Sel  map[string]string `json:"sel"`
}

type resourceHostDTO struct {
	Host      string                `json:"host"`
	Summary   []string              `json:"summary"`
	Coverage  []resourceCoverageDTO `json:"coverage"`
	Processes []resourceProcessDTO  `json:"processes"`
}

type resourceRuleDTO struct {
	ID         string              `json:"id"`
	Host       string              `json:"host"`
	Title      string              `json:"title"`
	Wording    string              `json:"wording"`
	Label      string              `json:"label"`
	Thresholds string              `json:"thresholds"`
	Evidence   []string            `json:"evidence"`
	Episode    *resourceEpisodeDTO `json:"episode"`
}

type resourceMarkerDTO struct {
	Kind string `json:"kind"`
	Edge string `json:"edge"`
	Host string `json:"host"`
	T    string `json:"t"`
	Text string `json:"text"`
}

type resourceDTO struct {
	From     string               `json:"from"`
	To       string               `json:"to"`
	Partial  bool                 `json:"partial"`
	Hosts    []resourceHostDTO    `json:"hosts"`
	Rules    []resourceRuleDTO    `json:"rules"`
	Gaps     []string             `json:"gaps"`
	Markers  []resourceMarkerDTO  `json:"markers"`
	Episodes []resourceEpisodeDTO `json:"episodes"`
}

func resourceStamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// parseResourceEpisodes reads the ep values, each "start/end" in RFC 3339 with
// end not before start, at most maxResourceEpisodes. Interval ids are "ep-<i>".
func parseResourceEpisodes(values []string) ([]join.Interval, bool) {
	if len(values) > maxResourceEpisodes {
		return nil, false
	}
	out := make([]join.Interval, 0, len(values))
	for i, v := range values {
		a, b, found := strings.Cut(v, "/")
		if !found {
			return nil, false
		}
		start, err1 := time.Parse(time.RFC3339, a)
		end, err2 := time.Parse(time.RFC3339, b)
		if err1 != nil || err2 != nil || end.Before(start) {
			return nil, false
		}
		out = append(out, join.Interval{ID: "ep-" + strconv.Itoa(i), Start: start.UTC(), End: end.UTC()})
	}
	return out, true
}

// caseResource answers GET /api/cases/{id}/resource: the resource rules RC1 to
// RC9 over one case, for the window from-to and the stall intervals given as
// ep values. The case is read under the read share, so an erase waits for the
// evaluation, and an evaluation that meets an erase answers 404.
func (h *handler) caseResource(w http.ResponseWriter, r *http.Request, raw string) {
	_, c, ok := h.openCase(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	query := r.URL.Query()
	var from, to time.Time
	for _, f := range []struct {
		name string
		into *time.Time
	}{{"from", &from}, {"to", &to}} {
		t, err := time.Parse(time.RFC3339, query.Get(f.name))
		if err != nil {
			writeCaseFailure(w, http.StatusBadRequest, "bad-time", "from and to must be RFC 3339 times with a zone.")
			return
		}
		*f.into = t.UTC()
	}
	if to.Before(from) {
		writeCaseFailure(w, http.StatusBadRequest, "bad-time", "Give a from and a to time, with to not before from.")
		return
	}
	if to.Sub(from) > maxResourceSpan {
		writeCaseFailure(w, http.StatusBadRequest, "bad-time", "The time range is too wide.")
		return
	}
	ivs, ok := parseResourceEpisodes(query["ep"])
	if !ok {
		writeCaseFailure(w, http.StatusBadRequest, "bad-episode",
			"Each ep must be two RFC 3339 times, start/end, with end not before start; at most 200.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	res, err := rules.Evaluate(ctx, c, importer.Window{From: from, To: to}, ivs, rules.DefaultThresholds())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resourceAnswer(res, from, to), maxResponseBytes)
}

// resourceAnswer turns a rules result into the response body. Every array is
// a list, never null.
func resourceAnswer(res rules.Result, from, to time.Time) resourceDTO {
	out := resourceDTO{From: resourceStamp(from), To: resourceStamp(to), Partial: res.Partial,
		Hosts: []resourceHostDTO{}, Rules: []resourceRuleDTO{}, Gaps: []string{},
		Markers: []resourceMarkerDTO{}, Episodes: []resourceEpisodeDTO{}}
	for _, hs := range res.Hosts {
		dto := resourceHostDTO{Host: hs.Host, Summary: append([]string{}, hs.Summary...), Coverage: []resourceCoverageDTO{},
			Processes: []resourceProcessDTO{}}
		for _, cv := range hs.Coverage {
			dto.Coverage = append(dto.Coverage, resourceCoverageDTO{Metric: cv.Metric, Fraction: cv.Fraction})
		}
		for _, p := range hs.Processes {
			sel := make(map[string]string, len(p.Sel))
			for k, v := range p.Sel {
				sel[k] = v
			}
			dto.Processes = append(dto.Processes, resourceProcessDTO{Name: p.Name, Sel: sel})
		}
		out.Hosts = append(out.Hosts, dto)
	}
	for _, row := range res.Rules {
		dto := resourceRuleDTO{ID: row.ID, Host: row.Host, Title: row.Title, Wording: row.Wording, Label: row.Label,
			Thresholds: row.Thresholds, Evidence: append([]string{}, row.Evidence...)}
		if row.Episode != nil {
			dto.Episode = &resourceEpisodeDTO{Start: resourceStamp(row.Episode.Start), End: resourceStamp(row.Episode.End)}
		}
		out.Rules = append(out.Rules, dto)
	}
	out.Gaps = append(out.Gaps, res.Gaps...)
	for _, m := range res.Markers {
		out.Markers = append(out.Markers, resourceMarkerDTO{Kind: m.Kind, Edge: m.Edge, Host: m.Host, T: resourceStamp(m.Start), Text: m.Text})
	}
	for _, ep := range res.Episodes {
		out.Episodes = append(out.Episodes, resourceEpisodeDTO{Start: resourceStamp(ep.Start), End: resourceStamp(ep.End)})
	}
	return out
}
