package keeper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/history"
	"api-audit-explorer/internal/sqlconn"
)

// Runner opens the connections the keeper makes with a saved secret and runs the fixed check catalogues on them. RealRunner uses the
// SQL Server and MongoDB drivers; tests give a fake so nothing ever connects.
type Runner interface {
	TestSQL(ctx context.Context, t dbhealth.SQLTarget) (dbhealth.TestResult, error)
	HealthSQL(ctx context.Context, t dbhealth.SQLTarget, label string) (dbhealth.Report, error)
	LaneSQL(ctx context.Context, t dbhealth.SQLTarget, label string, req dbhealth.LaneRequest) (dbhealth.Report, error)
	TestMongo(ctx context.Context, in audit.ConnectionInput) (audit.ConnectionResult, error)
	ValidateMongo(in audit.ConnectionInput) error
	HealthMongo(ctx context.Context, in audit.ConnectionInput, database, collection string) (dbhealth.Report, error)
	LaneMongo(ctx context.Context, in audit.ConnectionInput, label string, req dbhealth.LaneRequest) (dbhealth.Report, error)
}

// Backend is what the keeper does with its sealed stores. Every field but Runner and HTTP is required.
type Backend struct {
	SQL    *sqlconn.Service
	Mongo  *history.Service
	BB     *bbsaved.Store
	Runner Runner
	// HTTP reads a Blackbox by URL. nil builds one with no proxy, no redirects and BBRequestTimeout.
	HTTP *http.Client
	// Audit hosts the MongoDB audit sources opened from saved links (P14b). nil leaves the audit.* operations out.
	Audit *AuditHost

	busyMu sync.Mutex
	busy   map[string]bool
}

// Timeouts and limits of the keeper's operations.
const (
	storeTimeout     = 15 * time.Second
	reachTimeout     = 60 * time.Second
	runTimeout       = 150 * time.Second // same as one health run in Unravis
	bbFileTimeout    = 5 * time.Minute
	BBRequestTimeout = 30 * time.Second
	// BBMaxBody bounds one Blackbox file passed through the keeper.
	BBMaxBody = 256 << 20
)

// Register adds the backend's operations to s.
func (b *Backend) Register(s *Server) {
	s.Handle(OpSQLList, classRead, storeTimeout, b.sqlList)
	s.Handle(OpSQLDescribe, classRead, storeTimeout, b.sqlDescribe)
	s.Handle(OpSQLSave, classWrite, storeTimeout, b.sqlSave)
	s.Handle(OpSQLDelete, classWrite, storeTimeout, b.sqlDelete)
	s.Handle(OpSQLImport, classWrite, storeTimeout, b.sqlImport)
	s.Handle(OpSQLTest, classReach, reachTimeout, b.sqlTest)
	s.Handle(OpSQLHealth, classReach, runTimeout, b.sqlHealth)
	s.Handle(OpSQLLane, classReach, runTimeout, b.sqlLane)

	s.Handle(OpMongoList, classRead, storeTimeout, b.mongoList)
	s.Handle(OpMongoDescribe, classRead, storeTimeout, b.mongoDescribe)
	s.Handle(OpMongoRemember, classWrite, storeTimeout, b.mongoRemember)
	s.Handle(OpMongoDelete, classWrite, storeTimeout, b.mongoDelete)
	s.Handle(OpMongoImport, classWrite, storeTimeout, b.mongoImport)
	s.Handle(OpMongoTest, classReach, reachTimeout, b.mongoTest)
	s.Handle(OpMongoHealth, classReach, runTimeout, b.mongoHealth)
	s.Handle(OpMongoLane, classReach, runTimeout, b.mongoLane)

	s.Handle(OpBBList, classRead, storeTimeout, b.bbList)
	s.Handle(OpBBGet, classRead, storeTimeout, b.bbGet)
	s.Handle(OpBBFind, classRead, storeTimeout, b.bbFind)
	s.Handle(OpBBAdd, classWrite, storeTimeout, b.bbAdd)
	s.Handle(OpBBSetThisPC, classWrite, storeTimeout, b.bbSetThisPC)
	s.Handle(OpBBSetKey, classWrite, storeTimeout, b.bbSetKey)
	s.Handle(OpBBSetRecorder, classWrite, storeTimeout, b.bbSetRecorder)
	s.Handle(OpBBRemove, classWrite, storeTimeout, b.bbRemove)
	s.Handle(OpBBImport, classWrite, storeTimeout, b.bbImport)
	s.Handle(OpBBHTTP, classBlob, bbFileTimeout, b.bbHTTP)

	if b.Audit != nil {
		b.registerAudit(s)
	}
}

