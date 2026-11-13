package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/history"
	"api-audit-explorer/internal/keeper"
	"api-audit-explorer/internal/sourcelinks"
)

// Reopening the audit sources a person had open before Unravis was restarted.
//
// Every source added from a saved connection leaves a link (sourcelinks: ids and names only, no password). At start-up, and again
// when Start tracing runs, each link whose saved connection stores its password is opened again, read-only, through the same
// repository calls as the add-source route, so the cap on sources, the duplicate check and the key keeper path behave as they do
// for a person pressing Connect. A saved connection with no stored password is left alone: its lane keeps the Connect button.

const (
	// reconnectEachTimeout bounds one connection; there is no retry inside a pass.
	reconnectEachTimeout = 5 * time.Second
	// reconnectCooldown is how long a link that failed for a Start tracing is left alone, so pressing Start tracing again at once does
	// not dial a dead host again. A link that failed in the start-up pass waits longer: nobody is looking at it yet.
	reconnectCooldown = 20 * time.Second
	startupCooldown   = 2 * time.Minute
	// tracingReconnectBudget is the most Start tracing spends reopening saved databases.
	tracingReconnectBudget = 12 * time.Second
	// StartupReconnectBudget is the most the start-up pass spends.
	StartupReconnectBudget = 60 * time.Second
	// maxPendingLinks bounds the sources added before their connection was saved.
	maxPendingLinks = 16
	maxReasonLength = 200
)

// sourceLinkStore is the sealed links file (sourcelinks.Store).
type sourceLinkStore interface {
	List() ([]sourcelinks.Link, error)
	Add(sourcelinks.Link) error
	Remove(selectionKey, database, collection string) error
	Forget(savedConnectionID string) error
}

// WithSourceLinks turns on reopening saved audit sources. Without it nothing is remembered and nothing is reopened.
func WithSourceLinks(store sourceLinkStore) Option {
	return func(h *handler) {
		h.links = store
		h.reconn.gate = make(chan struct{}, 1)
	}
}

// pendingLink is a source added before its connection was saved: the page adds the source with the typed details, then saves the
// connection. The address (never the password) is kept in memory until a saved connection with the same address turns up.
type pendingLink struct {
	link    sourcelinks.Link
	address audit.ConnectionInput
}

// What a person does next after a failed reopen. The page words each one; it never matches on the reason text.
const (
	actionConnect     = "connect"       // press Connect and type the password again
	actionRetry       = "retry"         // nothing was wrong: there was no time left, start tracing again
	actionStartKeeper = "start-keeper"  // the Unravis Key Keeper service must run
	actionFreeSource  = "remove-source" // too many sources are open
)

type reconnectFail struct {
	reason string
	action string
	until  time.Time // the link is left alone until then
}

type reconnectState struct {
	gate    chan struct{}
	mu      sync.Mutex
	pending []pendingLink
	failed  map[string]reconnectFail
}

// tracingReconnectFail is a saved database that could not be reopened, for the Start tracing answer.
type tracingReconnectFail struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
	// Action is what the person does next: "connect", "retry", "start-keeper" or "remove-source".
	Action string `json:"action"`
}

func linkKey(l sourcelinks.Link) string {
	return l.SavedConnectionID + "\x00" + l.Database + "\x00" + l.Collection
}

// addressOnly drops anything secret from a typed address: the user name and password a connection string can carry before the host.
// A string that does not parse as an address is kept as typed, except that everything before the last "@" of its authority is cut.
func addressOnly(uri string) string {
	uri = strings.TrimSpace(uri)
	scheme := strings.Index(uri, "://")
	if scheme < 0 {
		return uri
	}
	rest := uri[scheme+3:]
	end := len(rest)
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		end = i
	}
	authority := rest[:end]
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		authority = authority[at+1:]
	}
	return uri[:scheme+3] + authority + rest[end:]
}

func sameAddress(rec history.Recent, c audit.ConnectionInput) bool {
	return addressOnly(rec.URI) == addressOnly(c.URI) && rec.Username == c.Username && rec.AuthSource == c.AuthSource && rec.TLSMode == c.TLSMode && rec.CACertificate == c.CACertificate
}

