package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/dbhealth"
	"api-audit-explorer/internal/history"
	"api-audit-explorer/internal/keeper"
	"api-audit-explorer/internal/sourcelinks"
)

// reconnLinks is the links file in memory.
type reconnLinks struct {
	mu    sync.Mutex
	links []sourcelinks.Link
}

func (s *reconnLinks) List() ([]sourcelinks.Link, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sourcelinks.Link(nil), s.links...), nil
}

func (s *reconnLinks) Add(l sourcelinks.Link) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.links[:0:0]
	for _, p := range s.links {
		if !(p.SavedConnectionID == l.SavedConnectionID && p.Database == l.Database && p.Collection == l.Collection) {
			kept = append(kept, p)
		}
	}
	s.links = append(kept, l)
	return nil
}

func (s *reconnLinks) Remove(key, database, collection string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.links[:0:0]
	for _, l := range s.links {
		if (key != "" && l.SelectionKey == key) || (key == "" && l.Database == database && l.Collection == collection) {
			continue
		}
		kept = append(kept, l)
	}
	s.links = kept
	return nil
}

func (s *reconnLinks) Forget(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.links[:0:0]
	for _, l := range s.links {
		if l.SavedConnectionID != id {
			kept = append(kept, l)
		}
	}
	s.links = kept
	return nil
}

func (s *reconnLinks) databases() string {
	links, _ := s.List()
	names := make([]string, len(links))
	for i, l := range links {
		names[i] = l.Database
	}
	return strings.Join(names, ",")
}

// reconnRepo is an audit repository that opens sources in memory: it counts, caps and dedupes like the real one, and can be told to
// fail or to hang. It also reads a rollup (through auditFake) so Start tracing can run against it.
type reconnRepo struct {
	*auditFake
	mu        sync.Mutex
	adds      []audit.SourceInput
	held      []audit.SourceInput
	failFor   map[string]error // by database
	hang      bool             // AddSource waits for its context
	deleted   []string
	deleteErr error
}

func (r *reconnRepo) Sources(context.Context) ([]audit.Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.auditFake.listCalls++
	return append([]audit.Source(nil), r.auditFake.sources...), nil
}

func (r *reconnRepo) open(in audit.SourceInput) (audit.Source, error) {
	for _, s := range r.auditFake.sources {
		if s.Database == in.Database && s.Collection == in.Collection {
			return audit.Source{}, fmt.Errorf("%w: %w", audit.ErrInvalidInput, audit.ErrDuplicate)
		}
	}
	if len(r.auditFake.sources) >= audit.MaxSources {
		return audit.Source{}, audit.ErrLimit
	}
	n := len(r.adds) + len(r.held)
	src := audit.Source{ID: fmt.Sprintf("rand-%d", n), SelectionKey: "key-" + in.Database + "-" + in.Collection, Label: in.Label, ConnectionLabel: in.ConnectionLabel, Database: in.Database, Collection: in.Collection}
	r.auditFake.sources = append(r.auditFake.sources, src)
	return src, nil
}

func (r *reconnRepo) AddSource(ctx context.Context, in audit.SourceInput) (audit.Source, error) {
	r.mu.Lock()
	hang := r.hang
	r.mu.Unlock()
	if hang {
		<-ctx.Done()
		return audit.Source{}, ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adds = append(r.adds, in)
	if err := r.failFor[in.Database]; err != nil {
		return audit.Source{}, err
	}
	return r.open(in)
}

func (r *reconnRepo) DeleteSource(_ context.Context, id string) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, s := range r.auditFake.sources {
		if s.ID == id {
			r.auditFake.sources = append(r.auditFake.sources[:i], r.auditFake.sources[i+1:]...)
			r.deleted = append(r.deleted, id)
			return nil
		}
	}
	return audit.ErrNotFound
}

func (r *reconnRepo) ValidateConnectionSettings(audit.ConnectionInput) error { return nil }

func (r *reconnRepo) addCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.adds)
}

// heldRepo is the shared layout's router: AddHeldSource opens a source whose password the keeper holds.
type heldRepo struct{ *reconnRepo }

func (r heldRepo) AddHeldSource(_ context.Context, in audit.SourceInput) (audit.Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.held = append(r.held, in)
	return r.open(in)
}

func (r heldRepo) SourcesWithKeeper(ctx context.Context) ([]audit.Source, map[string]keeper.HeldState, error) {
	items, err := r.Sources(ctx)
	return items, nil, err
}

