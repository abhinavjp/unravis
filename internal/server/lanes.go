package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/investigate"
)

// The lane routes read one database for one incident (SQL Server or MongoDB), keep the answer in the case as a health run whose report carries the
// lane result, and feed it to the investigation. Unravis only reads: the grant scripts and the profiler guide are text for the owner to run.

const (
	laneSQLLabel   = "SQL Server"
	laneMongoLabel = "MongoDB"
	laneNoDatabase = "Pick the audit database to read."
)

type laneWindows struct {
	Incident  investigationWindow `json:"incident"`
	Reference investigationWindow `json:"reference"`
}

type laneSQLBody struct {
	sqlRequest
	laneWindows
}

type laneMongoBody struct {
	audit.ConnectionInput
	Database   string `json:"database"`
	Collection string `json:"collection"`
	laneWindows
}

func laneWindow(w investigationWindow) dbhealth.Window {
	return dbhealth.Window{Start: w.Start.UTC(), End: w.End.UTC()}
}

// sentence turns a validation error into a sentence for the person.
func sentence(err error) string {
	s := strings.TrimSpace(err.Error())
	if s == "" {
		return "Check the windows."
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	s = string(r)
	if !strings.HasSuffix(s, ".") {
		s += "."
	}
	return s
}

// healthStoreOf finds the health store behind a case, through the shared-case wrapper.
func healthStoreOf(c casestore.Case) (casestore.HealthStore, bool) {
	if sc, ok := c.(*sharedCase); ok {
		c = sc.Case
	}
	hs, ok := c.(casestore.HealthStore)
	return hs, ok
}

// laneRun is a stored health run that carries a lane result.
type laneRun struct {
	Run    casestore.HealthRun
	Report dbhealth.Report
}

func (l laneRun) source() string {
	return l.Run.Kind + "\x00" + l.Report.Label + "\x00" + l.Report.Database
}

func laneRunsOf(runs []casestore.HealthRun) []laneRun {
	var out []laneRun
	for _, r := range runs {
		if r.Kind != "sql" && r.Kind != "mongo" {
			continue
		}
		var rep dbhealth.Report
		if json.Unmarshal(r.Report, &rep) != nil || rep.Lane == nil {
			continue
		}
		out = append(out, laneRun{Run: r, Report: rep})
	}
	return out
}

// overlapsHeldDay reports a day already held for the same source whose incident window overlaps the new one without being the same window. The
// same window again is a re-check and is allowed; two different days that share minutes would be counted twice, so the second is refused.
func overlapsHeldDay(held []laneRun, kind, label, database string, incident dbhealth.Window) bool {
	probe := laneRun{Run: casestore.HealthRun{Kind: kind}, Report: dbhealth.Report{Label: label, Database: database}}
	for _, h := range held {
		if h.source() != probe.source() {
			continue
		}
		if h.Report.Lane.Incident.Overlaps(incident) && !h.Report.Lane.Incident.Same(incident) {
			return true
		}
	}
	return false
}

const laneOverlapText = "You already added a day for this database that overlaps these times. Use the same start and end to check that day again, or pick times that do not overlap. Nothing was read."

// laneKey names a lane day by its source and window. The case keeps one run per key and holds lane days apart from the plain health runs, so
// reading the same day again replaces it and a run of the health checks can never push a day out.
func laneKey(kind string, report dbhealth.Report) string {
	in := report.Lane.Incident
	return kind + "|" + report.Label + "|" + report.Database + "|" + in.Start.UTC().Format(time.RFC3339) + "|" + in.End.UTC().Format(time.RFC3339)
}

// laneSave keeps a finished lane run in the case and answers like a health run does.
func laneSave(w http.ResponseWriter, r *http.Request, hs casestore.HealthStore, kind string, report dbhealth.Report) {
	enc, err := json.Marshal(report)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "The result could not be prepared.")
		return
	}
	sctx, scancel := context.WithTimeout(r.Context(), requestTimeout)
	defer scancel()
	saved, err := hs.AddHealthRun(sctx, casestore.HealthRun{Kind: kind, Label: report.Label, Status: string(report.Overall), Report: enc, Key: laneKey(kind, report)})
	out := struct {
		Saved bool         `json:"saved"`
		State string       `json:"state"`
		Run   healthRunDTO `json:"run"`
		Error string       `json:"saveError,omitempty"`
	}{State: report.Lane.State()}
	if err != nil {
		out.Run = healthRunDTO{At: report.FinishedAt, Kind: kind, Label: report.Label, Status: string(report.Overall), Report: enc}
		out.Error = "The result could not be saved in the case."
		if errors.Is(err, casestore.ErrCaseClosed) {
			out.Error = "The case is closed, so the result was not saved."
		}
	} else {
		out.Saved, out.Run = true, healthDTO(saved)
	}
	writeJSON(w, http.StatusOK, out, maxResponseBytes)
}