// noteOpenedSource remembers a source that was just added. savedID is the saved connection the request named, if any; conn is the
// address the source was opened with (its password is not kept).
func (h *handler) noteOpenedSource(item audit.Source, savedID string, conn audit.ConnectionInput) {
	if h.links == nil || h.demo || item.Database == "" || item.Collection == "" {
		return
	}
	link := sourcelinks.Link{SavedConnectionID: savedID, Database: item.Database, Collection: item.Collection, Label: item.Label, ConnectionLabel: item.ConnectionLabel, SelectionKey: item.SelectionKey}
	if savedID != "" {
		_ = h.links.Add(link)
		return
	}
	conn.Password, conn.SavedConnectionID, conn.URI = "", "", addressOnly(conn.URI)
	h.reconn.mu.Lock()
	h.reconn.pending = append(h.reconn.pending, pendingLink{link: link, address: conn})
	if len(h.reconn.pending) > maxPendingLinks {
		h.reconn.pending = h.reconn.pending[len(h.reconn.pending)-maxPendingLinks:]
	}
	h.reconn.mu.Unlock()
	h.promotePending()
}

// promotePending links the sources added from typed details to the saved connection with the same address, once there is one.
func (h *handler) promotePending() {
	if h.links == nil || h.history == nil {
		return
	}
	h.reconn.mu.Lock()
	waiting := len(h.reconn.pending)
	h.reconn.mu.Unlock()
	if waiting == 0 {
		return
	}
	recents, err := h.history.List()
	if err != nil {
		return
	}
	h.reconn.mu.Lock()
	defer h.reconn.mu.Unlock()
	kept := h.reconn.pending[:0]
	for _, p := range h.reconn.pending {
		matched := false
		for _, rec := range recents {
			if sameAddress(rec, p.address) {
				p.link.SavedConnectionID = rec.ID
				_ = h.links.Add(p.link)
				matched = true
				break
			}
		}
		if !matched {
			kept = append(kept, p)
		}
	}
	h.reconn.pending = kept
}

// sourceKeyOf finds the stable key, database and collection of an open source by its id. deleteSource calls it before the delete,
// while the source can still be looked up, so the link of a source the person removed can be dropped afterwards.
func (h *handler) sourceKeyOf(ctx context.Context, id string) (key, database, collection string, found bool) {
	if h.links == nil || h.demo {
		return "", "", "", false
	}
	items, err := h.repo.Sources(ctx)
	if err != nil {
		return "", "", "", false
	}
	for _, s := range items {
		if s.ID == id {
			return s.SelectionKey, s.Database, s.Collection, true
		}
	}
	return "", "", "", false
}

// dropLinkOfSource removes the link of an open source before it is deleted, and returns a function that puts it back if the delete
// fails. With no link store, or no such source, it does nothing.
func (h *handler) dropLinkOfSource(ctx context.Context, id string) (restore func()) {
	key, database, collection, found := h.sourceKeyOf(ctx, id)
	if !found {
		return func() {}
	}
	var held []sourcelinks.Link
	if all, err := h.links.List(); err == nil {
		for _, l := range all {
			if (key != "" && l.SelectionKey == key) || (key == "" && l.Database == database && l.Collection == collection) {
				held = append(held, l)
			}
		}
	}
	h.forgetOpenedSource(key, database, collection)
	return func() {
		for _, l := range held {
			_ = h.links.Add(l)
		}
	}
}

// forgetOpenedSource drops the link (and any waiting address) of a source the person removed.
func (h *handler) forgetOpenedSource(key, database, collection string) {
	if h.links == nil {
		return
	}
	_ = h.links.Remove(key, database, collection)
	h.reconn.mu.Lock()
	kept := h.reconn.pending[:0]
	for _, p := range h.reconn.pending {
		if !((key != "" && p.link.SelectionKey == key) || (key == "" && p.link.Database == database && p.link.Collection == collection)) {
			kept = append(kept, p)
		}
	}
	h.reconn.pending = kept
	h.reconn.mu.Unlock()
}