// reconnHistory is the saved MongoDB connections: each has a password or not.
type reconnHistory struct {
	mu          sync.Mutex
	items       []history.Recent
	passwords   map[string]string
	resolveErr  error
	resolved    int
	remembered  int
	deletedByID []string
	deleteErr   error
}

func (h *reconnHistory) List() ([]history.Recent, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]history.Recent(nil), h.items...), nil
}

func (h *reconnHistory) Remember(label string, in audit.ConnectionInput, collection string, remember bool) (history.Recent, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.remembered++
	rec := history.Recent{ID: fmt.Sprintf("saved-%d", len(h.items)+1), Label: label, URI: in.URI, Username: in.Username, AuthSource: in.AuthSource, TLSMode: in.TLSMode, Collection: collection, HasCredentials: remember}
	h.items = append(h.items, rec)
	if remember {
		if h.passwords == nil {
			h.passwords = map[string]string{}
		}
		h.passwords[rec.ID] = in.Password
	}
	return rec, nil
}

func (h *reconnHistory) Resolve(in audit.ConnectionInput) (audit.ConnectionInput, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.resolved++
	if h.resolveErr != nil {
		return audit.ConnectionInput{}, h.resolveErr
	}
	for _, rec := range h.items {
		if rec.ID == in.SavedConnectionID {
			in.Password = h.passwords[rec.ID]
			in.SavedConnectionID = ""
			return in, nil
		}
	}
	return audit.ConnectionInput{}, history.ErrNotFound
}

func (h *reconnHistory) Delete(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.deletedByID = append(h.deletedByID, id)
	if h.deleteErr != nil {
		return h.deleteErr
	}
	return nil
}

func (h *reconnHistory) resolveCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.resolved
}

// heldHistory is the key keeper's saved list: it says which saved connections the keeper holds the password for.
type heldHistory struct {
	*reconnHistory
	heldIDs map[string]bool
}

func (h heldHistory) Held(in audit.ConnectionInput) (bool, error) {
	return h.heldIDs[in.SavedConnectionID], nil
}
func (h heldHistory) TestSaved(context.Context, audit.ConnectionInput) (audit.ConnectionResult, error) {
	return audit.ConnectionResult{}, errors.New("not used")
}
func (h heldHistory) HealthSaved(context.Context, audit.ConnectionInput, string, string) (dbhealth.Report, error) {
	return dbhealth.Report{}, errors.New("not used")
}
func (h heldHistory) LaneSaved(context.Context, audit.ConnectionInput, string, dbhealth.LaneRequest) (dbhealth.Report, error) {
	return dbhealth.Report{}, errors.New("not used")
}
func (h heldHistory) RememberChecked(_ context.Context, label string, in audit.ConnectionInput, collection string, remember bool) (history.Recent, error) {
	return h.Remember(label, in, collection, remember)
}

const reconnAddress = "mongodb://db.example"

func savedRecent(id string, withPassword bool) history.Recent {
	return history.Recent{ID: id, Label: "Sandbox", URI: reconnAddress, Username: "reader", AuthSource: "admin", TLSMode: "system", HasCredentials: withPassword}
}

func newReconnRepo(open ...audit.Source) *reconnRepo {
	return &reconnRepo{auditFake: &auditFake{sources: open}, failFor: map[string]error{}}
}

func linkTo(id, database string) sourcelinks.Link {
	return sourcelinks.Link{SavedConnectionID: id, Database: database, Collection: "audit", Label: database + " audit", ConnectionLabel: "Sandbox", SelectionKey: "key-" + database + "-audit"}
}

// reconnectHandler wires a handler with the links and the saved connections.
func reconnectHandler(t *testing.T, repo audit.Repository, saved savedHistory, links *reconnLinks) *handler {
	t.Helper()
	h, ok := New(repo, nil, testToken, false, WithHistory(saved), WithSourceLinks(links)).(*handler)
	if !ok {
		t.Fatal("New did not return the handler")
	}
	return h
}

func TestReconnectOpensASavedSourceWhoseConnectionStoresItsPassword(t *testing.T) {
	repo := newReconnRepo()
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "stored-secret"}}
	links := &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll")}}
	h := reconnectHandler(t, repo, saved, links)

	failed := h.reconnectSaved(context.Background(), time.Second, nil)
	if len(failed) != 0 || repo.addCount() != 1 {
		t.Fatalf("failed %+v, %d adds", failed, repo.addCount())
	}
	got := repo.adds[0]
	if got.Database != "payroll" || got.Collection != "audit" || got.Label != "payroll audit" || got.URI != reconnAddress || got.Password != "stored-secret" || got.SavedConnectionID != "" {
		t.Errorf("source input = %+v", got)
	}

	// Idempotent: the source is open now, so a second pass opens nothing and does not even look the password up again.
	resolved := saved.resolveCount()
	if failed := h.reconnectSaved(context.Background(), time.Second, nil); len(failed) != 0 || repo.addCount() != 1 || saved.resolveCount() != resolved {
		t.Errorf("second pass: failed %+v, %d adds, %d resolves", failed, repo.addCount(), saved.resolveCount()-resolved)
	}
}

