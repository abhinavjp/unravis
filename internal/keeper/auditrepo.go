package keeper

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/store"
)

// AuditRouter is the audit repository Unravis uses in the shared install (P14b). Sources opened from a saved link whose password the
// key keeper holds are opened BY the keeper (AddHeldSource) and every read of them goes there over the pipe; every other source (a
// typed password, a link with no password, a password typed earlier this session) stays in this process's own repository, as in a
// per-user install. The keeper's password therefore never reaches this process: the router only ever sends the saved link's id and
// the typed filters of the audit package.
type AuditRouter struct {
	local    audit.Repository
	client   *Client
	instance string

	mu     sync.Mutex
	held   map[string]audit.Source // sources the keeper opened for this Unravis, as last seen
	down   bool                    // the last keeper call found it stopped (or something else on its pipe)
	seated bool                    // the keeper has (or had) a seat for this Unravis
}

// MaxAuditAnswer bounds one audit answer read from the keeper. The audit repository's own budgets keep real answers far below it
// (16 MiB for the largest); the bound stops a broken keeper from filling this process's memory.
const MaxAuditAnswer = 64 << 20

// Errors of the router, worded by the HTTP handlers.
var (
	// ErrMixedSources is a read that names sources opened by the keeper together with sources opened in this Unravis (a timeline is the
	// one read that merges both).
	ErrMixedSources = errors.New("sources opened by the key keeper and by this Unravis cannot be read together")
	// ErrHeldSourceGone is a source the keeper no longer has open: it was restarted, or the source sat unused for a long time.
	ErrHeldSourceGone = fmt.Errorf("%w: the key keeper no longer has this audit source open", audit.ErrNotFound)
	errHeldDuplicate  = fmt.Errorf("%w: source profile already exists", audit.ErrInvalidInput)
	errHeldLimit      = fmt.Errorf("%w: maximum source count reached", audit.ErrLimit)
)

// NewAuditRouter routes between local and the keeper reached through c.
func NewAuditRouter(local audit.Repository, c *Client) *AuditRouter {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Without randomness the instance is still unique per process start (the keeper also keys by process id).
		copy(b[:], fmt.Sprintf("%016x", time.Now().UnixNano()))
	}
	return &AuditRouter{local: local, client: c, instance: hex.EncodeToString(b[:]), held: map[string]audit.Source{}}
}

// keeperUnreachable reports whether err means the keeper could not be used at all (stopped, an impostor, a dropped pipe).
func keeperUnreachable(err error) bool {
	return errors.Is(err, ErrNotRunning) || errors.Is(err, ErrImpostor) || errors.Is(err, errTransport)
}

// fromKeeper turns a keeper answer into the error the local repository would have returned.
func (r *AuditRouter) fromKeeper(err error) error {
	if err == nil {
		return nil
	}
	if keeperUnreachable(err) {
		r.mu.Lock()
		r.down = true
		r.mu.Unlock()
		return err
	}
	var we *WireError
	if errors.As(err, &we) && we.Code == CodeAudit {
		return auditNative(we)
	}
	return native(err, kindMongo)
}

func (r *AuditRouter) isHeld(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.held[id]
	return ok
}

func (r *AuditRouter) forget(id string) {
	r.mu.Lock()
	delete(r.held, id)
	r.mu.Unlock()
}

// side says where a read of ids runs: true for the keeper. A mix is refused.
func (r *AuditRouter) side(ids []string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	held, local := 0, 0
	for _, id := range ids {
		if _, ok := r.held[id]; ok {
			held++
		} else {
			local++
		}
	}
	if held > 0 && local > 0 {
		return false, ErrMixedSources
	}
	return held > 0, nil
}

