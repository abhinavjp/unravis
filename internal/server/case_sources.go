package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/casestore"
)

// Environment and source tags of one case (TSK-07) and the per-database
// classification of a Mongo connection (TSK-08). The tags are labels the owner
// types; they are never connection details. Nothing here reads, deletes or
// changes imported events.

const (
	// mongoDatabaseKind is the SourceTag kind of one classified Mongo database.
	// Its label is the database name and nothing else.
	mongoDatabaseKind = "mongo-database"

	// mongoTestTTL is how long a connection test's database list stays usable
	// for classification. mongoTestKeep is how many recent test results are kept.
	mongoTestTTL  = 30 * time.Minute
	mongoTestKeep = 8

	tagClosedText     = "The case is closed. Its environments and source tags cannot be changed."
	tagBadText        = "A name or label is empty, too long, or holds characters that are not allowed."
	tagCredentialText = "A name or label looks like a connection string or a password. Type a plain label."
	tagEnvCapText     = "A case holds at most 32 environments."
	tagSourceCapText  = "A case holds at most 200 sources."
	tagNotTestedText  = "That database was not in the last connection test. Test the connection and choose a database from the list it returned."
	tagClassText      = "Choose whether the database holds audit logs, other data, or is not used."
	tagRemovedText    = "The source tag was removed. Events already imported from it were not deleted."
)

// credentialLike matches text that would carry a credential: a URI scheme of a
// database connection, user information in a URL, or a password assignment.
var credentialLike = regexp.MustCompile(`(?i)(mongodb(\+srv)?://|://[^/\s]*@|\b(password|passwd|pwd)\s*[=:])`)

/* ---------------------------------------------------------------- DTOs */

// sourceTagDTO is one source tag as the page sees it. importId is 0 when the
// tag is not tied to one import. Fields hold labels only.
type sourceTagDTO struct {
	ID             int64     `json:"id"`
	ImportID       int64     `json:"importId"`
	Kind           string    `json:"kind"`
	Label          string    `json:"label"`
	Env            string    `json:"env"`
	Portal         string    `json:"portal"`
	API            string    `json:"api"`
	Classification string    `json:"classification"`
	Created        time.Time `json:"created"`
}

func toSourceTagDTO(s casestore.SourceTag) sourceTagDTO {
	return sourceTagDTO{
		ID: s.ID, ImportID: s.ImportID, Kind: s.Kind, Label: s.Label, Env: s.Env, Portal: s.Portal, API: s.API,
		Classification: s.Classification, Created: s.Created.UTC(),
	}
}

// sourceTagBody is the request body of POST /api/cases/{id}/sources.
type sourceTagBody struct {
	ImportID       int64  `json:"importId"`
	Kind           string `json:"kind"`
	Label          string `json:"label"`
	Env            string `json:"env"`
	Portal         string `json:"portal"`
	API            string `json:"api"`
	Classification string `json:"classification"`
}

type environmentsDTO struct {
	Environments []string `json:"environments"`
}

type sourceTagsDTO struct {
	Sources []sourceTagDTO `json:"sources"`
}

// caseViewDTO is GET /api/cases/{id}: the list view's fields plus the counts of
// an opened case. (The list itself has no counts: it reads the manifest only.)
type caseViewDTO struct {
	caseDTO
	Envs    int `json:"envs"`
	Sources int `json:"sources"`
}

/* ------------------------------------------------------- recent tests */

// mongoTests remembers the database names the last few successful connection
// tests returned, for a short time. It holds names only: no URI, user name or
// password, and nothing that identifies the cluster. A classification is
// accepted only for a name in it, so arbitrary text never becomes a label.
type mongoTests struct {
	mu      sync.Mutex
	entries []mongoTestEntry
	now     func() time.Time // test hook
}

type mongoTestEntry struct {
	at    time.Time
	names map[string]struct{}
}

func (m *mongoTests) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// prune drops expired entries. The caller holds m.mu.
func (m *mongoTests) prune(now time.Time) {
	kept := m.entries[:0]
	for _, e := range m.entries {
		if now.Sub(e.at) < mongoTestTTL {
			kept = append(kept, e)
		}
	}
	m.entries = kept
}

// record keeps the names of one successful test.
func (m *mongoTests) record(names []string) {
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	now := m.clock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prune(now)
	m.entries = append(m.entries, mongoTestEntry{at: now, names: set})
	if over := len(m.entries) - mongoTestKeep; over > 0 {
		m.entries = append([]mongoTestEntry(nil), m.entries[over:]...)
	}
}

// has reports whether a recent test returned the database name.
func (m *mongoTests) has(name string) bool {
	now := m.clock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prune(now)
	for _, e := range m.entries {
		if _, ok := e.names[name]; ok {
			return true
		}
	}
	return false
}

/* ------------------------------------------------------------ plumbing */

// tagMu serialises the read-then-write replace of a Mongo classification.
var tagMu sync.Mutex