func TestReconnectLeavesASavedConnectionWithoutAPasswordAlone(t *testing.T) {
	repo := newReconnRepo()
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", false), savedRecent("saved-2", true)}, passwords: map[string]string{"saved-2": "pw"}}
	links := &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll"), linkTo("gone", "orders")}}
	h := reconnectHandler(t, repo, saved, links)

	failed := h.reconnectSaved(context.Background(), time.Second, nil)
	if len(failed) != 0 || repo.addCount() != 0 || saved.resolveCount() != 0 {
		t.Errorf("failed %+v, %d adds, %d resolves: nothing should be tried for a connection with no stored password or one that is gone", failed, repo.addCount(), saved.resolveCount())
	}
	// The link of the deleted saved connection is dropped; the one whose password is not stored stays, for its Connect button.
	if got := links.databases(); got != "payroll" {
		t.Errorf("links left = %q", got)
	}
}

func TestReconnectTreatsAnAlreadyOpenSourceAsDone(t *testing.T) {
	// The page opened it by hand a moment ago, under a key the link does not know (an older link without a key).
	repo := newReconnRepo()
	repo.failFor["payroll"] = fmt.Errorf("%w: %w", audit.ErrInvalidInput, audit.ErrDuplicate)
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "pw"}}
	h := reconnectHandler(t, repo, saved, &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll")}})
	if failed := h.reconnectSaved(context.Background(), time.Second, nil); len(failed) != 0 {
		t.Errorf("a duplicate is not a failure: %+v", failed)
	}
}

func TestReconnectReportsAReasonAndLeavesAFailedSourceAloneForAWhile(t *testing.T) {
	repo := newReconnRepo()
	repo.failFor["payroll"] = &audit.ConnectionError{Stage: "authentication", Code: "auth_failed", Message: "Authentication failed."}
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "pw"}}
	h := reconnectHandler(t, repo, saved, &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll")}})

	failed := h.reconnectSaved(context.Background(), time.Second, nil)
	if len(failed) != 1 || failed[0].Name != "payroll" || failed[0].Reason != "authentication failed" {
		t.Fatalf("failed = %+v", failed)
	}
	if strings.Contains(fmt.Sprint(failed), "pw") || strings.Contains(fmt.Sprint(failed), reconnAddress) {
		t.Errorf("a failure carries a secret or an address: %+v", failed)
	}
	// No retry loop: pressing Start tracing again at once reports the same reason without dialling again.
	again := h.reconnectSaved(context.Background(), time.Second, nil)
	if repo.addCount() != 1 || len(again) != 1 || again[0].Reason != "authentication failed" {
		t.Errorf("second pass: %d adds, %+v", repo.addCount(), again)
	}
	// After the cool-down it tries once more, and a source that now opens clears the failure.
	for k, f := range h.reconn.failed {
		f.until = time.Now().Add(-time.Second)
		h.reconn.failed[k] = f
	}
	delete(repo.failFor, "payroll")
	if failed := h.reconnectSaved(context.Background(), time.Second, nil); len(failed) != 0 || repo.addCount() != 2 || len(h.reconn.failed) != 0 {
		t.Errorf("third pass: %+v, %d adds, %d recorded failures", failed, repo.addCount(), len(h.reconn.failed))
	}
}

func TestReconnectIsBoundedByItsBudget(t *testing.T) {
	repo := newReconnRepo()
	repo.hang = true
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "pw"}}
	links := &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll"), linkTo("saved-1", "orders")}}
	h := reconnectHandler(t, repo, saved, links)

	started := time.Now()
	failed := h.reconnectSaved(context.Background(), 80*time.Millisecond, nil)
	if took := time.Since(started); took > 2*time.Second {
		t.Fatalf("took %s with an 80ms budget", took)
	}
	// The budget ran out while the first was being tried: nobody blames the database, and no cool-down starts.
	if len(failed) != 2 || failed[0].Reason != reconnectOutOfTime || failed[1].Reason != reconnectOutOfTime || failed[0].Action != actionRetry {
		t.Errorf("failed = %+v", failed)
	}
	if len(h.reconn.failed) != 0 {
		t.Errorf("a cut-short pass recorded failures: %+v", h.reconn.failed)
	}
	repo.mu.Lock()
	repo.hang = false
	repo.mu.Unlock()
	if failed := h.reconnectSaved(context.Background(), time.Second, nil); len(failed) != 0 || repo.addCount() != 2 {
		t.Errorf("the next pass should try again at once: %+v, %d adds", failed, repo.addCount())
	}
}