func (b *Backend) claim(key string) bool {
	b.busyMu.Lock()
	defer b.busyMu.Unlock()
	if b.busy == nil {
		b.busy = map[string]bool{}
	}
	if b.busy[key] {
		return false
	}
	b.busy[key] = true
	return true
}

func (b *Backend) release(key string) {
	b.busyMu.Lock()
	delete(b.busy, key)
	b.busyMu.Unlock()
}

var errBusy = &WireError{Code: CodeBusy, Message: "A check is already running on this saved connection. Wait for it to finish."}

/* ---------------------------------------------------------------- SQL */

// IDArgs names one saved record.
type IDArgs struct {
	ID string `json:"id"`
}

// SQLRef is a saved SQL Server login plus what the person may change for one run: a typed password (used instead of the stored one)
// and the database.
type SQLRef struct {
	ID       string `json:"id"`
	Password string `json:"password,omitempty"`
	Database string `json:"database,omitempty"`
}

// SQLTestResult is the answer of sql.test. ListError is set when the login connected but could not list the databases.
type SQLTestResult struct {
	Result    dbhealth.TestResult `json:"result"`
	ListError string              `json:"listError,omitempty"`
}

// SQLRunArgs is sql.health and sql.lane.
type SQLRunArgs struct {
	Ref   SQLRef                `json:"ref"`
	Label string                `json:"label,omitempty"`
	Lane  *dbhealth.LaneRequest `json:"lane,omitempty"`
}

// ImportResult says how many records were added.
type ImportResult struct {
	Imported int `json:"imported"`
}

func (b *Backend) sqlList(_ context.Context, _ *Call) (any, error) {
	items, err := b.SQL.List()
	if items == nil && err == nil {
		items = []sqlconn.Recent{}
	}
	return items, err
}

func (b *Backend) sqlDescribe(_ context.Context, c *Call) (any, error) {
	var a IDArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	c.Record = a.ID
	return describeSQL(b.SQL, a.ID)
}

func describeSQL(s *sqlconn.Service, id string) (sqlconn.Recent, error) {
	items, err := s.List()
	if err != nil {
		return sqlconn.Recent{}, err
	}
	for _, it := range items {
		if it.ID == id {
			return it, nil
		}
	}
	return sqlconn.Recent{}, sqlconn.ErrNotFound
}

func (b *Backend) sqlSave(_ context.Context, c *Call) (any, error) {
	var in sqlconn.Input
	if err := c.Decode(&in); err != nil {
		return nil, err
	}
	rec, err := b.SQL.Save(in)
	c.Record = rec.ID
	return rec, err
}

func (b *Backend) sqlDelete(_ context.Context, c *Call) (any, error) {
	var a IDArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	c.Record = a.ID
	return struct{}{}, b.SQL.Delete(a.ID)
}

func (b *Backend) sqlImport(_ context.Context, c *Call) (any, error) {
	var a struct {
		Records []sqlconn.Record `json:"records"`
	}
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	n, err := b.SQL.Import(a.Records)
	return ImportResult{Imported: n}, err
}

// sqlTarget builds the login for a saved SQL Server connection. Only SQL Server sign-in runs here: Windows sign-in would run as the
// keeper's own service account, so it is refused and runs in the person's own Unravis instead.
func (b *Backend) sqlTarget(ref SQLRef) (dbhealth.SQLTarget, string, error) {
	if ref.ID == "" {
		return dbhealth.SQLTarget{}, "", &WireError{Code: CodeInvalid, Message: "Pick a saved connection."}
	}
	in, err := b.SQL.Resolve(ref.ID)
	if err != nil {
		return dbhealth.SQLTarget{}, "", err
	}
	if in.Auth != sqlconn.AuthSQL {
		return dbhealth.SQLTarget{}, "", &WireError{Code: CodeRefused, Message: "Windows sign-in runs in your own Unravis, not in the key keeper."}
	}
	if ref.Password != "" {
		in.Password = ref.Password
	}
	if ref.Database != "" {
		in.Database = ref.Database
	}
	return dbhealth.SQLTarget{
		Server: in.Server, Auth: in.Auth, Username: in.Username, Password: in.Password,
		Database: in.Database, Encrypt: in.Encrypt, TrustServerCertificate: in.TrustServerCertificate,
	}, in.Label, nil
}