// read runs one audit read in the keeper and decodes its streamed answer into out.
func (r *AuditRouter) read(ctx context.Context, op string, input any, out any) error {
	rc, err := r.client.Open(ctx, op, AuditArgs[any]{Instance: r.instance, Input: input}, nil)
	if err != nil {
		return r.goneOrNative(err)
	}
	if rc == nil {
		return &WireError{Code: CodeInternal, Message: "The key keeper's answer was not understood."}
	}
	defer rc.Close()
	limited := &io.LimitedReader{R: rc, N: MaxAuditAnswer + 1}
	if err := json.NewDecoder(limited).Decode(out); err != nil {
		var we *WireError
		if errors.As(err, &we) || keeperUnreachable(err) || ctx.Err() != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return r.goneOrNative(err)
		}
		if limited.N <= 0 {
			return fmt.Errorf("%w: the answer from the key keeper is too large", audit.ErrLimit)
		}
		return &WireError{Code: CodeInternal, Message: "The key keeper's answer was not understood."}
	}
	// Read to the end so a failure the keeper sent after the answer is not missed.
	if _, err := io.Copy(io.Discard, limited); err != nil {
		return r.goneOrNative(err)
	}
	if limited.N <= 0 {
		return fmt.Errorf("%w: the answer from the key keeper is too large", audit.ErrLimit)
	}
	return nil
}

// goneOrNative is fromKeeper, and a seat the keeper no longer has drops every source this router thought it held.
func (r *AuditRouter) goneOrNative(err error) error {
	err = r.fromKeeper(err)
	if errors.Is(err, ErrHeldSourceGone) {
		r.mu.Lock()
		r.held = map[string]audit.Source{}
		r.mu.Unlock()
	}
	return err
}

/* --------------------------------------------------------- sources */

// HeldState is how a source is opened, for the sources list: "" for this Unravis, "held" for the keeper, "stopped" for the keeper when
// it cannot be reached (the source cannot be read until it runs again).
type HeldState string

const (
	HeldByKeeper HeldState = "held"
	HeldButDown  HeldState = "stopped"
)

// AddHeldSource opens a source from a saved link in the keeper. in carries the saved link's id and the settings the person saw; a
// password in it is refused (a typed password opens the source here, with AddSource).
func (r *AuditRouter) AddHeldSource(ctx context.Context, in audit.SourceInput) (audit.Source, error) {
	if in.Password != "" || in.SavedConnectionID == "" {
		return audit.Source{}, fmt.Errorf("%w: the key keeper opens only a saved link, with the password it stores", audit.ErrInvalidInput)
	}
	localItems, err := r.local.Sources(ctx)
	if err != nil {
		return audit.Source{}, err
	}
	r.mu.Lock()
	full := len(localItems)+len(r.held) >= audit.MaxSources
	r.mu.Unlock()
	if full {
		return audit.Source{}, errHeldLimit
	}
	var src audit.Source
	args := AuditAddArgs{
		Instance: r.instance, ConnectionLabel: in.ConnectionLabel, Label: in.Label, Database: in.Database, Collection: in.Collection,
		Connection: audit.ConnectionInput{URI: in.URI, Username: in.Username, AuthSource: in.AuthSource, TLSMode: in.TLSMode, CACertificate: in.CACertificate, SavedConnectionID: in.SavedConnectionID},
	}
	if err := r.client.Call(ctx, OpAuditAdd, args, &src); err != nil {
		return audit.Source{}, r.fromKeeper(err)
	}
	if src.ID == "" {
		return audit.Source{}, &WireError{Code: CodeInternal, Message: "The key keeper's answer was not understood."}
	}
	for _, s := range localItems {
		if src.SelectionKey != "" && s.SelectionKey == src.SelectionKey {
			// The same collection is already open in this Unravis (with a typed password): one copy is enough.
			_ = r.client.Call(ctx, OpAuditDelete, AuditIDArgs{Instance: r.instance, ID: src.ID}, nil)
			return audit.Source{}, errHeldDuplicate
		}
	}
	r.mu.Lock()
	r.held[src.ID] = src
	r.down, r.seated = false, true
	r.mu.Unlock()
	return src, nil
}

