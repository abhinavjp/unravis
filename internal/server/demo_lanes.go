package server

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/history"
	"api-audit-explorer/internal/sqlconn"
	"api-audit-explorer/internal/store"
)

// The demo serves the SQL Server and MongoDB lane screens from fakes, so every one of them can be driven on --demo. Nothing here opens a
// network connection, reads a file or keeps a password: the saved connections live in memory and forget any password they are given (only
// the fact that one was offered is kept), and a read is answered by canned rows that go through the same lane code a real server does.
//
// The database name steers the answer, so each screen can be reached by typing a name:
//
//	SQL Server: AuditDb reads fine; NoGrantDb lacks a permission (the grant script shows); EmptyDb holds nothing for the hours.
//	MongoDB:    AuditDb reads fine; NoGrantDb lacks a role; ProfilerOffDb has the profiler switched off.
const (
	demoSQLServer   = "demo-sql.example.invalid"
	demoMongoServer = "mongodb://demo-mongo.example.invalid:27017"
)

var demoSQLDatabases = []string{"AuditDb", "NoGrantDb", "EmptyDb"}

// demoNameHas says whether the typed database name asks for one of the demo's special answers (case does not matter).
func demoNameHas(name, word string) bool { return strings.Contains(strings.ToLower(name), word) }

/* ---------------------------------------------------------- saved SQL Server */

type demoSQLStore struct {
	mu    sync.Mutex
	items []sqlconn.Recent
	seq   int
}

func newDemoSQLStore(now time.Time) *demoSQLStore {
	return &demoSQLStore{items: []sqlconn.Recent{{
		ID: "demo-sql-1", Label: "Demo SQL Server", Server: demoSQLServer, Host: demoSQLServer, Port: 1433, Auth: sqlconn.AuthSQL, Username: "unravis_ro",
		Database: "AuditDb", Encrypt: sqlconn.EncryptMandatory, HasPassword: true, SavedAt: now,
	}}}
}

func (s *demoSQLStore) List() ([]sqlconn.Recent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sqlconn.Recent(nil), s.items...), nil
}

// Resolve gives the saved details back with no password: the demo never connects, so it has nothing to send one to.
func (s *demoSQLStore) Resolve(id string) (sqlconn.Input, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.items {
		if r.ID == id {
			return sqlconn.Input{Label: r.Label, Server: r.Server, Auth: r.Auth, Username: r.Username, Database: r.Database, Encrypt: r.Encrypt,
				TrustServerCertificate: r.TrustServerCertificate}, nil
		}
	}
	return sqlconn.Input{}, sqlconn.ErrNotFound
}

func (s *demoSQLStore) Save(in sqlconn.Input) (sqlconn.Recent, error) {
	rec, err := sqlconn.Normalize(in)
	if err != nil {
		return sqlconn.Recent{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec.SavedAt = time.Now().UTC()
	for i, prior := range s.items {
		if prior.Host == rec.Host && prior.Port == rec.Port && prior.Username == rec.Username && prior.Auth == rec.Auth && strings.EqualFold(prior.Database, rec.Database) {
			rec.ID = prior.ID
			// Like the real store: a blank password with "Remember password" still ticked keeps the saved one, but only while encryption
			// stays as strict. The demo keeps no password, only the fact that one is saved. This check comes before the prior is removed.
			if !rec.HasPassword && in.RememberPassword && rec.Auth == sqlconn.AuthSQL && prior.HasPassword {
				if !sqlconn.KeepsPassword(rec, prior) {
					return sqlconn.Recent{}, sqlconn.ErrPasswordAgain
				}
				rec.HasPassword = true
			}
			s.items = append(s.items[:i], s.items[i+1:]...)
			break
		}
	}
	if len(s.items) >= sqlconn.MaxRecords {
		return sqlconn.Recent{}, sqlconn.ErrFull
	}
	if rec.ID == "" {
		s.seq++
		rec.ID = "demo-sql-new-" + strconv.Itoa(s.seq)
	}
	s.items = append([]sqlconn.Recent{rec}, s.items...)
	return rec, nil
}

func (s *demoSQLStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.items {
		if r.ID == id {
			s.items = append(s.items[:i], s.items[i+1:]...)
			return nil
		}
	}
	return sqlconn.ErrNotFound
}

/* ------------------------------------------------------------ saved MongoDB */

type demoHistory struct {
	mu    sync.Mutex
	items []history.Recent
	seq   int
}

func newDemoHistory(now time.Time) *demoHistory {
	return &demoHistory{items: []history.Recent{{
		ID: "demo-mongo-1", Label: "Demo MongoDB", URI: demoMongoServer, Username: "unravis_ro", AuthSource: "admin", TLSMode: "required",
		Collection: "Logs", HasCredentials: true, LastUsedAt: now,
	}}}
}

func (s *demoHistory) List() ([]history.Recent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]history.Recent(nil), s.items...), nil
}

func (s *demoHistory) Remember(label string, in audit.ConnectionInput, collection string, remember bool) (history.Recent, error) {
	if strings.TrimSpace(in.URI) == "" {
		return history.Recent{}, audit.ErrInvalidInput
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) >= history.MaxRecords {
		return history.Recent{}, history.ErrFull
	}
	s.seq++
	rec := history.Recent{ID: "demo-mongo-new-" + strconv.Itoa(s.seq), Label: label, URI: in.URI, Username: in.Username, AuthSource: in.AuthSource, TLSMode: in.TLSMode,
		Collection: collection, HasCredentials: remember && in.Password != "", LastUsedAt: time.Now().UTC()}
	s.items = append([]history.Recent{rec}, s.items...)
	return rec, nil
}