func TestReconnectGivesUpQuietlyWhenTheCallerLeaves(t *testing.T) {
	repo := newReconnRepo()
	repo.hang = true
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "pw"}}
	h := reconnectHandler(t, repo, saved, &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll")}})
	ctx, leave := context.WithCancel(context.Background())
	time.AfterFunc(30*time.Millisecond, leave)
	failed := h.reconnectSaved(ctx, time.Minute, nil)
	if len(failed) != 1 || failed[0].Reason != reconnectOutOfTime || len(h.reconn.failed) != 0 {
		t.Errorf("failed %+v, recorded %+v", failed, h.reconn.failed)
	}
}

func TestReconnectOnlyOpensTheNamedDatabasesAndStartupWaitsLongerAfterAFailure(t *testing.T) {
	repo := newReconnRepo()
	repo.failFor["orders"] = errors.New("dial tcp: refused")
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "pw"}}
	h := reconnectHandler(t, repo, saved, &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll"), linkTo("saved-1", "orders")}})

	if failed := h.reconnectSaved(context.Background(), time.Second, []string{"Payroll"}); len(failed) != 0 || repo.addCount() != 1 || repo.adds[0].Database != "payroll" {
		t.Fatalf("failed %+v, adds %+v", failed, repo.adds)
	}
	// The start-up pass tries the rest; its failure is left alone for longer than a Start tracing failure.
	ReconnectSavedSources(context.Background(), h, time.Second)
	if repo.addCount() != 2 {
		t.Fatalf("%d adds", repo.addCount())
	}
	for _, f := range h.reconn.failed {
		if left := time.Until(f.until); left < 2*reconnectCooldown {
			t.Errorf("start-up cool-down is only %s", left)
		}
	}
	h.reconnectSaved(context.Background(), time.Second, []string{"orders"})
	if repo.addCount() != 2 {
		t.Errorf("a failed link was dialled again inside its cool-down")
	}
}

func TestReconnectTellsEachFailureWhatToDoNext(t *testing.T) {
	const connect = "Press Connect and enter the password."
	for _, tc := range []struct {
		err    error
		action string
		step   string
	}{
		{&audit.ConnectionError{Message: "Authentication failed."}, actionConnect, connect},
		{context.DeadlineExceeded, actionConnect, connect},
		{errors.New("refused"), actionConnect, connect},
		{keeper.ErrNotRunning, actionStartKeeper, "Start the Unravis Key Keeper service, then press Start tracing again."},
		{audit.ErrLimit, actionFreeSource, "Remove a source, then press Start tracing again."},
		{&keeper.WireError{Code: keeper.CodeBusy}, actionFreeSource, "Remove a source, then press Start tracing again."},
	} {
		why := reconnectWhy(tc.err)
		if why.action != tc.action || reconnectNextStep(why.action) != tc.step {
			t.Errorf("%v: action %q, step %q", tc.err, why.action, reconnectNextStep(why.action))
		}
	}
	reason, said := reconnectFailedReason([]tracingReconnectFail{{Name: "payroll", Reason: "the Unravis Key Keeper service is not running", Action: actionStartKeeper}}, []string{"payroll"})
	if reason != "Could not reconnect payroll: the Unravis Key Keeper service is not running. Start the Unravis Key Keeper service, then press Start tracing again." || said == nil {
		t.Errorf("reason = %q", reason)
	}
}

func TestPlainReasonCutsByLettersNotBytes(t *testing.T) {
	long := strings.Repeat("\u00e9", maxReasonLength+20)
	got := plainReason(long)
	if n := len([]rune(got)); n != maxReasonLength || !utf8.ValidString(got) {
		t.Errorf("%d letters, valid=%v", n, utf8.ValidString(got))
	}
}