// laneHeld reads the days already held, newest first.
func laneHeld(ctx context.Context, hs casestore.HealthStore) ([]laneRun, error) {
	runs, err := hs.HealthRuns(ctx)
	if err != nil {
		return nil, err
	}
	return laneRunsOf(runs), nil
}

func (h *handler) caseLaneSQL(w http.ResponseWriter, r *http.Request, raw string) {
	if h.demo {
		writeError(w, http.StatusServiceUnavailable, healthDemoText)
		return
	}
	var body laneSQLBody
	if !decodeJSON(w, r, &body) {
		return
	}
	req := dbhealth.LaneRequest{Incident: laneWindow(body.Incident), Reference: laneWindow(body.Reference)}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, sentence(err))
		return
	}
	c, hs, ok := h.healthCaseOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	if !h.tests.allow() {
		writeError(w, http.StatusTooManyRequests, "Too many checks in the last minute. Wait a minute, then run again.")
		return
	}
	target, label, ok := h.resolveSQL(w, body.sqlRequest)
	if !ok {
		return
	}
	if _, valid := dbhealth.QuoteIdent(target.Database); !valid {
		writeError(w, http.StatusBadRequest, laneNoDatabase)
		return
	}
	if strings.TrimSpace(label) == "" {
		label = laneSQLLabel
	}
	req.Database, req.Login = target.Database, target.Username
	ctx, cancel := context.WithTimeout(r.Context(), healthRunTimeout)
	defer cancel()
	held, err := laneHeld(ctx, hs)
	if err != nil {
		writeTagError(w, err, "")
		return
	}
	if overlapsHeldDay(held, "sql", label, req.Database, req.Incident) {
		writeError(w, http.StatusConflict, laneOverlapText)
		return
	}
	if !healthBusy.TryLock() {
		writeError(w, http.StatusConflict, healthBusyText)
		return
	}
	defer healthBusy.Unlock()
	q, err := openSQLFn(ctx, target)
	if err != nil {
		writeConnectFailure(w, err)
		return
	}
	defer q.Close()
	report := dbhealth.RunSQLLane(ctx, q, label, req, time.Now)
	laneSave(w, r, hs, "sql", report)
}

func (h *handler) caseLaneMongo(w http.ResponseWriter, r *http.Request, raw string) {
	if h.demo {
		writeError(w, http.StatusServiceUnavailable, healthDemoText)
		return
	}
	var body laneMongoBody
	if !decodeJSON(w, r, &body) {
		return
	}
	req := dbhealth.LaneRequest{Incident: laneWindow(body.Incident), Reference: laneWindow(body.Reference), Database: body.Database, Collection: body.Collection}
	if err := req.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, sentence(err))
		return
	}
	if !okMongoName(body.Database, 64, false) || (body.Collection != "" && !okMongoName(body.Collection, 120, true)) {
		writeError(w, http.StatusBadRequest, "Pick the MongoDB database to read.")
		return
	}
	c, hs, ok := h.healthCaseOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	if !h.tests.allow() {
		writeError(w, http.StatusTooManyRequests, "Too many checks in the last minute. Wait a minute, then run again.")
		return
	}
	input, ok := h.resolveConnection(w, body.ConnectionInput)
	if !ok {
		return
	}
	opener, ok := h.repo.(mongoCommandOpener)
	if !ok {
		writeError(w, http.StatusBadRequest, "MongoDB checks are unavailable for this repository.")
		return
	}
	req.Login = input.Username
	ctx, cancel := context.WithTimeout(r.Context(), healthRunTimeout)
	defer cancel()
	held, err := laneHeld(ctx, hs)
	if err != nil {
		writeTagError(w, err, "")
		return
	}
	if overlapsHeldDay(held, "mongo", laneMongoLabel, req.Database, req.Incident) {
		writeError(w, http.StatusConflict, laneOverlapText)
		return
	}
	if !healthBusy.TryLock() {
		writeError(w, http.StatusConflict, healthBusyText)
		return
	}
	defer healthBusy.Unlock()
	run, closeFn, err := opener.OpenCommandClient(ctx, input)
	if err != nil {
		if writeConnectionError(w, err) {
			return
		}
		writeError(w, http.StatusBadRequest, "Could not connect to MongoDB. Check the connection details and permissions.")
		return
	}
	defer closeFn()
	report := dbhealth.RunMongoLane(ctx, mongoAdapter(run), laneMongoLabel, req, time.Now)
	laneSave(w, r, hs, "mongo", report)
}

type laneDayDTO struct {
	ID        int64                `json:"id"`
	At        time.Time            `json:"at"`
	Kind      string               `json:"kind"`
	Label     string               `json:"label"`
	Database  string               `json:"database"`
	State     string               `json:"state"`
	Incident  dbhealth.Window      `json:"incident"`
	Reference dbhealth.Window      `json:"reference"`
	Lane      *dbhealth.LaneResult `json:"lane"`
}

