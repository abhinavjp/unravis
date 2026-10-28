package keeper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
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

// The stores below are what Unravis uses in place of its own sealed files when the key keeper is in use. Each one has the same methods
// as the local store, so the HTTP handlers stay as they are; the methods that would hand out a saved secret answer ErrKeeperHeld or
// leave the secret out, and the checks that need the secret run inside the keeper (TestSaved, HealthSaved, LaneSaved).

func storeCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), storeTimeout+5*time.Second)
}

/* ---------------------------------------------------------------- SQL */

// SQLStore is the saved SQL Server list held by the keeper.
type SQLStore struct{ c *Client }

// NewSQLStore uses c.
func NewSQLStore(c *Client) *SQLStore { return &SQLStore{c: c} }

func (s *SQLStore) List() ([]sqlconn.Recent, error) {
	ctx, cancel := storeCtx()
	defer cancel()
	var out []sqlconn.Recent
	return out, native(s.c.Call(ctx, OpSQLList, nil, &out), kindSQL)
}

// Describe returns the public fields of one saved login.
func (s *SQLStore) Describe(id string) (sqlconn.Recent, error) {
	ctx, cancel := storeCtx()
	defer cancel()
	var out sqlconn.Recent
	return out, native(s.c.Call(ctx, OpSQLDescribe, IDArgs{ID: id}, &out), kindSQL)
}

// Resolve returns the saved login WITHOUT its password: a Windows sign-in, or a SQL login the person typed a password for, runs in
// Unravis; a stored password stays in the keeper (see Held).
func (s *SQLStore) Resolve(id string) (sqlconn.Input, error) {
	r, err := s.Describe(id)
	if err != nil {
		return sqlconn.Input{}, err
	}
	return sqlconn.Input{Label: r.Label, Server: r.Server, Auth: r.Auth, Username: r.Username, Database: r.Database, Encrypt: r.Encrypt, TrustServerCertificate: r.TrustServerCertificate}, nil
}

func (s *SQLStore) Save(in sqlconn.Input) (sqlconn.Recent, error) {
	ctx, cancel := storeCtx()
	defer cancel()
	var out sqlconn.Recent
	return out, native(s.c.Call(ctx, OpSQLSave, in, &out), kindSQL)
}

func (s *SQLStore) Delete(id string) error {
	ctx, cancel := storeCtx()
	defer cancel()
	return native(s.c.Call(ctx, OpSQLDelete, IDArgs{ID: id}, nil), kindSQL)
}

// Held reports whether the saved login's password is in the keeper (a SQL login with a stored password), so the check must run there.
func (s *SQLStore) Held(id string) (bool, error) {
	r, err := s.Describe(id)
	if err != nil {
		return false, err
	}
	return r.Auth == sqlconn.AuthSQL && r.HasPassword, nil
}

// TestSaved connects with a saved login inside the keeper. listError is set when it connected but could not list the databases.
func (s *SQLStore) TestSaved(ctx context.Context, ref SQLRef) (dbhealth.TestResult, string, error) {
	var out SQLTestResult
	if err := s.c.Call(ctx, OpSQLTest, ref, &out); err != nil {
		return dbhealth.TestResult{}, "", native(err, kindSQL)
	}
	return out.Result, out.ListError, nil
}

// HealthSaved runs the fixed SQL Server checks inside the keeper.
func (s *SQLStore) HealthSaved(ctx context.Context, ref SQLRef, label string) (dbhealth.Report, error) {
	var out dbhealth.Report
	return out, native(s.c.Call(ctx, OpSQLHealth, SQLRunArgs{Ref: ref, Label: label}, &out), kindSQL)
}

// LaneSaved runs the SQL Server lane checks inside the keeper.
func (s *SQLStore) LaneSaved(ctx context.Context, ref SQLRef, label string, req dbhealth.LaneRequest) (dbhealth.Report, error) {
	var out dbhealth.Report
	return out, native(s.c.Call(ctx, OpSQLLane, SQLRunArgs{Ref: ref, Label: label, Lane: &req}, &out), kindSQL)
}

/* -------------------------------------------------------------- Mongo */

// MongoStore is the saved MongoDB list held by the keeper. A password the person typed without "remember" is kept here, in this Unravis
// only, for the session (the keeper keeps no per-session secrets).
type MongoStore struct {
	c       *Client
	mu      sync.Mutex
	session map[string]sessionSecret
	legacy  *history.Service
}

type sessionSecret struct {
	address  history.Recent
	password string
}