func (b *Backend) sqlTest(ctx context.Context, c *Call) (any, error) {
	var ref SQLRef
	if err := c.Decode(&ref); err != nil {
		return nil, err
	}
	c.Record = ref.ID
	t, _, err := b.sqlTarget(ref)
	if err != nil {
		return nil, err
	}
	res, err := b.Runner.TestSQL(ctx, t)
	out := SQLTestResult{Result: res}
	if out.Result.Databases == nil {
		out.Result.Databases = []string{}
	}
	if err != nil {
		ce := dbhealth.ClassifyConnect(err)
		if ce.Code != "list" {
			return nil, ce
		}
		out.ListError = ce.Message
	}
	return out, nil
}

func (b *Backend) sqlHealth(ctx context.Context, c *Call) (any, error) {
	return b.sqlRun(ctx, c, false)
}

func (b *Backend) sqlLane(ctx context.Context, c *Call) (any, error) {
	return b.sqlRun(ctx, c, true)
}

func (b *Backend) sqlRun(ctx context.Context, c *Call, lane bool) (any, error) {
	var a SQLRunArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	c.Record = a.Ref.ID
	t, label, err := b.sqlTarget(a.Ref)
	if err != nil {
		return nil, err
	}
	if a.Label != "" {
		label = a.Label
	}
	var req dbhealth.LaneRequest
	if lane {
		if a.Lane == nil {
			return nil, &WireError{Code: CodeInvalid, Message: "Pick the incident and reference windows."}
		}
		req = *a.Lane
		req.Database, req.Login, req.Collection = t.Database, t.Username, ""
		if err := req.Validate(); err != nil {
			return nil, &WireError{Code: CodeInvalid, Message: "The incident and reference windows cannot be compared."}
		}
		if _, ok := dbhealth.QuoteIdent(t.Database); !ok {
			return nil, &WireError{Code: CodeInvalid, Message: "Pick the database to read."}
		}
	}
	if !b.claim("sql:" + a.Ref.ID) {
		return nil, errBusy
	}
	defer b.release("sql:" + a.Ref.ID)
	if lane {
		return b.Runner.LaneSQL(ctx, t, label, req)
	}
	return b.Runner.HealthSQL(ctx, t, label)
}

/* -------------------------------------------------------------- Mongo */

// MongoRememberArgs is mongo.remember.
type MongoRememberArgs struct {
	Label            string                `json:"label"`
	Connection       audit.ConnectionInput `json:"connection"`
	Collection       string                `json:"collection"`
	RememberPassword bool                  `json:"rememberPassword"`
}

// MongoConnArgs is mongo.test.
type MongoConnArgs struct {
	Connection audit.ConnectionInput `json:"connection"`
}

// MongoRunArgs is mongo.health and mongo.lane.
type MongoRunArgs struct {
	Connection audit.ConnectionInput `json:"connection"`
	Database   string                `json:"database,omitempty"`
	Collection string                `json:"collection,omitempty"`
	Label      string                `json:"label,omitempty"`
	Lane       *dbhealth.LaneRequest `json:"lane,omitempty"`
}

func (b *Backend) mongoList(_ context.Context, _ *Call) (any, error) {
	items, err := b.Mongo.List()
	if items == nil && err == nil {
		items = []history.Recent{}
	}
	return items, err
}

func (b *Backend) mongoDescribe(_ context.Context, c *Call) (any, error) {
	var a IDArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	c.Record = a.ID
	return describeMongo(b.Mongo, a.ID)
}

func describeMongo(s *history.Service, id string) (history.Recent, error) {
	items, err := s.List()
	if err != nil {
		return history.Recent{}, err
	}
	for _, it := range items {
		if it.ID == id {
			return it, nil
		}
	}
	return history.Recent{}, history.ErrNotFound
}