// ReconnectSavedSources reopens the saved audit sources once, within budget. It never returns an error and never panics the caller: a
// source that cannot be reopened stays "not connected" and the page says why. served is the handler New returned. This is the
// start-up pass: it tries every link, and a link that fails is left alone for longer than after a Start tracing.
func ReconnectSavedSources(ctx context.Context, served http.Handler, budget time.Duration) {
	if h, ok := served.(*handler); ok && h != nil {
		h.reconnectPass(ctx, budget, nil, startupCooldown)
	}
}

// reconnectSaved is the Start tracing pass: only the links whose database (or label) is one of only, the saved audit names of the case.
func (h *handler) reconnectSaved(ctx context.Context, budget time.Duration, only []string) []tracingReconnectFail {
	return h.reconnectPass(ctx, budget, only, reconnectCooldown)
}

func wantedLink(l sourcelinks.Link, only []string) bool {
	if only == nil {
		return true
	}
	for _, name := range only {
		name = strings.TrimSpace(name)
		if name != "" && (strings.EqualFold(name, l.Database) || strings.EqualFold(name, l.Label)) {
			return true
		}
	}
	return false
}

// reconnectPass reopens every wanted link that is not open, whose saved connection has a stored password. It returns the links that are
// still not open because opening them failed (or because there was no time). One pass runs at a time; a second caller waits for the
// first up to the budget. A pass cut short by its budget or by the caller leaving records no failure: those links were never tried.
func (h *handler) reconnectPass(ctx context.Context, budget time.Duration, only []string, cooldown time.Duration) []tracingReconnectFail {
	if h.links == nil || h.history == nil || h.demo || h.repo == nil || h.reconn.gate == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	select {
	case h.reconn.gate <- struct{}{}:
		defer func() { <-h.reconn.gate }()
	case <-ctx.Done():
		return h.currentReconnectFailures()
	}
	h.promotePending()
	links, err := h.links.List()
	if err != nil || len(links) == 0 {
		return nil
	}
	items, err := h.repo.Sources(ctx)
	if err != nil {
		return nil
	}
	missing := make([]sourcelinks.Link, 0, len(links))
	for _, l := range links {
		if wantedLink(l, only) && !linkIsOpen(l, items) {
			missing = append(missing, l)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	recents, err := h.history.List()
	if err != nil {
		return h.recordFailures(missing, reconnectWhy(err), cooldown)
	}
	byID := make(map[string]history.Recent, len(recents))
	for _, rec := range recents {
		byID[rec.ID] = rec
	}
	var out []tracingReconnectFail
	opened := false
	forgotten := map[string]bool{}
	for _, l := range missing {
		rec, ok := byID[l.SavedConnectionID]
		if !ok {
			// The saved connection was deleted: nothing can ever reopen this link, so it is dropped.
			if !forgotten[l.SavedConnectionID] {
				forgotten[l.SavedConnectionID] = true
				_ = h.links.Forget(l.SavedConnectionID)
			}
			continue
		}
		if !rec.HasCredentials {
			continue // no stored password: nothing automatic, the lane keeps its Connect button
		}
		key := linkKey(l)
		if prior, again := h.recentFailure(key); again {
			out = append(out, tracingReconnectFail{Name: l.Database, Reason: prior.reason, Action: prior.action})
			continue
		}
		if ctx.Err() != nil {
			out = append(out, outOfTime(l))
			continue
		}
		each, stop := context.WithTimeout(ctx, reconnectEachTimeout)
		err := h.openSavedLink(each, l, rec)
		stop()
		if err == nil || errors.Is(err, audit.ErrDuplicate) {
			h.clearFailure(key)
			opened = true
			continue
		}
		if ctx.Err() != nil {
			// The budget ran out or the caller left while this one was being tried: say so, but do not blame the database or start a cooldown.
			out = append(out, outOfTime(l))
			continue
		}
		why := reconnectWhy(err)
		h.setFailure(key, why, cooldown)
		out = append(out, tracingReconnectFail{Name: l.Database, Reason: why.reason, Action: why.action})
	}
	if opened {
		h.rollups.clear()
	}
	return out
}

func outOfTime(l sourcelinks.Link) tracingReconnectFail {
	return tracingReconnectFail{Name: l.Database, Reason: reconnectOutOfTime, Action: actionRetry}
}

const reconnectOutOfTime = "there was not enough time to try"

func linkIsOpen(l sourcelinks.Link, items []audit.Source) bool {
	for _, s := range items {
		if l.SelectionKey != "" && s.SelectionKey == l.SelectionKey {
			return true
		}
		if l.SelectionKey == "" && s.Database == l.Database && s.Collection == l.Collection {
			return true
		}
	}
	return false
}

// openSavedLink opens one link with the saved connection's stored password. It is addSource without the HTTP request: a password the
// key keeper holds is used in the keeper, any other is read from the saved connections here.
func (h *handler) openSavedLink(ctx context.Context, l sourcelinks.Link, rec history.Recent) error {
	in := audit.SourceInput{
		ConnectionLabel: l.ConnectionLabel, Label: l.Label, URI: rec.URI, Username: rec.Username, AuthSource: rec.AuthSource,
		TLSMode: rec.TLSMode, CACertificate: rec.CACertificate, SavedConnectionID: rec.ID, Database: l.Database, Collection: l.Collection,
	}
	conn := audit.ConnectionInput{URI: rec.URI, Username: rec.Username, AuthSource: rec.AuthSource, TLSMode: rec.TLSMode, CACertificate: rec.CACertificate, SavedConnectionID: rec.ID}
	if ka, isKeeper := h.repo.(keeperAudit); isKeeper {
		if k, ok := h.history.(keeperMongo); ok {
			held, err := k.Held(conn)
			if err != nil {
				return err
			}
			if held {
				in.Password = ""
				_, err := ka.AddHeldSource(ctx, in)
				return err
			}
		}
	}
	resolved, err := h.history.Resolve(conn)
	if err != nil {
		return err
	}
	if resolved.Password == "" {
		return errNoStoredPassword
	}
	in.URI, in.Username, in.Password, in.AuthSource = resolved.URI, resolved.Username, resolved.Password, resolved.AuthSource
	in.TLSMode, in.CACertificate, in.SavedConnectionID = resolved.TLSMode, resolved.CACertificate, ""
	_, err = h.repo.AddSource(ctx, in)
	return err
}

var errNoStoredPassword = errors.New("no stored password")

func (h *handler) recentFailure(key string) (reconnectFail, bool) {
	h.reconn.mu.Lock()
	defer h.reconn.mu.Unlock()
	f, ok := h.reconn.failed[key]
	return f, ok && time.Now().Before(f.until)
}

func (h *handler) setFailure(key string, why reconnectFail, cooldown time.Duration) {
	h.reconn.mu.Lock()
	defer h.reconn.mu.Unlock()
	if h.reconn.failed == nil {
		h.reconn.failed = map[string]reconnectFail{}
	}
	why.until = time.Now().Add(cooldown)
	h.reconn.failed[key] = why
}

func (h *handler) clearFailure(key string) {
	h.reconn.mu.Lock()
	delete(h.reconn.failed, key)
	h.reconn.mu.Unlock()
}

// recordFailures marks every link as failed for the same reason: the saved connections could not be listed at all.
func (h *handler) recordFailures(links []sourcelinks.Link, why reconnectFail, cooldown time.Duration) []tracingReconnectFail {
	out := make([]tracingReconnectFail, 0, len(links))
	for _, l := range links {
		h.setFailure(linkKey(l), why, cooldown)
		out = append(out, tracingReconnectFail{Name: l.Database, Reason: why.reason, Action: why.action})
	}
	return out
}

// currentReconnectFailures is what the pass in progress has found so far.
func (h *handler) currentReconnectFailures() []tracingReconnectFail {
	links, err := h.links.List()
	if err != nil {
		return nil
	}
	var out []tracingReconnectFail
	h.reconn.mu.Lock()
	defer h.reconn.mu.Unlock()
	for _, l := range links {
		if f, ok := h.reconn.failed[linkKey(l)]; ok && time.Now().Before(f.until) {
			out = append(out, tracingReconnectFail{Name: l.Database, Reason: f.reason, Action: f.action})
		}
	}
	return out
}

// reconnectWhy words a failed reopen in plain words (lower case, no stop at the end) and says what to do next. It carries no address,
// user name or password: only the sanitized connection message or a fixed phrase.
func reconnectWhy(err error) reconnectFail {
	var ce *audit.ConnectionError
	var we *keeper.WireError
	switch {
	case errors.As(err, &ce) && ce != nil && strings.TrimSpace(ce.Message) != "":
		return reconnectFail{reason: plainReason(ce.Message), action: actionConnect}
	case errors.Is(err, keeper.ErrNotRunning):
		return reconnectFail{reason: "the Unravis Key Keeper service is not running", action: actionStartKeeper}
	case errors.Is(err, keeper.ErrImpostor):
		return reconnectFail{reason: "the Unravis Key Keeper service could not be verified", action: actionStartKeeper}
	case errors.As(err, &we) && we != nil && we.Code == keeper.CodeBusy:
		return reconnectFail{reason: "the key keeper has too many sources open", action: actionFreeSource}
	case errors.Is(err, history.ErrNotFound):
		return reconnectFail{reason: "the saved connection was removed", action: actionConnect}
	case errors.Is(err, history.ErrCorrupt), errors.Is(err, history.ErrUnavailable):
		return reconnectFail{reason: "the saved connections could not be read", action: actionConnect}
	case errors.Is(err, errNoStoredPassword):
		return reconnectFail{reason: "no password is saved for it", action: actionConnect}
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return reconnectFail{reason: "the database did not answer in time", action: actionConnect}
	case errors.Is(err, audit.ErrLimit):
		return reconnectFail{reason: "the most sources are already open", action: actionFreeSource}
	}
	return reconnectFail{reason: "the database could not be reached", action: actionConnect}
}

func reconnectReason(err error) string { return reconnectWhy(err).reason }

// plainReason makes a sentence fit after "Could not reconnect X:": lower-case first word (unless it is an acronym) and no final stop.
func plainReason(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, ". ")
	if r := []rune(s); len(r) > maxReasonLength {
		s = strings.TrimSpace(string(r[:maxReasonLength]))
	}
	if len(s) > 1 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'a' && s[1] <= 'z' {
		s = strings.ToLower(s[:1]) + s[1:]
	}
	return s
}

// reconnectNextStep is what the lane tells the person to do, by the failure's action.
func reconnectNextStep(action string) string {
	switch action {
	case actionRetry:
		return "Press Start tracing again."
	case actionStartKeeper:
		return "Start the Unravis Key Keeper service, then press Start tracing again."
	case actionFreeSource:
		return "Remove a source, then press Start tracing again."
	}
	return "Press Connect and enter the password."
}

// reconnectFailedFor keeps the failures of databases that are still not open (a failed link may have been opened by hand meanwhile),
// one per database name, in order.
func reconnectFailedFor(failed []tracingReconnectFail, open []audit.Source) []tracingReconnectFail {
	var out []tracingReconnectFail
	seen := map[string]bool{}
	for _, f := range failed {
		key := strings.ToLower(f.Name)
		if seen[key] {
			continue
		}
		isOpen := false
		for _, s := range open {
			if strings.EqualFold(s.Database, f.Name) {
				isOpen = true
				break
			}
		}
		if !isOpen {
			seen[key] = true
			out = append(out, f)
		}
	}
	return out
}

// reconnectFailedReason is the audit lane's reason when saved databases could not be reopened: it names the first one that is also
// a saved name of the case, and says what to do. It returns that failure too, so the answer does not say the same thing twice. Empty
// when none of the failures is one of saved.
func reconnectFailedReason(failed []tracingReconnectFail, saved []string) (string, *tracingReconnectFail) {
	for i, f := range failed {
		for _, name := range saved {
			if strings.EqualFold(strings.TrimSpace(name), f.Name) {
				return "Could not reconnect " + f.Name + ": " + f.Reason + ". " + reconnectNextStep(f.Action), &failed[i]
			}
		}
	}
	return "", nil
}