// caseLaneList returns the days held in the case, newest first. A day read again with the same windows replaces the older read in this list.
func (h *handler) caseLaneList(w http.ResponseWriter, r *http.Request, raw string) {
	c, hs, ok := h.healthCaseOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	held, err := laneHeld(ctx, hs)
	if err != nil {
		writeTagError(w, err, "")
		return
	}
	out := []laneDayDTO{}
	for _, l := range freshestLaneRuns(held) {
		out = append(out, laneDayDTO{ID: l.Run.ID, At: l.Run.At, Kind: l.Run.Kind, Label: l.Report.Label, Database: l.Report.Database,
			State: l.Report.Lane.State(), Incident: l.Report.Lane.Incident, Reference: l.Report.Lane.Reference, Lane: l.Report.Lane})
	}
	writeJSON(w, http.StatusOK, struct {
		Days []laneDayDTO `json:"days"`
	}{out}, maxResponseBytes)
}

// freshestLaneRuns keeps the newest run for each source and window (held is newest first).
func freshestLaneRuns(held []laneRun) []laneRun {
	var out []laneRun
	for _, l := range held {
		dup := false
		for _, k := range out {
			if k.source() == l.source() && k.Report.Lane.Incident.Same(l.Report.Lane.Incident) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, l)
		}
	}
	return out
}

// applyLaneRuns feeds the case's SQL and Mongo days into the engine. Only days whose incident window overlaps the investigated incident count. A
// yes from any day wins; a no stands only when no day said yes. Nothing is added when the case holds no such day, so an audit-only run is unchanged.
func applyLaneRuns(ctx context.Context, c casestore.Case, in *investigate.Input, read *investigationRead) {
	hs, ok := healthStoreOf(c)
	if !ok {
		return
	}
	held, err := laneHeld(ctx, hs)
	if err != nil {
		if read != nil {
			read.Warnings = append(read.Warnings, "The SQL and MongoDB days held in this case could not be read, so they were not used.")
		}
		return
	}
	incident := dbhealth.Window{Start: in.Incident.Start, End: in.Incident.End}
	seenSource := map[string]bool{}
	for _, l := range freshestLaneRuns(held) {
		lane := l.Report.Lane
		if !lane.Incident.Overlaps(incident) {
			continue
		}
		for id, v := range lane.Answers {
			switch v {
			case dbhealth.AnswerYes:
				ensureLanes(in)[id] = investigate.Yes
			case dbhealth.AnswerNo:
				if in.Lanes[id] != investigate.Yes {
					ensureLanes(in)[id] = investigate.No
				}
			}
		}
		if lane.Onset != nil {
			if in.LaneOnsets == nil {
				in.LaneOnsets = map[string]time.Time{}
			}
			t := lane.Onset.UTC()
			if prev, has := in.LaneOnsets[l.Run.Kind]; !has || t.Before(prev) {
				in.LaneOnsets[l.Run.Kind] = t
			}
		}
		for _, s := range lane.Sources {
			if !seenSource[s] {
				seenSource[s] = true
				in.LaneSources = append(in.LaneSources, s)
			}
		}
	}
}

func ensureLanes(in *investigate.Input) map[string]investigate.Tri {
	if in.Lanes == nil {
		in.Lanes = map[string]investigate.Tri{}
	}
	return in.Lanes
}

// grantScripts returns text for the owner to run. It never runs anything and never reads a connection: the login and database names only fill
// the placeholders.
func (h *handler) grantScripts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	login, database := q.Get("login"), q.Get("database")
	if len(login) > 256 || len(database) > 256 {
		writeError(w, http.StatusBadRequest, "That name is too long.")
		return
	}
	switch q.Get("kind") {
	case "sql":
		era, ok := dbhealth.ParseEra(q.Get("version"))
		if !ok {
			writeError(w, http.StatusBadRequest, "Version must be 2019 or 2022.")
			return
		}
		fixes := map[string]dbhealth.GrantFix{}
		for _, id := range []string{dbhealth.CheckServer, dbhealth.CheckErrorLog, dbhealth.CheckQueryStore, dbhealth.CheckAgentJobs} {
			fixes[id] = dbhealth.SQLCheckGrant(id, era, login, database)
		}
		writeJSON(w, http.StatusOK, struct {
			Kind    string                       `json:"kind"`
			Version string                       `json:"version"`
			Script  string                       `json:"script"`
			Checks  map[string]dbhealth.GrantFix `json:"checks"`
		}{"sql", string(era), dbhealth.SQLGrantScript(era, login, database), fixes}, 64<<10)
	case "mongo":
		writeJSON(w, http.StatusOK, struct {
			Kind   string `json:"kind"`
			Script string `json:"script"`
		}{"mongo", dbhealth.MongoGrantScript(login, database)}, 64<<10)
	case "mongo-profiler":
		writeJSON(w, http.StatusOK, struct {
			Kind  string                 `json:"kind"`
			Guide dbhealth.ProfilerGuide `json:"guide"`
		}{"mongo-profiler", dbhealth.MongoProfilerGuide(database)}, 64<<10)
	default:
		writeError(w, http.StatusBadRequest, "Kind must be sql, mongo or mongo-profiler.")
	}
}