// tagCaseOf opens the case for a tag request. A malformed id answers the same
// 404 as a case that does not exist, so the route does not tell the two apart.
func (h *handler) tagCaseOf(w http.ResponseWriter, r *http.Request, raw string) (casestore.Case, casestore.Tagger, bool) {
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
	// openCase wraps the handle to hold the read share, and the wrapper only
	// shows the Case methods, so the assertion is made on the handle inside.
	var inner casestore.Case = c
	if sc, wrapped := c.(*sharedCase); wrapped {
		inner = sc.Case
	}
	t, ok := inner.(casestore.Tagger)
	if !ok {
		c.Close()
		writeCaseFailure(w, http.StatusServiceUnavailable, "tags-unavailable", "This case store cannot hold environments or source tags.")
		return nil, nil, false
	}
	return c, t, true
}

// writeTagError maps a Tagger failure. cap is the text for a full list.
func writeTagError(w http.ResponseWriter, err error, capText string) {
	switch {
	case errors.Is(err, casestore.ErrBadTag):
		writeCaseFailure(w, http.StatusUnprocessableEntity, "bad-tag", tagBadText)
	case errors.Is(err, casestore.ErrCapExceeded):
		writeCaseFailure(w, http.StatusUnprocessableEntity, "tag-cap-exceeded", capText)
	case errors.Is(err, casestore.ErrCaseClosed):
		writeCaseFailure(w, http.StatusConflict, "case-closed", tagClosedText)
	case errors.Is(err, casestore.ErrHandleClosed), errors.Is(err, casestore.ErrNotFound):
		writeCaseFailure(w, http.StatusNotFound, "case-not-found", "The case was not found.")
	default:
		writeStoreError(w, err)
	}
}

func looksLikeCredential(texts ...string) bool {
	for _, t := range texts {
		if credentialLike.MatchString(t) {
			return true
		}
	}
	return false
}

/* -------------------------------------------------------- environments */

func (h *handler) getEnvironments(w http.ResponseWriter, r *http.Request, raw string) {
	c, t, ok := h.tagCaseOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	envs, err := t.Envs(ctx)
	if err != nil {
		writeTagError(w, err, tagEnvCapText)
		return
	}
	writeJSON(w, http.StatusOK, environmentsDTO{Environments: nonNil(envs)}, maxResponseBytes)
}