// NewMongoStore uses c.
func NewMongoStore(c *Client) *MongoStore {
	return &MongoStore{c: c, session: map[string]sessionSecret{}}
}

// WithLegacy is the TEMPORARY P14b adapter: until the keeper hosts the MongoDB audit source (P14b), adding an audit source from a saved
// link whose password the keeper holds falls back to the person's own old per-user file, while it is still on disk. Remove with P14b.
func (s *MongoStore) WithLegacy(old *history.Service) *MongoStore {
	s.legacy = old
	return s
}

func sameAddress(saved history.Recent, in audit.ConnectionInput) bool {
	return saved.URI == strings.TrimSpace(in.URI) && saved.Username == in.Username && saved.AuthSource == in.AuthSource && saved.TLSMode == in.TLSMode && saved.CACertificate == in.CACertificate
}

func (s *MongoStore) List() ([]history.Recent, error) {
	ctx, cancel := storeCtx()
	defer cancel()
	var out []history.Recent
	return out, native(s.c.Call(ctx, OpMongoList, nil, &out), kindMongo)
}

// Describe returns the public fields of one saved link.
func (s *MongoStore) Describe(id string) (history.Recent, error) {
	ctx, cancel := storeCtx()
	defer cancel()
	var out history.Recent
	return out, native(s.c.Call(ctx, OpMongoDescribe, IDArgs{ID: id}, &out), kindMongo)
}

// Remember saves a link in the keeper. The keeper resolves a saved id itself and validates the settings, so the handler sends the
// link as typed (RememberChecked).
func (s *MongoStore) Remember(label string, in audit.ConnectionInput, collection string, remember bool) (history.Recent, error) {
	ctx, cancel := storeCtx()
	defer cancel()
	return s.RememberChecked(ctx, label, in, collection, remember)
}

// RememberChecked sends the link to the keeper, which resolves a saved id, validates the settings and saves it.
func (s *MongoStore) RememberChecked(ctx context.Context, label string, in audit.ConnectionInput, collection string, remember bool) (history.Recent, error) {
	var out history.Recent
	err := s.c.Call(ctx, OpMongoRemember, MongoRememberArgs{Label: label, Connection: in, Collection: collection, RememberPassword: remember}, &out)
	if err != nil {
		return history.Recent{}, native(err, kindMongo)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !remember && in.Password != "" {
		s.session[out.ID] = sessionSecret{address: out, password: in.Password}
	} else if remember {
		delete(s.session, out.ID)
	}
	return out, nil
}

func (s *MongoStore) Delete(id string) error {
	ctx, cancel := storeCtx()
	defer cancel()
	err := native(s.c.Call(ctx, OpMongoDelete, IDArgs{ID: id}, nil), kindMongo)
	s.mu.Lock()
	delete(s.session, id)
	s.mu.Unlock()
	return err
}

func (s *MongoStore) sessionPassword(rec history.Recent) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.session[rec.ID]; ok && c.address.URI == rec.URI && c.address.Username == rec.Username && c.address.AuthSource == rec.AuthSource && c.address.TLSMode == rec.TLSMode && c.address.CACertificate == rec.CACertificate {
		return c.password
	}
	return ""
}

// Resolve fills a saved link for use in Unravis: a typed password, or one typed earlier this session, or none when the link has no
// stored credentials. A stored password stays in the keeper: Resolve answers ErrKeeperHeld and the caller runs the work in the keeper.
func (s *MongoStore) Resolve(in audit.ConnectionInput) (audit.ConnectionInput, error) {
	if in.SavedConnectionID == "" {
		return in, nil
	}
	rec, err := s.Describe(in.SavedConnectionID)
	if err != nil {
		return audit.ConnectionInput{}, err
	}
	if !sameAddress(rec, in) {
		return audit.ConnectionInput{}, audit.ErrInvalidInput
	}
	if in.Password == "" {
		in.Password = s.sessionPassword(rec)
	}
	if in.Password == "" && rec.HasCredentials {
		return audit.ConnectionInput{}, ErrKeeperHeld
	}
	in.SavedConnectionID = ""
	return in, nil
}

// Held reports whether this request needs the keeper's stored password (a saved link with stored credentials and nothing typed).
func (s *MongoStore) Held(in audit.ConnectionInput) (bool, error) {
	if in.SavedConnectionID == "" || in.Password != "" {
		return false, nil
	}
	rec, err := s.Describe(in.SavedConnectionID)
	if err != nil {
		return false, err
	}
	if !sameAddress(rec, in) {
		return false, nil // Resolve answers the mismatch
	}
	return rec.HasCredentials && s.sessionPassword(rec) == "", nil
}

