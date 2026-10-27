package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/sqlconn"
	"api-audit-explorer/internal/store"
)

// healthRunTimeout bounds one whole run (each check also has its own 20 s limit inside dbhealth).
const healthRunTimeout = 150 * time.Second

// The drivers are called through these so a test can stand in for a real server.
var (
	testSQLFn = dbhealth.TestSQL
	openSQLFn = func(ctx context.Context, t dbhealth.SQLTarget) (healthQueryer, error) { return dbhealth.OpenSQL(ctx, t) }
)

type healthQueryer interface {
	dbhealth.Queryer
	Close()
}

type mongoCommandOpener interface {
	OpenCommandClient(ctx context.Context, input audit.ConnectionInput) (store.MongoCommandFunc, func(), error)
}

type mongoAdapter store.MongoCommandFunc

func (f mongoAdapter) Command(ctx context.Context, database, commandJSON string) (map[string]any, error) {
	return f(ctx, database, commandJSON)
}

// healthBusy lets one run at a time touch a customer's server: a second click answers 409 instead of piling queries on it.
var healthBusy sync.Mutex

// sqlRequest is a SQL login typed in the form, or a saved one (savedId) with an optional password and database to use instead.
type sqlRequest struct {
	SavedID string `json:"savedId"`
	sqlconn.Input
}

const (
	healthDemoText    = "Health checks need a real SQL Server or MongoDB, so they are off in the demo."
	healthBusyText    = "A health check is already running. Wait for it to finish."
	sqlNeedServerText = "Enter the server name."
)

// resolveSQL turns the request into a login. A saved login supplies everything the browser never holds (the stored password).
func (h *handler) resolveSQL(w http.ResponseWriter, req sqlRequest) (dbhealth.SQLTarget, string, bool) {
	in := req.Input
	if req.SavedID != "" {
		if h.sqlConns == nil {
			writeError(w, http.StatusServiceUnavailable, sqlUnavailable)
			return dbhealth.SQLTarget{}, "", false
		}
		saved, err := h.sqlConns.Resolve(req.SavedID)
		if err != nil {
			if errors.Is(err, sqlconn.ErrNotFound) {
				writeError(w, http.StatusNotFound, "Saved connection was not found.")
				return dbhealth.SQLTarget{}, "", false
			}
			writeError(w, http.StatusServiceUnavailable, "Saved SQL Server connections could not be read.")
			return dbhealth.SQLTarget{}, "", false
		}
		if req.Password != "" {
			saved.Password = req.Password
		}
		if req.Database != "" {
			saved.Database = req.Database
		}
		in = saved
	}
	if strings.TrimSpace(in.Server) == "" {
		writeError(w, http.StatusBadRequest, sqlNeedServerText)
		return dbhealth.SQLTarget{}, "", false
	}
	if in.Auth != "sql" {
		in.Auth, in.Password, in.Username = "windows", "", ""
	}
	return dbhealth.SQLTarget{
		Server: in.Server, Auth: in.Auth, Username: in.Username, Password: in.Password,
		Database: in.Database, Encrypt: in.Encrypt, TrustServerCertificate: in.TrustServerCertificate,
	}, in.Label, true
}

func writeConnectFailure(w http.ResponseWriter, err error) {
	ce := dbhealth.ClassifyConnect(err)
	writeJSON(w, http.StatusBadRequest, struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}{Error: ce.Message, Code: ce.Code}, 4096)
}

// testSQLConnection connects, reads the version and lists the databases the login can open. A login that connects but cannot list the databases
// is still a success (listError says why), so the person can type the name.
func (h *handler) testSQLConnection(w http.ResponseWriter, r *http.Request) {
	if h.demo {
		writeError(w, http.StatusServiceUnavailable, healthDemoText)
		return
	}
	if !h.tests.allow() {
		writeError(w, http.StatusTooManyRequests, "Too many connection tests in the last minute. Wait a minute, then test again.")
		return
	}
	var req sqlRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	target, _, ok := h.resolveSQL(w, req)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	res, err := testSQLFn(ctx, target)
	out := struct {
		OK        bool     `json:"ok"`
		Version   string   `json:"version"`
		Edition   string   `json:"edition"`
		Databases []string `json:"databases"`
		ListError string   `json:"listError,omitempty"`
	}{OK: true, Version: res.Version, Edition: res.Edition, Databases: res.Databases}
	if out.Databases == nil {
		out.Databases = []string{}
	}
	if err != nil {
		ce := dbhealth.ClassifyConnect(err)
		if ce.Code != "list" {
			writeConnectFailure(w, err)
			return
		}
		out.ListError = ce.Message
	}
	writeJSON(w, http.StatusOK, out, maxResponseBytes)
}

/* ---------------------------------------------------------------- case runs */

type healthRunBody struct {
	Kind  string      `json:"kind"`
	SQL   *sqlRequest `json:"sql"`
	Mongo *struct {
		audit.ConnectionInput
		Database   string `json:"database"`
		Collection string `json:"collection"`
	} `json:"mongo"`
}

type healthRunDTO struct {
	ID     int64           `json:"id"`
	At     time.Time       `json:"at"`
	Kind   string          `json:"kind"`
	Label  string          `json:"label"`
	Status string          `json:"status"`
	Report json.RawMessage `json:"report"`
}

func healthDTO(r casestore.HealthRun) healthRunDTO {
	return healthRunDTO{ID: r.ID, At: r.At, Kind: r.Kind, Label: r.Label, Status: r.Status, Report: json.RawMessage(r.Report)}
}