func TestATypedAddressIsKeptAndComparedWithoutItsLogin(t *testing.T) {
	if got := addressOnly("mongodb://reader:s3cret@db.example:27017/admin?tls=true"); got != "mongodb://db.example:27017/admin?tls=true" {
		t.Errorf("addressOnly = %q", got)
	}
	if got := addressOnly(" mongodb+srv://a:b@c@host.example "); got != "mongodb+srv://host.example" {
		t.Errorf("addressOnly with an @ in the password = %q", got)
	}
	if got := addressOnly("db.example:27017"); got != "db.example:27017" {
		t.Errorf("addressOnly of a bare host = %q", got)
	}
	rec := history.Recent{URI: "mongodb://db.example", Username: "reader", AuthSource: "admin", TLSMode: "system"}
	if !sameAddress(rec, audit.ConnectionInput{URI: "mongodb://reader:s3cret@db.example", Username: "reader", AuthSource: "admin", TLSMode: "system"}) {
		t.Errorf("the same host typed with a login in the string should match")
	}
	// What is kept in memory while waiting carries no login.
	h := reconnectHandler(t, newReconnRepo(), &reconnHistory{}, &reconnLinks{})
	h.noteOpenedSource(audit.Source{Database: "payroll", Collection: "audit"}, "", audit.ConnectionInput{URI: "mongodb://reader:s3cret@db.example", Password: "s3cret"})
	if len(h.reconn.pending) != 1 || strings.Contains(fmt.Sprintf("%+v", h.reconn.pending), "s3cret") {
		t.Errorf("pending = %+v", h.reconn.pending)
	}
}

func TestReconnectNeverRunsTwoPassesAtOnce(t *testing.T) {
	repo := newReconnRepo()
	repo.hang = true
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "pw"}}
	h := reconnectHandler(t, repo, saved, &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll")}})

	ctx, stop := context.WithCancel(context.Background())
	first := make(chan struct{})
	go func() { h.reconnectSaved(ctx, time.Minute, nil); close(first) }()
	for i := 0; i < 200 && len(h.reconn.gate) == 0; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	// The second caller gives up at its own budget instead of starting a second dial.
	second := make(chan []tracingReconnectFail, 1)
	go func() { second <- h.reconnectSaved(context.Background(), 50*time.Millisecond, nil) }()
	select {
	case <-second:
	case <-time.After(2 * time.Second):
		t.Fatal("the second pass did not give up at its budget")
	}
	stop()
	<-first
}

func TestReconnectStopsAtTheSourceLimit(t *testing.T) {
	var open []audit.Source
	for i := 0; i < audit.MaxSources; i++ {
		open = append(open, audit.Source{ID: fmt.Sprintf("o%d", i), SelectionKey: fmt.Sprintf("k%d", i), Database: fmt.Sprintf("db%d", i), Collection: "audit"})
	}
	repo := newReconnRepo(open...)
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "pw"}}
	h := reconnectHandler(t, repo, saved, &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll")}})

	failed := h.reconnectSaved(context.Background(), time.Second, nil)
	if len(failed) != 1 || failed[0].Reason != "the most sources are already open" || len(repo.auditFake.sources) != audit.MaxSources {
		t.Errorf("failed %+v, %d sources open", failed, len(repo.auditFake.sources))
	}
}