// mongoResolve fills the stored password of a saved link. Only saved links are served: the keeper is not a general way to reach any
// MongoDB from the service account.
func (b *Backend) mongoResolve(in audit.ConnectionInput) (audit.ConnectionInput, error) {
	if in.SavedConnectionID == "" {
		return audit.ConnectionInput{}, &WireError{Code: CodeInvalid, Message: "Pick a saved connection."}
	}
	out, err := b.Mongo.Resolve(in)
	if err != nil {
		if errors.Is(err, history.ErrNotFound) || errors.Is(err, history.ErrCorrupt) || errors.Is(err, history.ErrUnavailable) {
			return audit.ConnectionInput{}, err
		}
		return audit.ConnectionInput{}, &WireError{Code: CodeMismatch, Message: "Saved connection no longer matches these settings. Select it again or enter the password."}
	}
	return out, nil
}

// errMongoOwnUnravis is the one refusal for a MongoDB link the keeper will not open under its own Windows account.
func errMongoOwnUnravis() error {
	return &WireError{Code: CodeRefused, Message: "This MongoDB link runs in your own Unravis, not in the key keeper."}
}

// mongoNamesFiles reports whether a MongoDB link names a file on this PC in its options (a CA, a client certificate, a key or its
// password file, a revocation list). The driver opens such files itself, so in the keeper it would read them as the keeper's own
// account. A pasted CA certificate (custom TLS mode) is text, not a file, and is fine.
func mongoNamesFiles(uri string) bool {
	raw := uri
	if !strings.Contains(raw, "://") {
		raw = "mongodb://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return true
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return true
	}
	for name := range q {
		n := strings.ToLower(name)
		if (strings.HasPrefix(n, "tls") || strings.HasPrefix(n, "ssl")) && (strings.Contains(n, "file") || strings.Contains(n, "password") || strings.Contains(n, "key")) {
			return true
		}
	}
	return false
}

// keeperMongoAllowed is what the keeper checks before it opens a saved MongoDB link itself: a user name and the stored password, a
// password sign-in (SCRAM, the default), and no option that names a file. Anything else (Kerberos, X.509, AWS, LDAP, a link with no
// credentials) would sign in or read files as the keeper's own account, so it runs in the person's own Unravis instead.
func keeperMongoAllowed(in audit.ConnectionInput) error {
	if in.Username == "" || in.Password == "" || mongoNamesFiles(in.URI) {
		return errMongoOwnUnravis()
	}
	raw := in.URI
	if !strings.Contains(raw, "://") {
		raw = "mongodb://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return errMongoOwnUnravis()
	}
	for name, values := range u.Query() {
		if !strings.EqualFold(name, "authMechanism") {
			continue
		}
		for _, v := range values {
			if !strings.EqualFold(v, "SCRAM-SHA-1") && !strings.EqualFold(v, "SCRAM-SHA-256") {
				return errMongoOwnUnravis()
			}
		}
	}
	return nil
}

func (b *Backend) mongoRemember(_ context.Context, c *Call) (any, error) {
	var a MongoRememberArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	id := a.Connection.SavedConnectionID
	conn := a.Connection
	if id != "" {
		var err error
		if conn, err = b.mongoResolve(a.Connection); err != nil {
			return nil, err
		}
	}
	if mongoNamesFiles(conn.URI) {
		// Even checking such a link would make the driver open the named files as the keeper's account.
		return nil, &WireError{Code: CodeRefused, Message: "A MongoDB link that names certificate files on this PC cannot be saved in the shared install. Paste the CA certificate instead."}
	}
	if err := b.Runner.ValidateMongo(conn); err != nil {
		return nil, err
	}
	conn.SavedConnectionID = id
	if !a.RememberPassword {
		// The keeper keeps no per-session passwords: a password the person did not ask to remember stays in their own Unravis.
		conn.Password = ""
	}
	rec, err := b.Mongo.Remember(a.Label, conn, a.Collection, a.RememberPassword)
	c.Record = rec.ID
	return rec, err
}

func (b *Backend) mongoDelete(_ context.Context, c *Call) (any, error) {
	var a IDArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	c.Record = a.ID
	return struct{}{}, b.Mongo.Delete(a.ID)
}

