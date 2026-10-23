package audit

import (
	"context"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Breakdown dimensions. Each groups the requests a query selects by one audit
// evidence field.
const (
	BreakdownStatusCode  = "statusCode"
	BreakdownUserID      = "userId"
	BreakdownUIVersion   = "uiVersion"
	BreakdownUserAgent   = "userAgent"
	BreakdownUserIP      = "userIp"
	BreakdownPollingType = "pollingType"
)

const (
	DefaultBreakdownTop = 20
	MaxBreakdownTop     = 50
	// MaxBreakdownGroups bounds the distinct values one source may return. It
	// fails closed: a partial value list would make the Other row wrong.
	MaxBreakdownGroups = 50000
	// BreakdownMissingLabel is what a blank or absent value is called. The audit
	// writer fills some columns after the request, so a blank usually means "not
	// written yet", not "none".
	BreakdownMissingLabel = "(not yet written)"
	// BreakdownOtherLabel names the row that sums every value outside the top N.
	BreakdownOtherLabel = "Other"
)

// ErrBreakdownGroupLimit is returned (wrapped in ErrLimit) when a dimension has
// more distinct values than MaxBreakdownGroups.
var ErrBreakdownGroupLimit = errors.New("breakdown group limit exceeded")

var breakdownDims = map[string]bool{
	BreakdownStatusCode: true, BreakdownUserID: true, BreakdownUIVersion: true,
	BreakdownUserAgent: true, BreakdownUserIP: true, BreakdownPollingType: true,
}

// BreakdownInput selects a dimension within the rows a query matches. Top is the
// number of values to list before Other; zero means DefaultBreakdownTop.
type BreakdownInput struct {
	Query Query  `json:"query"`
	Dim   string `json:"dim"`
	Top   int    `json:"top"`
}

// TopN returns the effective number of listed values.
func (in BreakdownInput) TopN() int {
	if in.Top == 0 {
		return DefaultBreakdownTop
	}
	return in.Top
}

// BreakdownRow is one value of the dimension. Count covers every selected row
// with that value, whatever its state. Incomplete rows (including a queued
// asynchronous request) never reach Stats: latency percentiles come from valid,
// completed rows only, and Stats.Count says how many that is. Complete is the
// rows that are not incomplete. Pending is a subset of Incomplete. 4xx and 5xx
// stay separate.
//
// Filter selects exactly these rows when applied to the same query. Other has
// no Filter: it is a remainder, not a value, and Values counts the distinct
// values it folds together.
type BreakdownRow struct {
	Value      string        `json:"value"`
	Missing    bool          `json:"missing"`
	Count      int64         `json:"count"`
	Complete   int64         `json:"complete"`
	Incomplete int64         `json:"incomplete"`
	Pending    int64         `json:"pending"`
	Count4xx   int64         `json:"count4xx"`
	Count5xx   int64         `json:"count5xx"`
	Stats      Stats         `json:"stats"`
	Filter     *RecordFilter `json:"filter,omitempty"`
	Values     int           `json:"values,omitempty"`
}

// Breakdown is the top rows of one dimension plus the remainder. Total is the
// number of rows the query selected, so Rows plus Other always add up to it.
type Breakdown struct {
	Dim      string         `json:"dim"`
	Rows     []BreakdownRow `json:"rows"`
	Other    *BreakdownRow  `json:"other"`
	Total    int64          `json:"total"`
	Distinct int            `json:"distinct"`
	Sources  []SourceResult `json:"sources"`
}

// BreakdownReader is an optional Repository capability, like SlowestReader.
type BreakdownReader interface {
	Breakdown(ctx context.Context, input BreakdownInput) (Breakdown, error)
}

// ValidateBreakdown checks the query, the dimension and the row count.
func ValidateBreakdown(in BreakdownInput) error {
	if err := ValidateQuery(in.Query); err != nil {
		return err
	}
	if !breakdownDims[in.Dim] {
		return invalidInputf("dim must be one of statusCode, userId, uiVersion, userAgent, userIp, pollingType")
	}
	if in.Top < 0 || in.Top > MaxBreakdownTop {
		return invalidInputf("top must be between 0 and %d", MaxBreakdownTop)
	}
	return nil
}

// BreakdownKey is the value of dim on one row, as text. An empty key means the
// field is missing or blank. Status and polling type are trimmed, as the
// RecordFilter compares them; a user id is its decimal digits.
func BreakdownKey(dim string, r RequestRecord) string {
	switch dim {
	case BreakdownStatusCode:
		return strings.TrimSpace(r.Status)
	case BreakdownPollingType:
		return strings.TrimSpace(r.PollingTypeRaw)
	case BreakdownUserID:
		if r.UserID == nil {
			return ""
		}
		return strconv.Itoa(*r.UserID)
	case BreakdownUIVersion:
		return r.UIVersion
	case BreakdownUserAgent:
		return r.UserAgent
	case BreakdownUserIP:
		return r.UserIP
	}
	return ""
}

// BreakdownFilter is the RecordFilter that selects exactly the rows whose
// BreakdownKey for dim is key.
func BreakdownFilter(dim, key string) *RecordFilter {
	f := &RecordFilter{}
	switch dim {
	case BreakdownStatusCode:
		f.StatusCodes = []string{key}
	case BreakdownPollingType:
		f.PollingTypes = []string{key}
	case BreakdownUIVersion:
		f.UIVersions = []string{key}
	case BreakdownUserAgent:
		f.UserAgents = []string{key}
	case BreakdownUserIP:
		f.UserIPs = []string{key}
	case BreakdownUserID:
		if key == "" {
			f.UserIDMissing = true
		} else if id, err := strconv.Atoi(key); err == nil {
			f.UserIDs = []int{id}
		}
	}
	return f
}

// BreakdownCounts are the per-value counters. Incomplete includes Pending.
type BreakdownCounts struct {
	Count, Incomplete, Pending, Count4xx, Count5xx int64
}

func (c *BreakdownCounts) merge(o BreakdownCounts) {
	c.Count += o.Count
	c.Incomplete += o.Incomplete
	c.Pending += o.Pending
	c.Count4xx += o.Count4xx
	c.Count5xx += o.Count5xx
}

type breakdownBin struct {
	count         int64
	sum, min, max float64
}

type breakdownGroup struct {
	key    string
	counts BreakdownCounts
	bins   map[int]*breakdownBin
}

// BreakdownBuilder merges per-value counters and histogram bins from any number
// of sources and builds the top-N rows. Callers feed it either whole records
// (AddRecord, for scans done in Go) or aggregated counters and bins (AddCounts,
// AddBin, AddOtherBin, for server-side aggregation).
type BreakdownBuilder struct {
	dim       string
	top       int
	groups    map[string]*breakdownGroup
	otherBins map[int]*breakdownBin
	pinned    map[string]bool
}

// NewBreakdownBuilder returns a builder for dim listing top values (zero means
// DefaultBreakdownTop).
func NewBreakdownBuilder(dim string, top int) *BreakdownBuilder {
	if top == 0 {
		top = DefaultBreakdownTop
	}
	return &BreakdownBuilder{dim: dim, top: top, groups: map[string]*breakdownGroup{}, otherBins: map[int]*breakdownBin{}}
}

func (b *BreakdownBuilder) group(key string) *breakdownGroup {
	g := b.groups[key]
	if g == nil {
		g = &breakdownGroup{key: key, bins: map[int]*breakdownBin{}}
		b.groups[key] = g
	}
	return g
}

// Distinct is the number of distinct values seen so far.
func (b *BreakdownBuilder) Distinct() int { return len(b.groups) }

// AddCounts merges counters for one value.
func (b *BreakdownBuilder) AddCounts(key string, c BreakdownCounts) {
	b.group(key).counts.merge(c)
}

func addBin(bins map[int]*breakdownBin, bin int, count int64, sum, min, max float64) {
	cur := bins[bin]
	if cur == nil {
		bins[bin] = &breakdownBin{count: count, sum: sum, min: min, max: max}
		return
	}
	cur.count += count
	cur.sum += sum
	cur.min = math.Min(cur.min, min)
	cur.max = math.Max(cur.max, max)
}

// AddBin merges one histogram bin of valid rows for one value.
func (b *BreakdownBuilder) AddBin(key string, bin int, count int64, sum, min, max float64) {
	addBin(b.group(key).bins, bin, count, sum, min, max)
}

// AddOtherBin merges one histogram bin of valid rows whose value lies outside
// the listed top values. Server-side callers use it because they cannot afford
// one bin set per distinct value.
func (b *BreakdownBuilder) AddOtherBin(bin int, count int64, sum, min, max float64) {
	addBin(b.otherBins, bin, count, sum, min, max)
}

// AddRecord counts one request row. state is "valid", "invalid" or "incomplete";
// durationMs feeds the histogram only for a valid row.
func (b *BreakdownBuilder) AddRecord(r RequestRecord, state string, pending bool, class string, durationMs *float64) {
	g := b.group(BreakdownKey(b.dim, r))
	g.counts.Count++
	if state == "incomplete" {
		g.counts.Incomplete++
	}
	if pending {
		g.counts.Pending++
	}
	switch class {
	case StatusClientError:
		g.counts.Count4xx++
	case StatusServerError:
		g.counts.Count5xx++
	}
	if state == "valid" && durationMs != nil {
		if bin := BinIndex(*durationMs); bin >= 0 {
			addBin(g.bins, bin, 1, *durationMs, *durationMs, *durationMs)
		}
	}
}

// Merge folds another builder's counters and bins into b.
func (b *BreakdownBuilder) Merge(o *BreakdownBuilder) {
	for key, g := range o.groups {
		dst := b.group(key)
		dst.counts.merge(g.counts)
		for bin, s := range g.bins {
			addBin(dst.bins, bin, s.count, s.sum, s.min, s.max)
		}
	}
	for bin, s := range o.otherBins {
		addBin(b.otherBins, bin, s.count, s.sum, s.min, s.max)
	}
}

// Pin fixes the listed rows to keys. A server-side caller reads histogram bins
// only for the top values it saw first, so a later change of ranking (a source
// dropped after its counters arrived) must not promote a value that has no bins.
func (b *BreakdownBuilder) Pin(keys []string) {
	b.pinned = make(map[string]bool, len(keys))
	for _, key := range keys {
		b.pinned[key] = true
	}
}

// listed returns the groups that become rows, most frequent first, and the rest.
func (b *BreakdownBuilder) listed() (top, rest []*breakdownGroup) {
	for _, g := range b.ranked() {
		if len(top) < b.top && (b.pinned == nil || b.pinned[g.key]) {
			top = append(top, g)
		} else {
			rest = append(rest, g)
		}
	}
	return top, rest
}

func (b *BreakdownBuilder) ranked() []*breakdownGroup {
	list := make([]*breakdownGroup, 0, len(b.groups))
	for _, g := range b.groups {
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool {
		a, c := list[i], list[j]
		if a.counts.Count != c.counts.Count {
			return a.counts.Count > c.counts.Count
		}
		if (a.key == "") != (c.key == "") {
			return a.key == ""
		}
		if b.dim == BreakdownUserID {
			x, _ := strconv.Atoi(a.key)
			y, _ := strconv.Atoi(c.key)
			return x < y
		}
		return a.key < c.key
	})
	return list
}

// TopKeys lists the values Build will report as rows, most frequent first.
func (b *BreakdownBuilder) TopKeys() []string {
	top, _ := b.listed()
	keys := make([]string, len(top))
	for i, g := range top {
		keys[i] = g.key
	}
	return keys
}

func statsOf(bins ...map[int]*breakdownBin) (Stats, error) {
	acc := newHistogramAccumulator()
	for _, set := range bins {
		order := make([]int, 0, len(set))
		for bin := range set {
			order = append(order, bin)
		}
		sort.Ints(order)
		for _, bin := range order {
			s := set[bin]
			if err := acc.add(bin, s.count, s.sum, s.min, s.max); err != nil {
				return Stats{}, err
			}
		}
	}
	stats := acc.stats()
	// A row carries percentiles, not the full histogram, so 50 rows stay small.
	stats.Histogram = []BinCount{}
	return stats, nil
}

func rowOf(c BreakdownCounts) BreakdownRow {
	return BreakdownRow{Count: c.Count, Complete: c.Count - c.Incomplete, Incomplete: c.Incomplete, Pending: c.Pending, Count4xx: c.Count4xx, Count5xx: c.Count5xx}
}

// Build returns the top rows, the remainder and the total.
func (b *BreakdownBuilder) Build() (Breakdown, error) {
	top, others := b.listed()
	out := Breakdown{Dim: b.dim, Rows: []BreakdownRow{}, Distinct: len(top) + len(others), Sources: []SourceResult{}}
	var rest BreakdownCounts
	restBins := []map[int]*breakdownBin{b.otherBins}
	for _, g := range others {
		out.Total += g.counts.Count
		rest.merge(g.counts)
		restBins = append(restBins, g.bins)
	}
	for _, g := range top {
		out.Total += g.counts.Count
		row := rowOf(g.counts)
		row.Value, row.Missing = g.key, g.key == ""
		if row.Missing {
			row.Value = BreakdownMissingLabel
		}
		row.Filter = BreakdownFilter(b.dim, g.key)
		stats, err := statsOf(g.bins)
		if err != nil {
			return Breakdown{}, err
		}
		row.Stats = stats
		out.Rows = append(out.Rows, row)
	}
	if len(others) > 0 {
		other := rowOf(rest)
		other.Value, other.Values = BreakdownOtherLabel, len(others)
		stats, err := statsOf(restBins...)
		if err != nil {
			return Breakdown{}, err
		}
		other.Stats = stats
		out.Other = &other
	}
	return out, nil
}