// ResolveForSource is the TEMPORARY P14b adapter for adding an audit source: Resolve, and when the keeper holds the password, the old
// per-user file while it exists. Remove with P14b, when the keeper opens audit sources itself.
func (s *MongoStore) ResolveForSource(in audit.ConnectionInput) (audit.ConnectionInput, error) {
	out, err := s.Resolve(in)
	if !errors.Is(err, ErrKeeperHeld) || s.legacy == nil {
		return out, err
	}
	old, lerr := s.legacy.Resolve(in)
	if lerr != nil || old.Password == "" {
		return audit.ConnectionInput{}, ErrKeeperHeld
	}
	return old, nil
}

// TestSaved tests a saved link inside the keeper.
func (s *MongoStore) TestSaved(ctx context.Context, in audit.ConnectionInput) (audit.ConnectionResult, error) {
	var out audit.ConnectionResult
	return out, native(s.c.Call(ctx, OpMongoTest, MongoConnArgs{Connection: in}, &out), kindMongo)
}

// HealthSaved runs the fixed MongoDB checks inside the keeper.
func (s *MongoStore) HealthSaved(ctx context.Context, in audit.ConnectionInput, database, collection string) (dbhealth.Report, error) {
	var out dbhealth.Report
	return out, native(s.c.Call(ctx, OpMongoHealth, MongoRunArgs{Connection: in, Database: database, Collection: collection}, &out), kindMongo)
}

// LaneSaved runs the MongoDB lane checks inside the keeper.
func (s *MongoStore) LaneSaved(ctx context.Context, in audit.ConnectionInput, label string, req dbhealth.LaneRequest) (dbhealth.Report, error) {
	var out dbhealth.Report
	return out, native(s.c.Call(ctx, OpMongoLane, MongoRunArgs{Connection: in, Label: label, Lane: &req}, &out), kindMongo)
}

/* ----------------------------------------------------------- Blackbox */

// BBStore is the saved Blackbox list held by the keeper. Key never answers: reads that need the key go through KeyedSource.
type BBStore struct{ c *Client }

// NewBBStore uses c.
func NewBBStore(c *Client) *BBStore { return &BBStore{c: c} }

func (s *BBStore) List() ([]bbsaved.Entry, error) {
	ctx, cancel := storeCtx()
	defer cancel()
	var out []bbsaved.Entry
	return out, native(s.c.Call(ctx, OpBBList, nil, &out), kindBB)
}

func (s *BBStore) Get(id string) (bbsaved.Entry, error) {
	ctx, cancel := storeCtx()
	defer cancel()
	var out bbsaved.Entry
	return out, native(s.c.Call(ctx, OpBBGet, IDArgs{ID: id}, &out), kindBB)
}

func (s *BBStore) Find(canon, host, name string) (bbsaved.Entry, bool) {
	ctx, cancel := storeCtx()
	defer cancel()
	var out BBFindResult
	if err := s.c.Call(ctx, OpBBFind, BBFindArgs{Canon: canon, Host: host, Name: name}, &out); err != nil {
		return bbsaved.Entry{}, false
	}
	return out.Entry, out.Found
}

func (s *BBStore) Add(in bbsaved.AddInput) (bbsaved.Entry, error) {
	ctx, cancel := storeCtx()
	defer cancel()
	var out bbsaved.Entry
	err := s.c.Call(ctx, OpBBAdd, BBAddArgs{Name: in.Name, Location: in.Location, Key: in.Key, ThisPC: in.ThisPC, RecorderHost: in.RecorderHost, RecorderName: in.RecorderName}, &out)
	var we *WireError
	if errors.As(err, &we) && we.Code == CodeDuplicate {
		_ = json.Unmarshal(we.Detail, &out)
	}
	return out, native(err, kindBB)
}

func (s *BBStore) SetThisPC(id string, thisPC bool) error {
	ctx, cancel := storeCtx()
	defer cancel()
	return native(s.c.Call(ctx, OpBBSetThisPC, BBSetArgs{ID: id, ThisPC: thisPC}, nil), kindBB)
}

func (s *BBStore) SetKey(id, key string) error {
	ctx, cancel := storeCtx()
	defer cancel()
	return native(s.c.Call(ctx, OpBBSetKey, BBSetArgs{ID: id, Key: key}, nil), kindBB)
}