func (s *demoHistory) Resolve(in audit.ConnectionInput) (audit.ConnectionInput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.items {
		if r.ID == in.SavedConnectionID {
			in.URI, in.Username, in.AuthSource, in.TLSMode = r.URI, r.Username, r.AuthSource, r.TLSMode
			return in, nil
		}
	}
	return audit.ConnectionInput{}, history.ErrNotFound
}

func (s *demoHistory) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.items {
		if r.ID == id {
			s.items = append(s.items[:i], s.items[i+1:]...)
			return nil
		}
	}
	return history.ErrNotFound
}

/* ------------------------------------------------------------------- reads */

// demoTestResult is what a connection test says in the demo: it connected and found three databases.
func demoTestResult() dbhealth.TestResult {
	return dbhealth.TestResult{Version: "16.0.1000.6", Edition: "Developer (demo)", Databases: append([]string(nil), demoSQLDatabases...)}
}

// demoSQLRows answers the lane's queries from canned rows placed around the windows the person asked for. It implements healthQueryer.
type demoSQLRows struct {
	by  map[string][]dbhealth.Row
	err map[string]error
	// rest, when set, is the answer to every query except the first one (the version and time zone).
	rest error
}

func (d demoSQLRows) Query(_ context.Context, text string) ([]dbhealth.Row, error) {
	if d.rest != nil && !strings.Contains(text, "unravis:base") {
		return nil, d.rest
	}
	for k, e := range d.err {
		if strings.Contains(text, k) {
			return nil, e
		}
	}
	for k, r := range d.by {
		if strings.Contains(text, k) {
			return r, nil
		}
	}
	return nil, nil
}
func (demoSQLRows) Close() {}

func demoSQLQueryer(req dbhealth.LaneRequest) healthQueryer {
	ref := req.Reference
	stat := func(win string, ms, reads float64) dbhealth.Row {
		return dbhealth.Row{"query_id": int64(1), "plan_id": int64(10), "win": win, "execs": int64(50), "avg_ms": ms, "avg_reads": reads}
	}
	rows := demoSQLRows{by: map[string][]dbhealth.Row{
		"unravis:base":       {{"version": "16.0.1000.6", "edition": "Developer (demo)", "hadr": int64(0), "utc_offset_min": int64(0)}},
		"unravis:server":     {{"started_local": ref.Start.Add(-24 * time.Hour), "threadpool_tasks": int64(0)}},
		"unravis:qs_options": {{"actual_state_desc": "READ_WRITE", "desired_state_desc": "READ_WRITE", "readonly_reason": int64(0), "interval_length_minutes": int64(15)}},
		"unravis:qs_first":   {{"first_start": ref.Start.Add(-48 * time.Hour)}},
		"unravis:qs_stats":   {stat("ref", 10, 100), stat("inc", 11, 105)},
	}}
	switch {
	case demoNameHas(req.Database, "empty"):
		// The server answers its version and then has nothing to read for these hours: every check says Cannot test yet, none has a permission to fix.
		rows.rest = errors.New("Invalid object name 'sys.query_store_runtime_stats'.")
	case demoNameHas(req.Database, "nogrant"):
		denied := errors.New("The SELECT permission was denied on the object 'database_query_store_options', database 'AuditDb'.")
		rows.err = map[string]error{"unravis:qs_options": denied, "unravis:qs_first": denied, "unravis:qs_stats": denied}
	}
	return rows
}

func demoMongoBatch(docs ...map[string]any) map[string]any {
	arr := make([]any, len(docs))
	for i, d := range docs {
		arr[i] = d
	}
	return map[string]any{"cursor": map[string]any{"firstBatch": arr}}
}

func demoMongoDate(t time.Time) map[string]any {
	return map[string]any{"$date": t.UTC().Format(time.RFC3339)}
}

// demoMongoRun answers the lane's commands from canned documents placed around the windows asked for.
func demoMongoRun(req dbhealth.LaneRequest) store.MongoCommandFunc {
	return func(_ context.Context, _, cmd string) (map[string]any, error) {
		switch {
		case strings.HasPrefix(cmd, `{"serverStatus"`):
			if demoNameHas(req.Database, "nogrant") {
				return nil, errors.New("not authorized on admin to execute command { serverStatus: 1 } for user unravis_ro")
			}
			return map[string]any{"version": "7.0.5", "uptime": float64(86400 * 9)}, nil
		case strings.HasPrefix(cmd, `{"profile"`):
			if demoNameHas(req.Database, "profileroff") {
				return map[string]any{"was": float64(0), "slowms": float64(100)}, nil
			}
			return map[string]any{"was": float64(1), "slowms": float64(100)}, nil
		case strings.Contains(cmd, `"limit":1,`):
			return demoMongoBatch(map[string]any{"ts": demoMongoDate(req.Reference.Start.Add(-24 * time.Hour))}), nil
		}
		return demoMongoBatch(map[string]any{
			"ts": demoMongoDate(req.Incident.Start.Add(10 * time.Minute)), "ns": req.Database + ".Logs", "op": "query", "millis": float64(900), "planSummary": "COLLSCAN",
		}), nil
	}
}
