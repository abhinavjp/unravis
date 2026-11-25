package clisrv

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clicore"
)

// The case reads (spec 05 D10): `cases.list` and `cases.show` read the same routes the app's window reads, through InternalGET, and then
// keep only the fields the spec allows. The projection is an allow-list of struct fields: a field the dashboard adds later does not
// reach the command line until it is named here. Never shown: the key ending, disk paths, request id, ledger text, evidence, import
// content, the mark, the detail of a history line.

// InternalGET runs one read on the dashboard handler in this process and returns the status and the body (at most MaxResponseBytes).
// It is the only way the cases operations reach the handler, and it has its own allow-list of paths.
type InternalGET func(ctx context.Context, path string) (status int, body []byte, err error)

var (
	casesListPath = regexp.MustCompile(`^/api/cases(\?archived=1&offset=[0-9]{1,9})?$`)
	casesShowPath = regexp.MustCompile(`^/api/cases/[a-z2-7]{26}$`)
)

// NewInternalGET builds an InternalGET over the dashboard handler. token is the dashboard's own session token, which never leaves this
// process. Only GET of the two case routes is allowed (TestInternalGETAllowlist); every other path is refused before the handler runs.
func NewInternalGET(h http.Handler, token string) InternalGET {
	return func(ctx context.Context, path string) (status int, body []byte, err error) {
		if !casesListPath.MatchString(path) && !casesShowPath.MatchString(path) {
			return 0, nil, errNotAllowed
		}
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		defer func() {
			if p := recover(); p != nil { // a handler that panics must not take the owner down
				status, body, err = 0, nil, errHandler
			}
		}()
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1"+path, nil)
		if rerr != nil {
			return 0, nil, errNotAllowed
		}
		req.Header.Set("X-Audit-Token", token)
		// The dashboard counts a client of this name as "in use", so an update waits for a command that is running.
		req.Header.Set("User-Agent", "unravis-mcp")
		req.RemoteAddr = "127.0.0.1:0"
		req.RequestURI = req.URL.RequestURI()
		rec := &capture{header: http.Header{}, status: http.StatusOK}
		h.ServeHTTP(rec, req)
		if err := ctx.Err(); err != nil {
			return 0, nil, err
		}
		if rec.over {
			return 0, nil, errTooLarge
		}
		return rec.status, rec.body.Bytes(), nil
	}
}

type internalError string

func (e internalError) Error() string { return string(e) }

const (
	errNotAllowed internalError = "path not allowed"
	errHandler    internalError = "handler failed"
	errTooLarge   internalError = "answer too large"
)

// capture records an answer, keeping at most MaxResponseBytes.
type capture struct {
	header http.Header
	status int
	wrote  bool
	body   bytes.Buffer
	over   bool
}

func (c *capture) Header() http.Header { return c.header }
func (c *capture) WriteHeader(code int) {
	if !c.wrote {
		c.status, c.wrote = code, true
	}
}
func (c *capture) Write(p []byte) (int, error) {
	c.WriteHeader(http.StatusOK)
	if c.body.Len()+len(p) > MaxResponseBytes {
		c.over = true
		return len(p), nil
	}
	c.body.Write(p)
	return len(p), nil
}

// The projection. Field names are the dashboard's (caseDTO, caseViewDTO); the output names are the spec's.
type caseRow struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Created   *time.Time `json:"created"` // null for a row the store shows without a date (a locked or untrusted case)
	Closed    *time.Time `json:"closed"`
	Bytes     int64      `json:"bytes"`
	Imports   int        `json:"imports"`
	OwnKey    bool       `json:"ownKey"`
	KeyLocked bool       `json:"keyLocked"`
	Archived  bool       `json:"archived"`
	Missing   bool       `json:"missing"`
	Untrusted bool       `json:"untrusted"`
}

type caseWire struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Created   time.Time  `json:"created"`
	Closed    *time.Time `json:"closed"`
	Bytes     int64      `json:"bytes"`
	Imports   int        `json:"imports"`
	OwnKey    bool       `json:"ownKey"`
	KeyLocked bool       `json:"keyLocked"`
	Missing   bool       `json:"missing"`
	Untrusted bool       `json:"untrusted"`
	Archive   *struct {
		Active bool `json:"active"`
	} `json:"archive"`
}