func (h *handler) putEnvironments(w http.ResponseWriter, r *http.Request, raw string) {
	c, t, ok := h.tagCaseOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	var body environmentsDTO
	if !decodeJSON(w, r, &body) {
		return
	}
	if looksLikeCredential(body.Environments...) {
		writeCaseFailure(w, http.StatusUnprocessableEntity, "bad-tag", tagCredentialText)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	if err := t.SetEnvs(ctx, body.Environments); err != nil {
		writeTagError(w, err, tagEnvCapText)
		return
	}
	// Answer with what was stored: trimmed, de-duplicated, in order.
	envs, err := t.Envs(ctx)
	if err != nil {
		writeTagError(w, err, tagEnvCapText)
		return
	}
	writeJSON(w, http.StatusOK, environmentsDTO{Environments: nonNil(envs)}, maxResponseBytes)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

/* ------------------------------------------------------------- sources */

func tagDTOs(list []casestore.SourceTag, keep func(casestore.SourceTag) bool) []sourceTagDTO {
	out := make([]sourceTagDTO, 0, len(list))
	for _, s := range list {
		if keep == nil || keep(s) {
			out = append(out, toSourceTagDTO(s))
		}
	}
	return out
}

func (h *handler) getSources(w http.ResponseWriter, r *http.Request, raw string) {
	c, t, ok := h.tagCaseOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	list, err := t.Sources(ctx)
	if err != nil {
		writeTagError(w, err, tagSourceCapText)
		return
	}
	writeJSON(w, http.StatusOK, sourceTagsDTO{Sources: tagDTOs(list, nil)}, maxResponseBytes)
}

func (h *handler) postSource(w http.ResponseWriter, r *http.Request, raw string) {
	c, t, ok := h.tagCaseOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	var body sourceTagBody
	if !decodeJSON(w, r, &body) {
		return
	}
	if looksLikeCredential(body.Kind, body.Label, body.Env, body.Portal, body.API) {
		writeCaseFailure(w, http.StatusUnprocessableEntity, "bad-tag", tagCredentialText)
		return
	}
	tag := casestore.SourceTag{
		ImportID: body.ImportID, Kind: body.Kind, Label: body.Label, Env: body.Env, Portal: body.Portal, API: body.API,
		Classification: body.Classification,
	}
	if strings.TrimSpace(tag.Kind) == mongoDatabaseKind && !mongoClassOK(tag.Classification) {
		writeCaseFailure(w, http.StatusUnprocessableEntity, "bad-classification", tagClassText)
		return
	}
	// The store trims and checks the fields; cleaned here too so the Mongo rules
	// below compare the same text the store will keep.
	clean, err := casestore.CleanSource(tag)
	if err != nil {
		writeTagError(w, err, tagSourceCapText)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	if clean.Kind == mongoDatabaseKind {
		h.postMongoDatabase(ctx, w, t, clean)
		return
	}
	added, err := t.AddSource(ctx, clean)
	if err != nil {
		writeTagError(w, err, tagSourceCapText)
		return
	}
	writeJSON(w, http.StatusCreated, toSourceTagDTO(added), maxResponseBytes)
}

func mongoClassOK(c string) bool {
	return c == casestore.ClassAudit || c == casestore.ClassOther || c == casestore.ClassUnused
}

// postMongoDatabase stores the owner's choice for one database. The label must
// be a database name a recent connection test returned, and the choice must be
// one of the three. A repeat choice for the same database (same environment)
// replaces the earlier one.
func (h *handler) postMongoDatabase(ctx context.Context, w http.ResponseWriter, t casestore.Tagger, tag casestore.SourceTag) {
	if !mongoClassOK(tag.Classification) {
		writeCaseFailure(w, http.StatusUnprocessableEntity, "bad-classification", tagClassText)
		return
	}
	if tag.Label == "" || !h.mongoTests.has(tag.Label) {
		writeCaseFailure(w, http.StatusUnprocessableEntity, "database-not-tested", tagNotTestedText)
		return
	}
	tagMu.Lock()
	defer tagMu.Unlock()
	existing, err := t.Sources(ctx)
	if err != nil {
		writeTagError(w, err, tagSourceCapText)
		return
	}
	var older []int64
	for _, s := range existing {
		if s.Kind == mongoDatabaseKind && s.Label == tag.Label && strings.EqualFold(s.Env, tag.Env) {
			older = append(older, s.ID)
		}
	}
	added, err := t.AddSource(ctx, tag)
	if errors.Is(err, casestore.ErrCapExceeded) && len(older) > 0 {
		// The replaced choice frees the slot the new one needs.
		for _, id := range older {
			if rerr := t.RemoveSource(ctx, id); rerr != nil {
				writeTagError(w, rerr, tagSourceCapText)
				return
			}
		}
		older = nil
		added, err = t.AddSource(ctx, tag)
	}
	if err != nil {
		writeTagError(w, err, tagSourceCapText)
		return
	}
	for _, id := range older {
		if err := t.RemoveSource(ctx, id); err != nil {
			writeTagError(w, err, tagSourceCapText)
			return
		}
	}
	writeJSON(w, http.StatusCreated, toSourceTagDTO(added), maxResponseBytes)
}

// caseSourcePath matches /api/cases/{id}/sources/{sourceId} and returns
// "id/sourceId".
func caseSourcePath(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/api/cases/")
	if !ok {
		return "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] != "sources" || parts[2] == "" {
		return "", false
	}
	return parts[0] + "/" + parts[2], true
}

// deleteSource removes one source tag. It never deletes imported events.
func (h *handler) deleteSourceTag(w http.ResponseWriter, r *http.Request, arg string) {
	rawCase, rawID, _ := strings.Cut(arg, "/")
	c, t, ok := h.tagCaseOf(w, r, rawCase)
	if !ok {
		return
	}
	defer c.Close()
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		writeCaseFailure(w, http.StatusNotFound, "source-not-found", "That source tag was not found.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	if err := t.RemoveSource(ctx, id); err != nil {
		writeTagError(w, err, tagSourceCapText)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Removed       bool   `json:"removed"`
		EventsDeleted bool   `json:"eventsDeleted"`
		Message       string `json:"message"`
	}{true, false, tagRemovedText}, 4096)
}

/* ------------------------------------------------------- mongo sources */

// getMongoSources answers GET /api/cases/{id}/mongo-sources: the classified
// Mongo databases of the case. Without a filter it lists the audit databases,
// the ones the wizard opens as analysis sources through the connect flow; with
// ?classification=other or =unused it lists those instead.
func (h *handler) getMongoSources(w http.ResponseWriter, r *http.Request, raw string) {
	c, t, ok := h.tagCaseOf(w, r, raw)
	if !ok {
		return
	}
	defer c.Close()
	want := casestore.ClassAudit
	if v, present := r.URL.Query()["classification"]; present {
		if len(v) != 1 || (v[0] != casestore.ClassAudit && v[0] != casestore.ClassOther && v[0] != casestore.ClassUnused) {
			writeCaseFailure(w, http.StatusBadRequest, "bad-classification", tagClassText)
			return
		}
		want = v[0]
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	list, err := t.Sources(ctx)
	if err != nil {
		writeTagError(w, err, tagSourceCapText)
		return
	}
	out := tagDTOs(list, func(s casestore.SourceTag) bool {
		return s.Kind == mongoDatabaseKind && s.Classification == want
	})
	writeJSON(w, http.StatusOK, sourceTagsDTO{Sources: out}, maxResponseBytes)
}
