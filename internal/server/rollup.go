package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/audit"
)

// rollupRefused is the 409 body of a daily or hourly view over a window that
// reads every request on a live source.
const rollupRefused = "This view reads every request in the window, so on a live source it only runs for windows up to 24 hours. Choose a shorter window, run it on a restored copy, or start Unravis with --allow-broad-scans."

const (
	// rollupCacheTTL keeps a finished read for the other view of the same window
	// (the table and the grid share one aggregation) and for a quick repeat.
	rollupCacheTTL     = time.Minute
	rollupCacheEntries = 8
)

type rollupCacheEntry struct {
	key     [sha256.Size]byte
	at      time.Time
	slots   audit.RollupSlots
	planned audit.RollupPlan
}

// rollupCache holds the last few completed rollup reads. Only a read in which
// every source answered is kept, and it is dropped when a source is added or
// removed. The slots are never changed after the read, so they are shared. The
// zero value is ready to use; now is for tests.
type rollupCache struct {
	mu      sync.Mutex
	now     func() time.Time
	entries []rollupCacheEntry // newest first
}

func (c *rollupCache) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func rollupCacheKey(plan audit.RollupPlan) [sha256.Size]byte {
	return sha256.Sum256([]byte(fmt.Sprintf("%q|%d|%d|%s|%g", plan.SourceIDs, plan.From.UnixNano(), plan.To.UnixNano(), plan.Zone, plan.SlowMs)))
}

func (c *rollupCache) get(plan audit.RollupPlan) (audit.RollupSlots, bool) {
	key := rollupCacheKey(plan)
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.clock()
	kept := c.entries[:0]
	var found audit.RollupSlots
	hit := false
	for _, e := range c.entries {
		if now.Sub(e.at) >= rollupCacheTTL {
			continue
		}
		kept = append(kept, e)
		if e.key == key {
			found, hit = e.slots, true
		}
	}
	c.entries = kept
	return found, hit
}

func (c *rollupCache) put(plan audit.RollupPlan, slots audit.RollupSlots) {
	key := rollupCacheKey(plan)
	c.mu.Lock()
	defer c.mu.Unlock()
	entries := []rollupCacheEntry{{key: key, at: c.clock(), slots: slots}}
	for _, e := range c.entries {
		if e.key != key && len(entries) < rollupCacheEntries {
			entries = append(entries, e)
		}
	}
	c.entries = entries
}

func (c *rollupCache) clear() {
	c.mu.Lock()
	c.entries = nil
	c.mu.Unlock()
}

// rollupParams reads from, to, tz, slowMs and the sources of a GET request. The
// sources come from "sources" (comma separated) and repeated "source"
// parameters; with none, every source the repository lists is used.
func (h *handler) rollupParams(w http.ResponseWriter, r *http.Request) (audit.RollupPlan, bool) {
	query := r.URL.Query()
	from, errFrom := time.Parse(time.RFC3339, query.Get("from"))
	to, errTo := time.Parse(time.RFC3339, query.Get("to"))
	if errFrom != nil || errTo != nil {
		writeError(w, http.StatusBadRequest, "from and to must be RFC 3339 timestamps.")
		return audit.RollupPlan{}, false
	}
	in := audit.RollupInput{From: from, To: to, TimeZone: query.Get("tz")}
	if raw := query.Get("slowMs"); raw != "" {
		slow, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "slowMs must be a number.")
			return audit.RollupPlan{}, false
		}
		in.SlowMs = slow
	}
	for _, value := range append(query["source"], strings.Split(query.Get("sources"), ",")...) {
		if value = strings.TrimSpace(value); value != "" {
			in.SourceIDs = append(in.SourceIDs, value)
		}
	}
	if len(in.SourceIDs) == 0 {
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		items, err := h.repo.Sources(ctx)
		if err != nil {
			writeRepoError(w, err)
			return audit.RollupPlan{}, false
		}
		for _, item := range items {
			in.SourceIDs = append(in.SourceIDs, item.ID)
		}
	}
	plan, err := audit.PlanRollup(in)
	if err != nil {
		writeRepoError(w, err)
		return audit.RollupPlan{}, false
	}
	return plan, true
}