// healthCaseOf opens the case for a health request (same 404 rule as tagCaseOf).
func (h *handler) healthCaseOf(w http.ResponseWriter, r *http.Request, raw string) (casestore.Case, casestore.HealthStore, bool) {
	if _, err := casestore.ParseID(raw); err != nil {
		if h.casesAvailable(w) {
			writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
		}
		return nil, nil, false
	}
	_, c, ok := h.openCase(w, r, raw)
	if !ok {
		return nil, nil, false
	}
	var inner casestore.Case = c
	if sc, wrapped := c.(*sharedCase); wrapped {
		inner = sc.Case
	}
	hs, ok := inner.(casestore.HealthStore)
	if !ok {
		c.Close()
		writeCaseFailure(w, http.StatusServiceUnavailable, "health-unavailable", "This case store cannot keep health check results.")
		return nil, nil, false
	}
	return c, hs, true
}

// okMongoName allows what MongoDB itself allows in a database or collection name and nothing that could change the shape of a command.
func okMongoName(s string, max int, allowDot bool) bool {
	if s == "" || len(s) > max || strings.ContainsAny(s, "\x00\"$/\\ ") || (!allowDot && strings.Contains(s, ".")) {
		return false
	}
	return true
}

func (h *handler) caseHealthRun(w http.ResponseWriter, r *http.Request, raw string) {
	if h.demo {
		writeError(w, http.StatusServiceUnavailable, healthDemoText)
		return
	}
	var body healthRunBody
	if !decodeJSON(w, r, &body) {
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

	var report dbhealth.Report
	switch body.Kind {
	case "sql":
		if body.SQL == nil {
			writeError(w, http.StatusBadRequest, "Pick a SQL Server connection to check.")
			return
		}
		target, label, ok := h.resolveSQL(w, *body.SQL)
		if !ok {
			return
		}
		if !healthBusy.TryLock() {
			writeError(w, http.StatusConflict, healthBusyText)
			return
		}
		defer healthBusy.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), healthRunTimeout)
		defer cancel()
		q, err := openSQLFn(ctx, target)
		if err != nil {
			writeConnectFailure(w, err)
			return
		}
		defer q.Close()
		report = dbhealth.RunSQL(ctx, q, label, target.Database, time.Now)
	case "mongo":
		if body.Mongo == nil || !okMongoName(body.Mongo.Database, 64, false) || (body.Mongo.Collection != "" && !okMongoName(body.Mongo.Collection, 120, true)) {
			writeError(w, http.StatusBadRequest, "Pick the MongoDB database to check.")
			return
		}
		input, ok := h.resolveConnection(w, body.Mongo.ConnectionInput)
		if !ok {
			return
		}
		opener, ok := h.repo.(mongoCommandOpener)
		if !ok {
			writeError(w, http.StatusBadRequest, "MongoDB health checks are unavailable for this repository.")
			return
		}
		if !healthBusy.TryLock() {
			writeError(w, http.StatusConflict, healthBusyText)
			return
		}
		defer healthBusy.Unlock()
		ctx, cancel := context.WithTimeout(r.Context(), healthRunTimeout)
		defer cancel()
		run, closeFn, err := opener.OpenCommandClient(ctx, input)
		if err != nil {
			if writeConnectionError(w, err) {
				return
			}
			writeError(w, http.StatusBadRequest, "Could not connect to MongoDB. Check the connection details and permissions.")
			return
		}
		defer closeFn()
		report = dbhealth.RunMongo(ctx, mongoAdapter(run), "MongoDB", body.Mongo.Database, body.Mongo.Collection, time.Now)
	default:
		writeError(w, http.StatusBadRequest, "Kind must be sql or mongo.")
		return
	}

	enc, err := json.Marshal(report)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "The result could not be prepared.")
		return
	}
	sctx, scancel := context.WithTimeout(r.Context(), requestTimeout)
	defer scancel()
	saved, err := hs.AddHealthRun(sctx, casestore.HealthRun{Kind: body.Kind, Label: report.Label, Status: string(report.Overall), Report: enc})
	out := struct {
		Saved bool         `json:"saved"`
		Run   healthRunDTO `json:"run"`
		Error string       `json:"saveError,omitempty"`
	}{}
	if err != nil {
		// The checks ran; the answer is still shown, it is just not kept.
		out.Run = healthRunDTO{At: report.FinishedAt, Kind: body.Kind, Label: report.Label, Status: string(report.Overall), Report: enc}
		out.Error = "The result could not be saved in the case."
		if errors.Is(err, casestore.ErrCaseClosed) {
			out.Error = "The case is closed, so the result was not saved."
		}
	} else {
		out.Saved, out.Run = true, healthDTO(saved)
	}
	writeJSON(w, http.StatusOK, out, maxResponseBytes)
}

func (h *handler) caseHealthList(w http.ResponseWriter, r *http.Request, raw string) {
	c, hs, ok := h.healthCaseOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	runs, err := hs.HealthRuns(ctx)
	if err != nil {
		writeTagError(w, err, "")
		return
	}
	out := make([]healthRunDTO, 0, len(runs))
	for _, run := range runs {
		if run.Key != "" { // a lane day belongs to the lanes list, not to the run history
			continue
		}
		out = append(out, healthDTO(run))
	}
	writeJSON(w, http.StatusOK, struct {
		Runs []healthRunDTO `json:"runs"`
	}{out}, maxResponseBytes)
}