func TestReconnectGivesPlainReasonsForTheOtherFailures(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{keeper.ErrNotRunning, "the Unravis Key Keeper service is not running"},
		{history.ErrNotFound, "the saved connection was removed"},
		{history.ErrCorrupt, "the saved connections could not be read"},
		{errNoStoredPassword, "no password is saved for it"},
		{context.DeadlineExceeded, "the database did not answer in time"},
		{errors.New("dial tcp 10.1.2.3:27017: connection refused"), "the database could not be reached"},
		{&audit.ConnectionError{Message: "Could not find the server."}, "could not find the server"},
	} {
		if got := reconnectReason(tc.err); got != tc.want {
			t.Errorf("reconnectReason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
	if got, _ := reconnectFailedReason([]tracingReconnectFail{{Name: "payroll", Reason: "authentication failed", Action: actionConnect}}, []string{"Other", "Payroll"}); got != "Could not reconnect payroll: authentication failed. Press Connect and enter the password." {
		t.Errorf("reason = %q", got)
	}
	if got, _ := reconnectFailedReason([]tracingReconnectFail{{Name: "payroll", Reason: "x"}}, []string{"orders"}); got != "" {
		t.Errorf("a failure of another database changed the reason: %q", got)
	}
}

func TestReconnectDoesNothingWithoutTheLinkStore(t *testing.T) {
	if got := (&handler{}).reconnectSaved(context.Background(), time.Second, nil); got != nil {
		t.Errorf("zero handler: %+v", got)
	}
	repo := newReconnRepo()
	h, _ := New(repo, nil, testToken, false, WithHistory(&reconnHistory{})).(*handler)
	if got := h.reconnectSaved(context.Background(), time.Second, nil); got != nil || repo.addCount() != 0 {
		t.Errorf("no store: %+v, %d adds", got, repo.addCount())
	}
	ReconnectSavedSources(context.Background(), http.NotFoundHandler(), time.Second) // not the handler: no panic
}

func TestReconnectUsesTheKeeperForAPasswordItHolds(t *testing.T) {
	repo := heldRepo{newReconnRepo()}
	base := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}}
	saved := heldHistory{reconnHistory: base, heldIDs: map[string]bool{"saved-1": true}}
	h := reconnectHandler(t, repo, saved, &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll")}})

	if failed := h.reconnectSaved(context.Background(), time.Second, nil); len(failed) != 0 {
		t.Fatalf("failed %+v", failed)
	}
	if len(repo.held) != 1 || repo.held[0].Password != "" || repo.held[0].SavedConnectionID != "saved-1" || repo.addCount() != 0 || base.resolveCount() != 0 {
		t.Errorf("held %+v, %d plain adds, %d resolves: the password must stay in the keeper", repo.held, repo.addCount(), base.resolveCount())
	}
}

// ---------------------------------------------------------------------------------------------------------------------------------
// What the add-source and remove-source routes leave behind.

func addSourceBody(t *testing.T, in audit.SourceInput) string {
	t.Helper()
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestAddSourceFromASavedConnectionLeavesALinkWithNoSecret(t *testing.T) {
	repo := newReconnRepo()
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "stored-secret"}}
	links := &reconnLinks{}
	h := reconnectHandler(t, repo, saved, links)

	in := audit.SourceInput{Label: "Payroll", ConnectionLabel: "Sandbox", URI: reconnAddress, Username: "reader", AuthSource: "admin", TLSMode: "system", SavedConnectionID: "saved-1", Database: "payroll", Collection: "audit"}
	if res := request(h, http.MethodPost, "/api/sources", addSourceBody(t, in), true); res.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", res.Code, res.Body)
	}
	got, _ := links.List()
	if len(got) != 1 || got[0].SavedConnectionID != "saved-1" || got[0].Database != "payroll" || got[0].Collection != "audit" || got[0].Label != "Payroll" || got[0].SelectionKey == "" {
		t.Fatalf("links = %+v", got)
	}
	if raw, _ := json.Marshal(got); strings.Contains(string(raw), "stored-secret") || strings.Contains(string(raw), reconnAddress) {
		t.Errorf("a link carries a secret or an address: %s", raw)
	}
}

func TestSourceTypedByHandIsLinkedOnceItsConnectionIsSaved(t *testing.T) {
	repo := newReconnRepo()
	saved := &reconnHistory{}
	links := &reconnLinks{}
	h := reconnectHandler(t, repo, saved, links)

	typed := audit.SourceInput{Label: "Payroll", URI: reconnAddress, Username: "reader", Password: "typed-secret", AuthSource: "admin", TLSMode: "system", Database: "payroll", Collection: "audit"}
	if res := request(h, http.MethodPost, "/api/sources", addSourceBody(t, typed), true); res.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", res.Code, res.Body)
	}
	if links.databases() != "" {
		t.Fatalf("a link was made before there was a saved connection: %q", links.databases())
	}
	// The page then saves the connection with the same details.
	conn := audit.ConnectionInput{URI: reconnAddress, Username: "reader", Password: "typed-secret", AuthSource: "admin", TLSMode: "system"}
	body, _ := json.Marshal(map[string]any{"label": "Sandbox", "connection": conn, "collection": "audit", "rememberPassword": true})
	if res := request(h, http.MethodPost, "/api/connections/remember", string(body), true); res.Code != http.StatusOK {
		t.Fatalf("remember = %d %s", res.Code, res.Body)
	}
	got, _ := links.List()
	if len(got) != 1 || got[0].SavedConnectionID != "saved-1" || got[0].Database != "payroll" {
		t.Fatalf("links = %+v", got)
	}
	if raw, _ := json.Marshal(got); strings.Contains(string(raw), "typed-secret") {
		t.Errorf("a link carries the typed password: %s", raw)
	}
}