func (b *Backend) mongoImport(_ context.Context, c *Call) (any, error) {
	var a struct {
		Records []history.Record `json:"records"`
	}
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	n, err := b.Mongo.Import(a.Records)
	return ImportResult{Imported: n}, err
}

func (b *Backend) mongoTest(ctx context.Context, c *Call) (any, error) {
	var a MongoConnArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	c.Record = a.Connection.SavedConnectionID
	in, err := b.mongoResolve(a.Connection)
	if err != nil {
		return nil, err
	}
	if err := keeperMongoAllowed(in); err != nil {
		return nil, err
	}
	res, err := b.Runner.TestMongo(ctx, in)
	if err != nil {
		return nil, err
	}
	if res.Databases == nil {
		res.Databases = []string{}
	}
	return res, nil
}

func okMongoName(s string, max int, allowDot bool) bool {
	return s != "" && len(s) <= max && !strings.ContainsAny(s, "\x00\"$/\\ ") && (allowDot || !strings.Contains(s, "."))
}

func (b *Backend) mongoHealth(ctx context.Context, c *Call) (any, error) {
	return b.mongoRun(ctx, c, false)
}

func (b *Backend) mongoLane(ctx context.Context, c *Call) (any, error) {
	return b.mongoRun(ctx, c, true)
}

func (b *Backend) mongoRun(ctx context.Context, c *Call, lane bool) (any, error) {
	var a MongoRunArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	c.Record = a.Connection.SavedConnectionID
	var req dbhealth.LaneRequest
	if lane {
		if a.Lane == nil {
			return nil, &WireError{Code: CodeInvalid, Message: "Pick the incident and reference windows."}
		}
		req = *a.Lane
		a.Database, a.Collection = req.Database, req.Collection
		if err := req.Validate(); err != nil {
			return nil, &WireError{Code: CodeInvalid, Message: "The incident and reference windows cannot be compared."}
		}
	}
	if !okMongoName(a.Database, 64, false) || (a.Collection != "" && !okMongoName(a.Collection, 120, true)) {
		return nil, &WireError{Code: CodeInvalid, Message: "Pick the MongoDB database to check."}
	}
	in, err := b.mongoResolve(a.Connection)
	if err != nil {
		return nil, err
	}
	if err := keeperMongoAllowed(in); err != nil {
		return nil, err
	}
	if !b.claim("mongo:" + a.Connection.SavedConnectionID) {
		return nil, errBusy
	}
	defer b.release("mongo:" + a.Connection.SavedConnectionID)
	if lane {
		req.Login = in.Username
		label := a.Label
		if label == "" {
			label = "MongoDB"
		}
		return b.Runner.LaneMongo(ctx, in, label, req)
	}
	return b.Runner.HealthMongo(ctx, in, a.Database, a.Collection)
}

/* ----------------------------------------------------------- Blackbox */

// BBAddArgs is bb.add (bbsaved.AddInput on the wire).
type BBAddArgs struct {
	Name         string            `json:"name"`
	Location     blackbox.Location `json:"location"`
	Key          string            `json:"key,omitempty"`
	ThisPC       bool              `json:"thisPc"`
	RecorderHost string            `json:"recorderHost,omitempty"`
	RecorderName string            `json:"recorderName,omitempty"`
}

// BBFindArgs is bb.find.
type BBFindArgs struct {
	Canon string `json:"canon"`
	Host  string `json:"host"`
	Name  string `json:"name"`
}

// BBFindResult is the answer of bb.find.
type BBFindResult struct {
	Entry bbsaved.Entry `json:"entry"`
	Found bool          `json:"found"`
}

// BBSetArgs is bb.setThisPC, bb.setKey and bb.setRecorder.
type BBSetArgs struct {
	ID     string `json:"id"`
	ThisPC bool   `json:"thisPc,omitempty"`
	Key    string `json:"key,omitempty"`
	Host   string `json:"host,omitempty"`
	Name   string `json:"name,omitempty"`
}

// BBHTTPArgs is bb.http: one GET on a saved Blackbox reached by URL.
type BBHTTPArgs struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// BBHTTPResult is the head of a bb.http answer. A 200 answer streams the body.
type BBHTTPResult struct {
	Status      int    `json:"status"`
	Time        string `json:"time,omitempty"`
	ContentType string `json:"contentType,omitempty"`
}