func (w caseWire) row() caseRow {
	return caseRow{ID: w.ID, Title: w.Title, Created: utc(&w.Created), Closed: utc(w.Closed), Bytes: w.Bytes, Imports: w.Imports, OwnKey: w.OwnKey,
		KeyLocked: w.KeyLocked, Archived: w.Archive != nil && w.Archive.Active, Missing: w.Missing, Untrusted: w.Untrusted}
}

func utc(t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// history is one saved move of a case: when and what, no detail.
type history struct {
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`
}

type caseShow struct {
	caseRow
	Envs    int       `json:"envs"`
	Sources int       `json:"sources"`
	History []history `json:"history"`
}

// erasedWire is what the dashboard sends for an erase notice; erasedCase is what the command line shows: the id and the time, and
// nothing else. A zero time is null, as for the case rows.
type erasedWire struct {
	ID string    `json:"id"`
	At time.Time `json:"at"`
}

type erasedCase struct {
	ID string     `json:"id"`
	At *time.Time `json:"at"`
}

// busyReasons are the reasons of a 409 that pass by themselves: the case is being erased, freed, archived or imported into, or is held by
// another call. The reason is compared with this closed list and never copied.
var busyReasons = map[string]bool{"case-erasing": true, "case-freeing": true, "case-in-use": true, "archive-running": true, "case-import-running": true, "cancel-archive-first": true}

// failFromStatus maps a failing answer of the dashboard to a command line error. The text is fixed: the body of the dashboard is never
// copied, so no title, path or key text can leave through an error. Only the "reason" of a 409 is read, against a closed list.
func failFromStatus(status int, body []byte) *clicore.Error {
	if status == http.StatusConflict {
		var f struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(body, &f)
		if busyReasons[f.Reason] {
			e := clicore.NewErrorAfterWork(clicore.CodeOwnerBusy, "The case is busy (being imported into, archived, freed or erased). Try again in a moment.", "")
			e.Retryable = true
			return e
		}
		return clicore.NewErrorAfterWork(clicore.CodeEndpointUnavailable, "This case cannot be read now. It needs attention in the app.", "Open the case in Unravis.")
	}
	code, bad := clicore.FromHTTPStatus(status)
	if !bad {
		return clicore.NewError(clicore.CodeInternal, "The case store gave an answer this command line does not understand.", "")
	}
	switch code {
	case clicore.CodeCaseLocked:
		return clicore.NewErrorAfterWork(code, "Unravis is locked. Unlock it in the app.", "Open Unravis and unlock the cases or the case key.")
	case clicore.CodeNotFound:
		return clicore.NewErrorAfterWork(code, "There is no case with that id.", "Run 'unravis cases list' to see the ids.")
	case clicore.CodeEndpointUnavailable:
		return clicore.NewErrorAfterWork(code, "The case store is not available in this app.", "")
	case clicore.CodeTimeout:
		return clicore.NewErrorAfterWork(code, "The case store did not answer in time.", "")
	case clicore.CodeInvalidArgument:
		return clicore.NewErrorAfterWork(code, "The case store refused the request.", "")
	case clicore.CodePermissionDenied:
		return clicore.NewErrorAfterWork(code, "The case store refused the request.", "Restart Unravis.")
	case clicore.CodeOwnerBusy:
		e := clicore.NewErrorAfterWork(code, "The case store is busy. Try again in a moment.", "")
		e.Retryable = true
		return e
	}
	return clicore.NewErrorAfterWork(clicore.CodeInternal, "The case store failed.", "")
}

func (s *server) get(ctx context.Context, path string) ([]byte, *clicore.Error) {
	if s.d.Internal == nil {
		return nil, clicore.NewError(clicore.CodeEndpointUnavailable, "The case store is not available in this app.", "")
	}
	status, body, err := s.d.Internal(ctx, path)
	if err != nil {
		switch {
		case ctx.Err() != nil:
			return nil, ctxError(ctx)
		case err == errTooLarge:
			return nil, clicore.NewErrorAfterWork(clicore.CodeResultTooLarge, "The result is larger than 16 MiB.", "")
		}
		return nil, clicore.NewErrorAfterWork(clicore.CodeInternal, "The case store failed.", "")
	}
	if status != http.StatusOK {
		return nil, failFromStatus(status, body)
	}
	return body, nil
}

func ctxError(ctx context.Context) *clicore.Error {
	if ctx.Err() == context.DeadlineExceeded {
		return clicore.NewErrorAfterWork(clicore.CodeTimeout, "The command took too long.", "")
	}
	return clicore.NewErrorAfterWork(clicore.CodeCanceled, "Canceled.", "")
}

func (s *server) casesList(ctx context.Context, args json.RawMessage) (any, *clicore.Error) {
	var in struct {
		Archived bool `json:"archived"`
		Offset   *int `json:"offset"`
	}
	if e := decodeStrict(args, &in); e != nil {
		return nil, e
	}
	path := "/api/cases"
	if in.Offset != nil && !in.Archived {
		return nil, clicore.NewError(clicore.CodeInvalidArgument, "offset goes with archived.", "")
	}
	if in.Archived {
		off := 0
		if in.Offset != nil {
			off = *in.Offset
		}
		if off < 0 || off > 999999999 {
			return nil, clicore.NewError(clicore.CodeInvalidArgument, "offset must be between 0 and 999999999.", "")
		}
		path += "?archived=1&offset=" + strconv.Itoa(off)
	}
	body, e := s.get(ctx, path)
	if e != nil {
		return nil, e
	}
	var wire struct {
		Cases         []caseWire   `json:"cases"`
		ArchivedTotal int          `json:"archivedTotal"`
		Offset        int          `json:"offset"`
		PageSize      int          `json:"pageSize"`
		Erased        []erasedWire `json:"erased"`
		OldCases      int          `json:"oldCases"`
	}
	if json.Unmarshal(body, &wire) != nil {
		return nil, clicore.NewErrorAfterWork(clicore.CodeInternal, "The case store gave an answer this command line does not understand.", "")
	}
	rows := make([]caseRow, len(wire.Cases))
	for i, c := range wire.Cases {
		rows[i] = c.row()
	}
	// erased.json is a plain file that reads as no lines when it is damaged, but a hand-edited line can still carry any text as its id:
	// keep a line only when its id is a case id (the web window does the same).
	erased := make([]erasedCase, 0, len(wire.Erased))
	for _, e := range wire.Erased {
		if _, err := casestore.ParseID(e.ID); err != nil {
			continue
		}
		erased = append(erased, erasedCase{ID: e.ID, At: utc(&e.At)})
	}
	return map[string]any{"cases": rows, "archivedTotal": wire.ArchivedTotal, "offset": wire.Offset, "pageSize": wire.PageSize,
		"erased": erased, "oldCases": wire.OldCases}, nil
}

func (s *server) casesShow(ctx context.Context, args json.RawMessage) (any, *clicore.Error) {
	var in struct {
		ID string `json:"id"`
	}
	if e := decodeStrict(args, &in); e != nil {
		return nil, e
	}
	if _, err := casestore.ParseID(in.ID); err != nil {
		return nil, clicore.NewError(clicore.CodeInvalidArgument, "id is not a case id (26 characters).", "Run 'unravis cases list' to see the ids.")
	}
	body, e := s.get(ctx, "/api/cases/"+in.ID)
	if e != nil {
		return nil, e
	}
	var wire struct {
		caseWire
		Envs    int `json:"envs"`
		Sources int `json:"sources"`
		History []struct {
			At   time.Time `json:"at"`
			Kind string    `json:"kind"`
		} `json:"history"`
	}
	if json.Unmarshal(body, &wire) != nil {
		return nil, clicore.NewErrorAfterWork(clicore.CodeInternal, "The case store gave an answer this command line does not understand.", "")
	}
	out := caseShow{caseRow: wire.row(), Envs: wire.Envs, Sources: wire.Sources, History: make([]history, len(wire.History))}
	for i, h := range wire.History {
		out.History[i] = history{Kind: h.Kind, At: h.At.UTC()}
	}
	return out, nil
}