func (s *BBStore) SetRecorder(id, host, name string) error {
	ctx, cancel := storeCtx()
	defer cancel()
	return native(s.c.Call(ctx, OpBBSetRecorder, BBSetArgs{ID: id, Host: host, Name: name}, nil), kindBB)
}

func (s *BBStore) Remove(id string) error {
	ctx, cancel := storeCtx()
	defer cancel()
	return native(s.c.Call(ctx, OpBBRemove, IDArgs{ID: id}, nil), kindBB)
}

// Key never gives out a read key: the keeper holds it.
func (s *BBStore) Key(string) (string, error) { return "", ErrKeeperHeld }

// KeyedSource reads a saved Blackbox reached by URL through the keeper, which adds the read key. The reader is the ordinary HTTP
// reader; only its transport is the keeper.
func (s *BBStore) KeyedSource(id string, loc blackbox.Location) blackbox.Source {
	rt := &bbTransport{c: s.c, id: id, base: loc.BaseURL()}
	return keyedSource{inner: blackbox.NewHTTPSource(loc, "", &http.Client{Transport: rt, Timeout: bbFileTimeout})}
}

type bbTransport struct {
	c    *Client
	id   string
	base string
}

// keeperNetError is a failure of the keeper to reach the Blackbox, shaped so the HTTP reader words it as it would its own.
type keeperNetError struct {
	timeout bool
	cause   error
}

func (e *keeperNetError) Error() string   { return "the Blackbox could not be reached from the key keeper" }
func (e *keeperNetError) Timeout() bool   { return e.timeout }
func (e *keeperNetError) Temporary() bool { return false }
func (e *keeperNetError) Unwrap() error   { return e.cause }

var _ net.Error = (*keeperNetError)(nil)

func (t *bbTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet {
		return nil, &WireError{Code: CodeRefused, Message: "The key keeper only reads a Blackbox."}
	}
	path := strings.TrimPrefix(req.URL.String(), t.base)
	var head BBHTTPResult
	body, err := t.c.Open(req.Context(), OpBBHTTP, BBHTTPArgs{ID: t.id, Path: path}, &head)
	if err != nil {
		var we *WireError
		if errors.As(err, &we) && we.Code == CodeBBRead {
			var d struct {
				Timeout bool `json:"timeout"`
			}
			_ = json.Unmarshal(we.Detail, &d)
			return nil, &keeperNetError{timeout: d.Timeout, cause: we}
		}
		return nil, err
	}
	if body == nil {
		body = http.NoBody
	}
	h := http.Header{}
	if head.Time != "" {
		h.Set(blackbox.TimeHeader, head.Time)
	}
	if head.ContentType != "" {
		h.Set("Content-Type", head.ContentType)
	}
	return &http.Response{StatusCode: head.Status, Status: http.StatusText(head.Status), Header: h, Body: body, Request: req, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, ContentLength: -1}, nil
}

// keyedSource rewords the failures that are the keeper's own (not running, refused, rate limited), which the HTTP reader would
// otherwise show as "nothing answered at that address".
type keyedSource struct{ inner blackbox.Source }

func keeperReadErr(err error) error {
	if err == nil {
		return nil
	}
	var re *blackbox.ReadError
	if !errors.As(err, &re) {
		return err
	}
	if errors.Is(err, ErrNotRunning) {
		return &blackbox.ReadError{Kind: blackbox.ReadUnreachable, Message: "The Unravis Key Keeper service is not running, so the saved read key cannot be used. Start the service, then look again.", Cause: ErrNotRunning}
	}
	var ne *keeperNetError
	if errors.As(err, &ne) {
		return err
	}
	var we *WireError
	if errors.As(err, &we) {
		kind := blackbox.ReadUnreachable
		if we.Code == CodeRateLimited {
			kind = blackbox.ReadThrottled
		}
		return &blackbox.ReadError{Kind: kind, Message: we.Message, Cause: we}
	}
	return err
}

func (k keyedSource) Manifest(ctx context.Context) (blackbox.ManifestRead, error) {
	m, err := k.inner.Manifest(ctx)
	return m, keeperReadErr(err)
}

func (k keyedSource) Status(ctx context.Context) (*blackbox.StatusDoc, error) {
	d, err := k.inner.Status(ctx)
	return d, keeperReadErr(err)
}

func (k keyedSource) OpenFile(ctx context.Context, name string) (io.ReadCloser, error) {
	rc, err := k.inner.OpenFile(ctx, name)
	return rc, keeperReadErr(err)
}
