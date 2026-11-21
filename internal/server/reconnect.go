package server

import (
	"context"
	"errors"
	"net/http"
	"net/url"
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
	// RemoveScoped drops one saved connection's link without touching another's that names the same database and collection (SEC-09).
	RemoveScoped(savedConnectionID, selectionKey, database, collection string) error
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
	// A password with an unescaped "/" ends the authority too early ("user:pw/more@host"). When the text before the first "/" looks like
	// a login and an "@" follows before any "?" or "#", the real login runs up to the last such "@".
	if at := slashPasswordEnd(rest, end); at >= 0 {
		rest = rest[at+1:]
		end = len(rest)
		if i := strings.IndexAny(rest, "/?#"); i >= 0 {
			end = i
		}
	}
	authority := rest[:end]
	if at := strings.LastIndex(authority, "@"); at >= 0 {
		authority = authority[at+1:]
	}
	return uri[:scheme+3] + authority + rest[end:]
}

// slashPasswordEnd returns the index in rest of the "@" that ends a login whose password holds a "/", "?" or "#", or -1. end is where the
// authority would end by the plain URL rule. It says yes only when the text before it has no "@" and reads as a login ("user:pw", not a host
// list "host:27017"), or when a database name between a "/" and the "@" would hold a "/" (a database name never does).
func slashPasswordEnd(rest string, end int) int {
	if end >= len(rest) || strings.Contains(rest[:end], "@") {
		return -1
	}
	if rest[end] != '/' {
		// "user:pw?x@host/db" and "user:pw#x@host/db": the authority ends at the "?" or "#" inside the password (BL-036).
		return loginEnd(rest, end)
	}
	stop := len(rest)
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		stop = i
	}
	at := strings.LastIndex(rest[:stop], "@")
	if at <= end {
		// The password may hold an unescaped "?" or "#" too ("user:pw/x?y@host"), so the "@" sits after it (BL-036).
		return loginEnd(rest, end)
	}
	if strings.Contains(rest[end+1:at], "/") {
		return at
	}
	return loginEnd(rest, end)
}

// loginEnd finds where a login ends when its password broke the authority early. It only applies when the text before end reads as
// "user:password". The login starts the real address at the last "@" before the first "/" that follows the first "@" (the path), so an "@"
// inside the password, or in a "?" part before the path, is cut with it. An address with no "/" after the login ends at its last "@".
// Limit: a query with an "@" and no path after a login is read as part of the password.
func loginEnd(rest string, end int) int {
	if !loginShape(rest[:end]) {
		return -1
	}
	first := strings.Index(rest[end:], "@")
	if first < 0 {
		return -1
	}
	first += end
	limit := len(rest)
	if slash := strings.Index(rest[first+1:], "/"); slash >= 0 {
		limit = first + 1 + slash
	}
	return strings.LastIndex(rest[:limit], "@")
}

// loginShape says the text reads as "user:password", never as a host list "host:27017": it has a ":" whose tail is not a port.
func loginShape(authority string) bool {
	colon := strings.LastIndex(authority, ":")
	if colon < 0 {
		return false
	}
	tail := authority[colon+1:]
	return tail != "" && !strings.Contains(tail, "]") && !allDigits(tail)
}