func (b *Backend) bbList(_ context.Context, _ *Call) (any, error) {
	items, err := b.BB.List()
	if items == nil && err == nil {
		items = []bbsaved.Entry{}
	}
	return items, err
}

func (b *Backend) bbGet(_ context.Context, c *Call) (any, error) {
	var a IDArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	c.Record = a.ID
	return b.BB.Get(a.ID)
}

func (b *Backend) bbFind(_ context.Context, c *Call) (any, error) {
	var a BBFindArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	e, ok := b.BB.Find(a.Canon, a.Host, a.Name)
	return BBFindResult{Entry: e, Found: ok}, nil
}

func (b *Backend) bbAdd(_ context.Context, c *Call) (any, error) {
	var a BBAddArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	e, err := b.BB.Add(bbsaved.AddInput{Name: a.Name, Location: a.Location, Key: a.Key, ThisPC: a.ThisPC, RecorderHost: a.RecorderHost, RecorderName: a.RecorderName})
	c.Record = e.ID
	if errors.Is(err, bbsaved.ErrDuplicate) {
		detail, _ := json.Marshal(e)
		return nil, &WireError{Code: CodeDuplicate, Message: bbsaved.ErrDuplicate.Error(), Detail: detail}
	}
	return e, err
}

func (b *Backend) bbSetThisPC(_ context.Context, c *Call) (any, error) {
	var a BBSetArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	c.Record = a.ID
	return struct{}{}, b.BB.SetThisPC(a.ID, a.ThisPC)
}

func (b *Backend) bbSetKey(_ context.Context, c *Call) (any, error) {
	var a BBSetArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	c.Record = a.ID
	return struct{}{}, b.BB.SetKey(a.ID, a.Key)
}

func (b *Backend) bbSetRecorder(_ context.Context, c *Call) (any, error) {
	var a BBSetArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	c.Record = a.ID
	return struct{}{}, b.BB.SetRecorder(a.ID, a.Host, a.Name)
}

func (b *Backend) bbRemove(_ context.Context, c *Call) (any, error) {
	var a IDArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	c.Record = a.ID
	return struct{}{}, b.BB.Remove(a.ID)
}

func (b *Backend) bbImport(_ context.Context, c *Call) (any, error) {
	var a struct {
		Records []bbsaved.Record `json:"records"`
	}
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	n, err := b.BB.Import(a.Records)
	return ImportResult{Imported: n}, err
}

// bbPathAllowed is the whole list of what the keeper GETs from a Blackbox: the manifest, the status and one recording file.
func bbPathAllowed(p string) bool {
	if p == "/manifest" || p == "/status" {
		return true
	}
	name, ok := strings.CutPrefix(p, "/files/")
	return ok && blackbox.ValidFileName(name)
}

func (b *Backend) httpClient() *http.Client {
	if b.HTTP != nil {
		c := *b.HTTP
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		return &c
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	return &http.Client{Transport: t, Timeout: BBRequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// bbReadFailure is a transport failure reading a Blackbox, worded without the address or the request (which holds the key).
func bbReadFailure(err error) *WireError {
	timeout := errors.Is(err, context.DeadlineExceeded)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		timeout = true
	}
	detail, _ := json.Marshal(map[string]any{"kind": string(blackbox.ReadUnreachable), "timeout": timeout})
	return &WireError{Code: CodeBBRead, Message: "The Blackbox could not be reached from the key keeper.", Detail: detail}
}

func (b *Backend) bbHTTP(ctx context.Context, c *Call) (any, error) {
	var a BBHTTPArgs
	if err := c.Decode(&a); err != nil {
		return nil, err
	}
	c.Record = a.ID
	if !bbPathAllowed(a.Path) {
		return nil, &WireError{Code: CodeRefused, Message: "The key keeper reads only the manifest, the status and recording files of a Blackbox."}
	}
	e, err := b.BB.Get(a.ID)
	if err != nil {
		return nil, err
	}
	loc, err := blackbox.ParseLocation(e.Location)
	if err != nil || loc.Kind != blackbox.LocURL {
		return nil, &WireError{Code: CodeRefused, Message: "Only a Blackbox reached by an address is read through the key keeper."}
	}
	key, err := b.BB.Key(a.ID)
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, &WireError{Code: CodeNotFound, Message: "No read key is saved for this Blackbox."}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc.BaseURL()+a.Path, nil)
	if err != nil {
		return nil, &WireError{Code: CodeInvalid, Message: "That address cannot be used."}
	}
	req.Header.Set(blackbox.KeyHeader, key)
	req.Header.Set("Accept", "application/json, application/x-ndjson")
	resp, err := b.httpClient().Do(req)
	if err != nil {
		return nil, bbReadFailure(err)
	}
	head := BBHTTPResult{Status: resp.StatusCode, Time: resp.Header.Get(blackbox.TimeHeader), ContentType: resp.Header.Get("Content-Type")}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return head, nil
	}
	c.Stream(func(_ context.Context, w io.Writer) error {
		defer resp.Body.Close()
		n, err := io.Copy(w, io.LimitReader(resp.Body, BBMaxBody+1))
		if err != nil {
			return bbReadFailure(err)
		}
		if n > BBMaxBody {
			detail, _ := json.Marshal(map[string]any{"kind": string(blackbox.ReadBadData)})
			return &WireError{Code: CodeBBRead, Message: "The Blackbox sent more data than a recording file holds.", Detail: detail}
		}
		return nil
	})
	return head, nil
}