func TestRemovingASourceOrDeletingItsConnectionDropsTheLink(t *testing.T) {
	repo := newReconnRepo()
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true), savedRecent("saved-2", true)}, passwords: map[string]string{"saved-1": "a", "saved-2": "b"}}
	links := &reconnLinks{}
	h := reconnectHandler(t, repo, saved, links)
	for _, db := range []string{"payroll", "orders"} {
		in := audit.SourceInput{Label: db, URI: reconnAddress, Username: "reader", AuthSource: "admin", TLSMode: "system", SavedConnectionID: "saved-1", Database: db, Collection: "audit"}
		if res := request(h, http.MethodPost, "/api/sources", addSourceBody(t, in), true); res.Code != http.StatusCreated {
			t.Fatalf("add %s = %d %s", db, res.Code, res.Body)
		}
	}
	in := audit.SourceInput{Label: "billing", URI: reconnAddress, Username: "reader", AuthSource: "admin", TLSMode: "system", SavedConnectionID: "saved-2", Database: "billing", Collection: "audit"}
	if res := request(h, http.MethodPost, "/api/sources", addSourceBody(t, in), true); res.Code != http.StatusCreated {
		t.Fatalf("add billing = %d %s", res.Code, res.Body)
	}
	if links.databases() != "payroll,orders,billing" {
		t.Fatalf("links = %q", links.databases())
	}

	// Remove the payroll source: its link goes, and a restart will not bring it back.
	if res := request(h, http.MethodDelete, "/api/sources/"+repo.auditFake.sources[0].ID, "", true); res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.Code, res.Body)
	}
	if links.databases() != "orders,billing" {
		t.Errorf("after removing a source: %q", links.databases())
	}
	// Delete the saved connection: every link of it goes.
	if res := request(h, http.MethodDelete, "/api/connections/recent/saved-1", "", true); res.Code != http.StatusNoContent {
		t.Fatalf("delete connection = %d %s", res.Code, res.Body)
	}
	if links.databases() != "billing" {
		t.Errorf("after deleting a connection: %q", links.databases())
	}
}

// ---------------------------------------------------------------------------------------------------------------------------------
// Start tracing.

func reconnectTracingEnv(t *testing.T, repo audit.Repository, saved savedHistory, links *reconnLinks) (*caseEnv, string) {
	t.Helper()
	store, _ := tagStores(t)["memory"]()
	e := tagEnv(t, store, repo, WithHistory(saved), WithSourceLinks(links))
	id := e.newCase("tracing")
	seedAuditDatabase(t, e, id, "payroll", casestore.ClassAudit)
	return e, id
}

func TestStartTracingReopensTheSavedDatabaseBeforeReadingIt(t *testing.T) {
	repo := newReconnRepo()
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "pw"}}
	e, id := reconnectTracingEnv(t, repo, saved, &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll")}})

	out, raw := tracingPost(t, e, id, tracingBody(""))
	lane := tracingLaneOf(t, out, "audit")
	if repo.addCount() != 1 || len(out.SavedNotConnected) != 0 || len(out.ReconnectFailed) != 0 || lane.Reason == tracingAuditSavedOnlyMsg {
		t.Errorf("%d adds, saved-not-connected %v, failed %+v, lane %q", repo.addCount(), out.SavedNotConnected, out.ReconnectFailed, lane.Reason)
	}
	if len(out.AuditSources) != 1 || strings.Contains(raw, "reconnectFailed") || strings.Contains(raw, `"pw"`) || strings.Contains(raw, reconnAddress) {
		t.Errorf("audit sources %+v; body %s", out.AuditSources, raw)
	}
}

func TestStartTracingSaysWhyASavedDatabaseCouldNotBeReopened(t *testing.T) {
	repo := newReconnRepo()
	repo.failFor["payroll"] = &audit.ConnectionError{Stage: "authentication", Code: "auth_failed", Message: "Authentication failed."}
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "pw"}}
	e, id := reconnectTracingEnv(t, repo, saved, &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll")}})

	out, raw := tracingPost(t, e, id, tracingBody(""))
	// The lane says it, so the notes under the chart do not repeat it.
	if len(out.ReconnectFailed) != 0 {
		t.Fatalf("reconnectFailed = %+v", out.ReconnectFailed)
	}
	lane := tracingLaneOf(t, out, "audit")
	if want := "Could not reconnect payroll: authentication failed. Press Connect and enter the password."; lane.Reason != want {
		t.Errorf("lane reason = %q, want %q", lane.Reason, want)
	}
	if strings.Join(out.SavedNotConnected, "|") != "payroll" || lane.State == "" {
		t.Errorf("saved-not-connected %v, lane %+v", out.SavedNotConnected, lane)
	}
	if strings.Contains(raw, "pw\"") || strings.Contains(raw, reconnAddress) {
		t.Errorf("the answer carries a secret or an address: %s", raw)
	}
}