// AddSource opens a source in this Unravis (a typed password, or a link with none).
func (r *AuditRouter) AddSource(ctx context.Context, in audit.SourceInput) (audit.Source, error) {
	localItems, err := r.local.Sources(ctx)
	if err != nil {
		return audit.Source{}, err
	}
	r.mu.Lock()
	full := len(localItems)+len(r.held) >= audit.MaxSources
	r.mu.Unlock()
	if full {
		return audit.Source{}, errHeldLimit
	}
	src, err := r.local.AddSource(ctx, in)
	if err != nil {
		return audit.Source{}, err
	}
	r.mu.Lock()
	dup := false
	for _, h := range r.held {
		if src.SelectionKey != "" && h.SelectionKey == src.SelectionKey {
			dup = true
		}
	}
	r.mu.Unlock()
	if dup {
		_ = r.local.DeleteSource(ctx, src.ID)
		return audit.Source{}, errHeldDuplicate
	}
	return src, nil
}

// Sources lists both sides.
func (r *AuditRouter) Sources(ctx context.Context) ([]audit.Source, error) {
	items, _, err := r.SourcesWithKeeper(ctx)
	return items, err
}

// SourcesWithKeeper lists both sides and says, per keeper source, whether the keeper can read it now. When the keeper cannot be
// reached its sources are still listed, as stopped, so the person sees why they cannot be read.
func (r *AuditRouter) SourcesWithKeeper(ctx context.Context) ([]audit.Source, map[string]HeldState, error) {
	items, err := r.local.Sources(ctx)
	if err != nil {
		return nil, nil, err
	}
	r.mu.Lock()
	ask := len(r.held) > 0
	r.mu.Unlock()
	states := map[string]HeldState{}
	if ask {
		var theirs []audit.Source
		err := r.client.Call(ctx, OpAuditSources, AuditSeat{Instance: r.instance}, &theirs)
		r.mu.Lock()
		switch {
		case err == nil:
			now := map[string]bool{}
			for _, s := range theirs {
				now[s.ID] = true
			}
			for id := range r.held {
				if !now[id] {
					delete(r.held, id) // the keeper restarted, or closed an idle seat
				}
			}
			r.down = false
		case keeperUnreachable(err):
			r.down = true
		}
		state := HeldByKeeper
		if r.down {
			state = HeldButDown
		}
		for id, s := range r.held {
			items = append(items, s)
			states[id] = state
		}
		r.mu.Unlock()
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, states, nil
}

// DeleteSource removes a source on its side. A keeper that is not running has closed its sources already, so the source is only
// forgotten here.
func (r *AuditRouter) DeleteSource(ctx context.Context, id string) error {
	if !r.isHeld(id) {
		return r.local.DeleteSource(ctx, id)
	}
	err := r.client.Call(ctx, OpAuditDelete, AuditIDArgs{Instance: r.instance, ID: id}, nil)
	if err == nil || keeperUnreachable(err) {
		r.forget(id)
		return nil
	}
	err = r.fromKeeper(err)
	if errors.Is(err, audit.ErrNotFound) {
		r.forget(id)
		return nil
	}
	return err
}

/* ------------------------------------------------------------ reads */

func (r *AuditRouter) Analyze(ctx context.Context, q audit.Query) (audit.Analysis, error) {
	keeper, err := r.side(q.SourceIDs)
	if err != nil {
		return audit.Analysis{}, err
	}
	if !keeper {
		return r.local.Analyze(ctx, q)
	}
	var out audit.Analysis
	return out, r.read(ctx, OpAuditAnalyze, q, &out)
}

func (r *AuditRouter) Requests(ctx context.Context, in audit.PageInput) (audit.Page, error) {
	keeper, err := r.side(in.Query.SourceIDs)
	if err != nil {
		return audit.Page{}, err
	}
	if !keeper {
		return r.local.Requests(ctx, in)
	}
	var out audit.Page
	return out, r.read(ctx, OpAuditRequests, in, &out)
}

func (r *AuditRouter) Payload(ctx context.Context, in audit.PayloadInput) (audit.Payload, error) {
	if !r.isHeld(in.SourceID) {
		return r.local.Payload(ctx, in)
	}
	var out audit.Payload
	return out, r.read(ctx, OpAuditPayload, in, &out)
}

func (r *AuditRouter) Record(ctx context.Context, in audit.RecordInput) (audit.RecordDetail, error) {
	if !r.isHeld(in.SourceID) {
		return r.local.Record(ctx, in)
	}
	var out audit.RecordDetail
	return out, r.read(ctx, OpAuditRecord, in, &out)
}

var errLocalCannot = fmt.Errorf("%w: this repository cannot answer that read", audit.ErrPatternsUnavailable)

func (r *AuditRouter) Activity(ctx context.Context, in audit.ActivityInput) (audit.Page, error) {
	keeper, err := r.side(in.SourceIDs)
	if err != nil {
		return audit.Page{}, err
	}
	if !keeper {
		reader, ok := r.local.(audit.ActivityReader)
		if !ok {
			return audit.Page{}, errLocalCannot
		}
		return reader.Activity(ctx, in)
	}
	var out audit.Page
	return out, r.read(ctx, OpAuditActivity, in, &out)
}

func (r *AuditRouter) Breakdown(ctx context.Context, in audit.BreakdownInput) (audit.Breakdown, error) {
	keeper, err := r.side(in.Query.SourceIDs)
	if err != nil {
		return audit.Breakdown{}, err
	}
	if !keeper {
		reader, ok := r.local.(audit.BreakdownReader)
		if !ok {
			return audit.Breakdown{}, errLocalCannot
		}
		return reader.Breakdown(ctx, in)
	}
	var out audit.Breakdown
	return out, r.read(ctx, OpAuditBreakdown, in, &out)
}

func (r *AuditRouter) Coverage(ctx context.Context, in audit.CoverageInput) (audit.CoverageReport, error) {
	if !r.isHeld(in.SourceID) {
		prober, ok := r.local.(audit.CoverageProber)
		if !ok {
			return audit.CoverageReport{}, errLocalCannot
		}
		return prober.Coverage(ctx, in)
	}
	var out audit.CoverageReport
	return out, r.read(ctx, OpAuditCoverage, in, &out)
}

func (r *AuditRouter) Slowest(ctx context.Context, in audit.PageInput) (audit.Page, error) {
	keeper, err := r.side(in.Query.SourceIDs)
	if err != nil {
		return audit.Page{}, err
	}
	if !keeper {
		reader, ok := r.local.(audit.SlowestReader)
		if !ok {
			return audit.Page{}, errLocalCannot
		}
		return reader.Slowest(ctx, in)
	}
	var out audit.Page
	return out, r.read(ctx, OpAuditSlowest, in, &out)
}

func (r *AuditRouter) SampleSchema(ctx context.Context, sourceID string, n int) (audit.SchemaCensus, error) {
	if !r.isHeld(sourceID) {
		sampler, ok := r.local.(audit.SchemaSampler)
		if !ok {
			return audit.SchemaCensus{}, errLocalCannot
		}
		return sampler.SampleSchema(ctx, sourceID, n)
	}
	var out audit.SchemaCensus
	return out, r.read(ctx, OpAuditSchema, AuditSchemaInput{SourceID: sourceID, N: n}, &out)
}

// Rollup is the one read that merges both sides: the timeline of every open source. A keeper that cannot be reached leaves its
// sources out, each marked with why, and the local part is still answered.
func (r *AuditRouter) Rollup(ctx context.Context, plan audit.RollupPlan) (audit.RollupSlots, error) {
	var mine, theirs []string
	for _, id := range plan.SourceIDs {
		if r.isHeld(id) {
			theirs = append(theirs, id)
		} else {
			mine = append(mine, id)
		}
	}
	reader, canLocal := r.local.(audit.RollupReader)
	if len(theirs) == 0 {
		if !canLocal {
			return audit.RollupSlots{}, errLocalCannot
		}
		return reader.Rollup(ctx, plan)
	}
	var res AuditRollupResult
	err := r.read(ctx, OpAuditRollup, AuditRollupInput{SourceIDs: theirs, From: plan.From, To: plan.To, Zone: plan.Zone, SlowMs: plan.SlowMs}, &res)
	var held *audit.RollupBuilder
	switch {
	case err == nil:
		if held, err = audit.RollupBuilderFromWire(res.Rollup, plan.Width); err != nil {
			return audit.RollupSlots{}, err
		}
	case keeperUnreachable(err) && len(mine) > 0:
		held = audit.NewRollupBuilder(plan.Width)
		res = AuditRollupResult{}
		for _, id := range theirs {
			res.Sources = append(res.Sources, audit.SourceResult{SourceID: id, ErrorCode: "keeper_stopped", Error: "The Unravis Key Keeper service is not running, so this source cannot be read until it runs again."})
		}
	default:
		return audit.RollupSlots{}, err
	}
	if len(mine) == 0 {
		return audit.RollupSlots{Builder: held, Sources: res.Sources, Provenance: res.Provenance}, nil
	}
	if !canLocal {
		return audit.RollupSlots{}, errLocalCannot
	}
	localPlan := plan
	localPlan.SourceIDs = mine
	ls, err := reader.Rollup(ctx, localPlan)
	if err != nil {
		return audit.RollupSlots{}, err
	}
	merged := audit.NewRollupBuilder(plan.Width)
	if ls.Builder != nil {
		merged.Merge(ls.Builder)
	}
	merged.Merge(held)
	out := audit.RollupSlots{Builder: merged, Sources: append(ls.Sources, res.Sources...), Provenance: append(ls.Provenance, res.Provenance...)}
	order := map[string]int{}
	for i, id := range plan.SourceIDs {
		order[id] = i
	}
	sort.SliceStable(out.Sources, func(i, j int) bool { return order[out.Sources[i].SourceID] < order[out.Sources[j].SourceID] })
	return out, nil
}

/* ------------------------------------------------- local-only callers */

// TestConnection, ValidateConnectionSettings and OpenCommandClient work on settings the person typed, so they run here.
func (r *AuditRouter) TestConnection(ctx context.Context, in audit.ConnectionInput) (audit.ConnectionResult, error) {
	tester, ok := r.local.(audit.ConnectionTester)
	if !ok {
		return audit.ConnectionResult{}, errLocalCannot
	}
	return tester.TestConnection(ctx, in)
}

func (r *AuditRouter) ValidateConnectionSettings(in audit.ConnectionInput) error {
	validator, ok := r.local.(audit.ConnectionValidator)
	if !ok {
		return nil
	}
	return validator.ValidateConnectionSettings(in)
}

type commandOpener interface {
	OpenCommandClient(ctx context.Context, input audit.ConnectionInput) (store.MongoCommandFunc, func(), error)
}

func (r *AuditRouter) OpenCommandClient(ctx context.Context, in audit.ConnectionInput) (store.MongoCommandFunc, func(), error) {
	opener, ok := r.local.(commandOpener)
	if !ok {
		return nil, nil, errLocalCannot
	}
	return opener.OpenCommandClient(ctx, in)
}

// Close closes the keeper's seat for this Unravis, then the local repository.
func (r *AuditRouter) Close(ctx context.Context) error {
	r.mu.Lock()
	ask := r.seated
	r.held, r.seated = map[string]audit.Source{}, false
	r.mu.Unlock()
	if ask {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		_ = r.client.Call(cctx, OpAuditClose, AuditSeat{Instance: r.instance}, nil)
		cancel()
	}
	return r.local.Close(ctx)
}