// addressWithout is addressOnly with the password the person typed as a hint. A password of digits with a "/" reads exactly like a port and
// a path ("user:1234/5678@host" and "host:27017/app@x" have the same shape), so parsing alone cannot tell them apart. When the known password
// sits in the text right after a ":" and before an "@", the login is cut through that "@" first (BL-036).
func addressWithout(uri, password string) string {
	uri = strings.TrimSpace(uri)
	scheme := strings.Index(uri, "://")
	if password != "" && scheme >= 0 {
		rest := uri[scheme+3:]
		for _, pw := range []string{password, url.QueryEscape(password), url.PathEscape(password)} {
			if i := strings.Index(rest, ":"+pw+"@"); i >= 0 && !strings.ContainsAny(rest[:i], "/?#@") {
				uri = uri[:scheme+3] + rest[i+len(pw)+2:]
				break
			}
		}
	}
	return addressOnly(uri)
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
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
	conn.Password, conn.SavedConnectionID, conn.URI = "", "", addressWithout(conn.URI, conn.Password)
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

// openSourceByID finds an open source by its id. deleteSource calls it before the delete, while the source can still be looked up, so
// the link of a source the person removed can be dropped afterwards.
func (h *handler) openSourceByID(ctx context.Context, id string) (audit.Source, bool) {
	if h.links == nil || h.demo {
		return audit.Source{}, false
	}
	items, err := h.repo.Sources(ctx)
	if err != nil {
		return audit.Source{}, false
	}
	for _, s := range items {
		if s.ID == id {
			return s, true
		}
	}
	return audit.Source{}, false
}

// linkBelongsToSource says whether a link names the open source. The selection key names it when there is one; otherwise the database
// and collection do, and the connection's label tells two saved connections that share them apart.
func linkBelongsToSource(l sourcelinks.Link, src audit.Source) bool {
	if src.SelectionKey != "" {
		return l.SelectionKey == src.SelectionKey
	}
	if l.Database != src.Database || l.Collection != src.Collection {
		return false
	}
	return src.ConnectionLabel == "" || l.ConnectionLabel == src.ConnectionLabel
}

// ambiguousLinks says the source cannot be matched to one saved connection: it has no selection key and no connection label, and the
// links that share its database and collection belong to more than one saved connection. Dropping any of them could drop the wrong
// connection's link, so the caller keeps them all.
func ambiguousLinks(src audit.Source, held []sourcelinks.Link) bool {
	if src.SelectionKey != "" || src.ConnectionLabel != "" {
		return false
	}
	seen := map[string]bool{}
	for _, l := range held {
		seen[l.SavedConnectionID] = true
	}
	return len(seen) > 1
}

// dropLinkOfSource removes the link of an open source before it is deleted, and returns a function that puts it back if the delete
// fails. With no link store, or no such source, it does nothing.
func (h *handler) dropLinkOfSource(ctx context.Context, id string) (restore func()) {
	src, found := h.openSourceByID(ctx, id)
	if !found {
		return func() {}
	}
	var held []sourcelinks.Link
	all, listErr := h.links.List()
	if listErr == nil {
		for _, l := range all {
			if linkBelongsToSource(l, src) {
				held = append(held, l)
			}
		}
	}
	if ambiguousLinks(src, held) {
		return func() {}
	}
	h.forgetOpenedSource(src, held, listErr == nil)
	return func() {
		for _, l := range held {
			_ = h.links.Add(l)
		}
	}
}

// forgetOpenedSource drops the links in held (and any waiting address) of a source the person removed. Each link is dropped for its own
// saved connection, so another saved connection that shares the database and collection keeps its link. listed says held is the
// complete answer; when it is not, no link is dropped, since the wrong saved connection's link could go with it.
func (h *handler) forgetOpenedSource(src audit.Source, held []sourcelinks.Link, listed bool) {
	if h.links == nil {
		return
	}
	if listed {
		for _, l := range held {
			_ = h.links.RemoveScoped(l.SavedConnectionID, l.SelectionKey, l.Database, l.Collection)
		}
	}
	h.reconn.mu.Lock()
	kept := h.reconn.pending[:0]
	for _, p := range h.reconn.pending {
		if !linkBelongsToSource(p.link, src) {
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
		// The person may have removed this source since the list above was read. Look again right before opening, so a removed
		// source is not brought back; the link is the person's record of what they want open.
		if !h.linkStillSaved(l) {
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

// linkStillSaved says whether the link is still in the links file. A file that cannot be read counts as "no": nothing is opened on a
// guess.
func (h *handler) linkStillSaved(l sourcelinks.Link) bool {
	now, err := h.links.List()
	if err != nil {
		return false
	}
	for _, n := range now {
		if linkKey(n) == linkKey(l) {
			return true
		}
	}
	return false
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