func TestStartTracingKeepsTheConnectButtonForASavedConnectionWithoutAPassword(t *testing.T) {
	repo := newReconnRepo()
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", false)}}
	e, id := reconnectTracingEnv(t, repo, saved, &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll")}})

	out, _ := tracingPost(t, e, id, tracingBody(""))
	lane := tracingLaneOf(t, out, "audit")
	if repo.addCount() != 0 || len(out.ReconnectFailed) != 0 || lane.Reason != tracingAuditSavedOnlyMsg || strings.Join(out.SavedNotConnected, "|") != "payroll" {
		t.Errorf("%d adds, failed %+v, lane %q, saved %v", repo.addCount(), out.ReconnectFailed, lane.Reason, out.SavedNotConnected)
	}
}

func TestStartTracingStillAnswersWhenTheDatabaseHangs(t *testing.T) {
	repo := newReconnRepo()
	repo.hang = true
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "pw"}}
	e, id := reconnectTracingEnv(t, repo, saved, &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll")}})

	started := time.Now()
	out, _ := tracingPost(t, e, id, tracingBody(""))
	if took := time.Since(started); took > tracingReconnectBudget+5*time.Second {
		t.Errorf("Start tracing took %s", took)
	}
	if lane := tracingLaneOf(t, out, "audit"); lane.Reason != "Could not reconnect payroll: the database did not answer in time. Press Connect and enter the password." {
		t.Errorf("lane reason = %q", lane.Reason)
	}
	if laneKeys(out) == "" {
		t.Errorf("no lanes in the answer")
	}
}

func TestStartTracingOnlyReopensTheDatabasesTheCaseSaved(t *testing.T) {
	repo := newReconnRepo()
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "pw"}}
	e, id := reconnectTracingEnv(t, repo, saved, &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll"), linkTo("saved-1", "billing")}})

	tracingPost(t, e, id, tracingBody(""))
	if repo.addCount() != 1 || repo.adds[0].Database != "payroll" {
		t.Errorf("adds = %+v: billing is not one of this case's databases", repo.adds)
	}
}

func TestStartTracingNamesTheNextStepAndKeepsOtherFailuresAsNotes(t *testing.T) {
	repo := newReconnRepo()
	repo.failFor["payroll"] = &audit.ConnectionError{Message: "Authentication failed."}
	repo.failFor["orders"] = keeper.ErrNotRunning
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "pw"}}
	e, id := reconnectTracingEnv(t, repo, saved, &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll"), linkTo("saved-1", "orders")}})
	seedAuditDatabase(t, e, id, "orders", casestore.ClassAudit)

	out, _ := tracingPost(t, e, id, tracingBody(""))
	lane := tracingLaneOf(t, out, "audit")
	if lane.Reason != "Could not reconnect payroll: authentication failed. Press Connect and enter the password." {
		t.Errorf("lane reason = %q", lane.Reason)
	}
	if len(out.ReconnectFailed) != 1 || out.ReconnectFailed[0].Name != "orders" || out.ReconnectFailed[0].Action != actionStartKeeper {
		t.Errorf("reconnectFailed = %+v", out.ReconnectFailed)
	}
}

func TestAFailedRemoveKeepsTheLinkAndAMissingConnectionDropsIt(t *testing.T) {
	repo := newReconnRepo()
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "pw"}}
	links := &reconnLinks{}
	h := reconnectHandler(t, repo, saved, links)
	in := audit.SourceInput{Label: "payroll", URI: reconnAddress, Username: "reader", AuthSource: "admin", TLSMode: "system", SavedConnectionID: "saved-1", Database: "payroll", Collection: "audit"}
	if res := request(h, http.MethodPost, "/api/sources", addSourceBody(t, in), true); res.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", res.Code, res.Body)
	}
	repo.mu.Lock()
	repo.deleteErr = errors.New("the database is busy")
	repo.mu.Unlock()
	if res := request(h, http.MethodDelete, "/api/sources/"+repo.auditFake.sources[0].ID, "", true); res.Code == http.StatusNoContent {
		t.Fatalf("delete should have failed")
	}
	if links.databases() != "payroll" {
		t.Errorf("a source that could not be removed lost its link: %q", links.databases())
	}
	// A saved connection that is already gone: deleting it again answers 404 and still drops what was linked to it.
	saved.mu.Lock()
	saved.items = nil
	saved.mu.Unlock()
	saved.deleteErr = history.ErrNotFound
	if res := request(h, http.MethodDelete, "/api/connections/recent/saved-1", "", true); res.Code != http.StatusNotFound {
		t.Fatalf("delete connection = %d %s", res.Code, res.Body)
	}
	if links.databases() != "" {
		t.Errorf("links left after the connection was found gone: %q", links.databases())
	}
}