// rollupSlots returns the slots of a plan from the cache or the repository.
func (h *handler) rollupSlots(w http.ResponseWriter, r *http.Request, plan audit.RollupPlan) (audit.RollupSlots, bool) {
	reader, ok := h.repo.(audit.RollupReader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "The daily and hourly view is unavailable for this repository.")
		return audit.RollupSlots{}, false
	}
	if slots, hit := h.rollups.get(plan); hit {
		return slots, true
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	slots, err := reader.Rollup(ctx, plan)
	if err != nil {
		switch {
		case errors.Is(err, audit.ErrBroadScanRefused):
			writeJSON(w, http.StatusConflict, struct {
				Error string `json:"error"`
				Code  string `json:"code"`
			}{rollupRefused, "broad_scan_refused"}, 4096)
		case errors.Is(err, audit.ErrRollupGroupLimit):
			writeError(w, http.StatusRequestEntityTooLarge, "This window has too many hourly groups in one source. Choose a shorter window.")
		case errors.Is(err, audit.ErrDocsExaminedBudget):
			writeError(w, http.StatusRequestEntityTooLarge, "The collection holds more documents than the docs-examined budget allows, so the daily view did not run. Raise --docs-examined-budget or run it on a restored copy.")
		case errors.Is(err, audit.ErrCollectionSizeCheck):
			writeError(w, http.StatusBadGateway, "The daily view could not read the collection size, so it did not run. Check that the source is reachable and that this account can read collection statistics.")
		default:
			writeRepoError(w, err)
		}
		return audit.RollupSlots{}, false
	}
	allOK := len(slots.Sources) > 0
	for _, s := range slots.Sources {
		allOK = allOK && s.OK
	}
	if allOK {
		h.rollups.put(plan, slots)
	}
	return slots, true
}

// rollupView is the part of both responses that names the window and its zone.
type rollupView struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Zone is the zone the days and hours follow (an IANA name; UTC when none was asked).
	Zone       string               `json:"zone"`
	SlowMs     float64              `json:"slowMs"`
	Sources    []audit.SourceResult `json:"sources"`
	Provenance []audit.Provenance   `json:"provenance"`
}

func newRollupView(plan audit.RollupPlan, slots audit.RollupSlots) rollupView {
	view := rollupView{From: plan.From, To: plan.To, Zone: plan.Zone, SlowMs: plan.SlowMs, Sources: slots.Sources, Provenance: slots.Provenance}
	if view.Sources == nil {
		view.Sources = []audit.SourceResult{}
	}
	if view.Provenance == nil {
		view.Provenance = []audit.Provenance{}
	}
	return view
}

func anySourceOK(slots audit.RollupSlots) bool {
	for _, s := range slots.Sources {
		if s.OK {
			return true
		}
	}
	return false
}

// dailyResponse is the body of GET /api/daily. With no source answering, Days is
// empty: zero counts would pass for a quiet system.
type dailyResponse struct {
	rollupView
	LowTrafficBelow int               `json:"lowTrafficBelow"`
	FlatPercent     int               `json:"flatPercent"`
	Days            []audit.RollupDay `json:"days"`
}

// hourGridResponse is the body of GET /api/hourgrid.
type hourGridResponse struct {
	rollupView
	NoiseBelow int `json:"noiseBelow"`
	audit.HourGrid
}

// daily serves GET /api/daily?from=&to=&tz=[&sources=][&slowMs=].
func (h *handler) daily(w http.ResponseWriter, r *http.Request) {
	plan, ok := h.rollupParams(w, r)
	if !ok {
		return
	}
	slots, ok := h.rollupSlots(w, r, plan)
	if !ok {
		return
	}
	out := dailyResponse{rollupView: newRollupView(plan, slots), LowTrafficBelow: audit.RollupLowTraffic, FlatPercent: audit.RollupFlatPercent, Days: []audit.RollupDay{}}
	if anySourceOK(slots) {
		days, err := audit.BuildDaily(plan, slots.Builder)
		if err != nil {
			writeRepoError(w, err)
			return
		}
		out.Days = days
	}
	writeJSON(w, http.StatusOK, out, maxResponseBytes)
}

// hourGrid serves GET /api/hourgrid?from=&to=&tz=[&sources=][&slowMs=].
func (h *handler) hourGrid(w http.ResponseWriter, r *http.Request) {
	plan, ok := h.rollupParams(w, r)
	if !ok {
		return
	}
	slots, ok := h.rollupSlots(w, r, plan)
	if !ok {
		return
	}
	out := hourGridResponse{rollupView: newRollupView(plan, slots), NoiseBelow: audit.RollupNoiseCount, HourGrid: audit.HourGrid{Days: []audit.HourGridDay{}, Cells: []audit.HourCell{}}}
	if anySourceOK(slots) {
		grid, err := audit.BuildHourGrid(plan, slots.Builder)
		if err != nil {
			writeRepoError(w, err)
			return
		}
		out.HourGrid = grid
	}
	writeJSON(w, http.StatusOK, out, maxResponseBytes)
}