/* ------------------------------------------------------------ errors */

// classify turns an error into what goes on the wire. Only fixed, known messages travel: a driver error is reduced to its
// classification, and anything unknown becomes "internal" with a fixed sentence (its text could hold an address).
func classify(err error) *WireError {
	var we *WireError
	if errors.As(err, &we) {
		return we
	}
	var ce *dbhealth.ConnectError
	if errors.As(err, &ce) {
		detail, _ := json.Marshal(map[string]string{"code": ce.Code, "message": ce.Message})
		return &WireError{Code: CodeConnect, Message: ce.Message, Detail: detail}
	}
	var ae *audit.ConnectionError
	if errors.As(err, &ae) {
		detail, _ := json.Marshal(map[string]string{"stage": ae.Stage, "code": ae.Code, "message": ae.Message})
		return &WireError{Code: CodeMongoConnect, Message: ae.Message, Detail: detail}
	}
	var re *blackbox.ReadError
	if errors.As(err, &re) {
		detail, _ := json.Marshal(map[string]string{"kind": string(re.Kind), "message": re.Message})
		return &WireError{Code: CodeBBRead, Message: re.Message, Detail: detail}
	}
	switch {
	case errors.Is(err, sqlconn.ErrNotFound), errors.Is(err, history.ErrNotFound), errors.Is(err, bbsaved.ErrNotFound):
		return &WireError{Code: CodeNotFound, Message: "Saved connection was not found."}
	case errors.Is(err, sqlconn.ErrPasswordAgain):
		return &WireError{Code: CodePasswordAgain, Message: "Type the password again to save weaker encryption settings."}
	case errors.Is(err, sqlconn.ErrFull), errors.Is(err, history.ErrFull), errors.Is(err, bbsaved.ErrTooMany):
		return &WireError{Code: CodeFull, Message: "The saved list is full."}
	case errors.Is(err, bbsaved.ErrDuplicate):
		return &WireError{Code: CodeDuplicate, Message: bbsaved.ErrDuplicate.Error()}
	case errors.Is(err, sqlconn.ErrInvalid), errors.Is(err, bbsaved.ErrInvalid), errors.Is(err, audit.ErrInvalidInput):
		return &WireError{Code: CodeInvalid, Message: "The details are not valid."}
	case errors.Is(err, sqlconn.ErrCorrupt), errors.Is(err, history.ErrCorrupt), errors.Is(err, bbsaved.ErrCorrupt):
		return &WireError{Code: CodeCorrupt, Message: "The key keeper's saved file could not be read. It was left as it is."}
	case errors.Is(err, sqlconn.ErrUnavailable), errors.Is(err, history.ErrUnavailable), errors.Is(err, bbsaved.ErrUnavailable):
		return &WireError{Code: CodeUnavailable, Message: "The key keeper's saved files are unavailable."}
	case errors.Is(err, context.DeadlineExceeded):
		return &WireError{Code: CodeUnavailable, Message: "The key keeper took too long."}
	}
	return &WireError{Code: CodeInternal, Message: "The key keeper failed on that request."}
}
