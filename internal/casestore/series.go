package casestore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Series: per-bucket min, mean, max and count over a time window, computed by
// streaming the matching events through Case.Each (plan section 4.1). The
// aggregation lives here, over the Case interface, so every store gets the same
// answer and a test can drive it from a generator of millions of events.

// Bounds of one series query.
const (
	MaxSeriesBuckets = 2000
	MaxSeriesCount   = 50
	maxSelectorTerms = 8
	maxSelectorAttrs = 4
	maxSelectorValue = 64
	maxSelectorBytes = 512
)

// Series error reasons. They are the reason codes the API returns.
const (
	SeriesBadTime        = "bad-time"
	SeriesBadBucket      = "bad-bucket"
	SeriesTooManyBuckets = "too-many-buckets"
	SeriesBadSelector    = "bad-selector"
	SeriesTooManySeries  = "too-many-series"
)

// SeriesError is a refused series query. Reason is one of the Series* codes and
// Message is a fixed sentence (numbers only; never a caller-supplied string).
type SeriesError struct {
	Reason  string
	Message string
}

func (e *SeriesError) Error() string { return e.Reason + ": " + e.Message }

func seriesErr(reason, format string, a ...any) error {
	return &SeriesError{Reason: reason, Message: fmt.Sprintf(format, a...)}
}

// SeriesQuery asks for the per-bucket statistics of one or more selections.
// From and To are inclusive. BucketMs is the bucket width in milliseconds.
// Sel holds one selector per series, in the order the answer returns them.
//
// A selector is ";"-separated name=value terms:
//
//	kind=<kind>            required
//	source=<importer>      optional
//	host=<label>           optional
//	attr.<name>=<value>    optional, up to 4, exact match on an event attribute
//	field=value|duration   which number to aggregate; default value
//
// An event without the chosen number is skipped.
type SeriesQuery struct {
	From, To time.Time
	BucketMs int64
	Sel      []string

	// MaxScan and MaxScanTime lower the scan budget (see EventQuery).
	MaxScan     int64
	MaxScanTime time.Duration
}

// SeriesPoint is one non-empty bucket. T is the bucket start in Unix
// milliseconds.
type SeriesPoint struct {
	T     int64   `json:"t"`
	Min   float64 `json:"min"`
	Mean  float64 `json:"mean"`
	Max   float64 `json:"max"`
	Count int64   `json:"count"`
}

// SeriesOut is the answer for one selector.
type SeriesOut struct {
	Sel       string        `json:"sel"`
	Unit      string        `json:"unit"`      // the unit of the events, "" when they carry none
	UnitMixed bool          `json:"unitMixed"` // events of this selection carried more than one unit
	Count     int64         `json:"count"`     // events counted in the buckets
	Points    []SeriesPoint `json:"points"`
}

// SeriesResult is the answer. Partial is true when the scan budget stopped the
// scan before every matching event was read: the numbers cover only the events
// read (Scanned).
type SeriesResult struct {
	From     time.Time   `json:"from"`
	To       time.Time   `json:"to"`
	BucketMs int64       `json:"bucketMs"`
	Buckets  int         `json:"buckets"`
	Partial  bool        `json:"partial"`
	Scanned  int64       `json:"scanned"`
	Series   []SeriesOut `json:"series"`
}

type selector struct {
	kind, source, host string
	attrs              [][2]string
	duration           bool
}

var (
	selectorAttrName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,39}$`)
	selectorKindName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)
)

func selectorValueOK(v string) bool {
	if v == "" || len(v) > maxSelectorValue || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return false
		}
	}
	return true
}

// parseSelector checks one selector string. Errors are SeriesBadSelector and
// never repeat the input.
func parseSelector(s string) (selector, error) {
	var sel selector
	bad := func(why string) (selector, error) {
		return selector{}, seriesErr(SeriesBadSelector, "A series selector is not valid: %s.", why)
	}
	if s == "" || len(s) > maxSelectorBytes {
		return bad("empty or too long")
	}
	terms := strings.Split(s, ";")
	if len(terms) > maxSelectorTerms {
		return bad("too many terms")
	}
	seen := map[string]bool{}
	for _, t := range terms {
		name, value, ok := strings.Cut(t, "=")
		if !ok || !selectorValueOK(value) {
			return bad("each term is name=value")
		}
		if seen[name] {
			return bad("a term is repeated")
		}
		seen[name] = true
		switch {
		case name == "kind":
			if !selectorKindName.MatchString(value) {
				return bad("kind")
			}
			sel.kind = value
		case name == "source":
			sel.source = value
		case name == "host":
			sel.host = value
		case name == "field":
			switch value {
			case "value":
			case "duration":
				sel.duration = true
			default:
				return bad("field is value or duration")
			}
		case strings.HasPrefix(name, "attr."):
			an := strings.TrimPrefix(name, "attr.")
			if !selectorAttrName.MatchString(an) {
				return bad("attribute name")
			}
			if len(sel.attrs) >= maxSelectorAttrs {
				return bad("too many attributes")
			}
			sel.attrs = append(sel.attrs, [2]string{an, value})
		default:
			return bad("unknown term")
		}
	}
	if sel.kind == "" {
		return bad("kind is required")
	}
	return sel, nil
}

func (s *selector) matches(ev *evidence.Event) bool {
	if s.source != "" && ev.Source != s.source {
		return false
	}
	if s.host != "" && ev.Host != s.host {
		return false
	}
	for _, a := range s.attrs {
		if v, ok := ev.Attrs[a[0]]; !ok || v != a[1] {
			return false
		}
	}
	return true
}

type seriesAgg struct {
	count    []int64
	sum      []float64
	min, max []float64
	unit     string
	unitSet  bool
	mixed    bool
	total    int64
}

// Series aggregates the events c holds for q. It checks the query first (every
// refusal is a *SeriesError), then makes one Each pass for all selectors. The
// result is Partial when the scan budget stopped the pass; any other Each
// error is returned as is.
func Series(ctx context.Context, c Case, q SeriesQuery) (SeriesResult, error) {
	var res SeriesResult
	if q.From.IsZero() || q.To.IsZero() || q.To.Before(q.From) {
		return res, seriesErr(SeriesBadTime, "Give a from and a to time, with to not before from.")
	}
	fromMs, toMs := q.From.UnixMilli(), q.To.UnixMilli()
	span := toMs - fromMs
	if span < 0 || span > int64(200*365*24*time.Hour/time.Millisecond) {
		return res, seriesErr(SeriesBadTime, "The time range is too wide.")
	}
	if q.BucketMs < 1 {
		return res, seriesErr(SeriesBadBucket, "The bucket must be a whole number of milliseconds, 1 or more.")
	}
	nb := span/q.BucketMs + 1
	if nb > MaxSeriesBuckets {
		least := span/MaxSeriesBuckets + 1
		return res, seriesErr(SeriesTooManyBuckets, "That range and bucket give %d buckets; the most is %d. Use a bucket of at least %d ms.", nb, MaxSeriesBuckets, least)
	}
	if len(q.Sel) == 0 {
		return res, seriesErr(SeriesBadSelector, "Give at least one series selector.")
	}
	if len(q.Sel) > MaxSeriesCount {
		return res, seriesErr(SeriesTooManySeries, "At most %d series per request.", MaxSeriesCount)
	}
	sels := make([]selector, len(q.Sel))
	byKind := map[string][]int{}
	kinds := make([]string, 0, len(q.Sel))
	allSource, allHost := true, true
	sources, hosts := map[string]bool{}, map[string]bool{}
	for i, s := range q.Sel {
		p, err := parseSelector(s)
		if err != nil {
			return res, err
		}
		sels[i] = p
		if _, ok := byKind[p.kind]; !ok {
			kinds = append(kinds, p.kind)
		}
		byKind[p.kind] = append(byKind[p.kind], i)
		if p.source == "" {
			allSource = false
		}
		sources[p.source] = true
		if p.host == "" {
			allHost = false
		}
		hosts[p.host] = true
	}
	eq := EventQuery{
		Window: importer.Window{From: q.From.UTC(), To: q.To.UTC()},
		Kinds:  kinds, MaxScan: q.MaxScan, MaxScanTime: q.MaxScanTime,
	}
	if allSource {
		eq.Sources = keys(sources)
	}
	if allHost {
		eq.Hosts = keys(hosts)
	}

	aggs := make([]seriesAgg, len(sels))
	var scanned int64
	err := c.Each(ctx, eq, func(ev evidence.Event) error {
		scanned++
		idxs := byKind[ev.Kind]
		if len(idxs) == 0 {
			return nil
		}
		ts := ev.Start.UnixMilli()
		if ts < fromMs || ts > toMs {
			return nil
		}
		b := int((ts - fromMs) / q.BucketMs)
		for _, i := range idxs {
			sel := &sels[i]
			if !sel.matches(&ev) {
				continue
			}
			p := ev.Value
			if sel.duration {
				p = ev.DurationMs
			}
			if p == nil || math.IsNaN(*p) || math.IsInf(*p, 0) {
				continue
			}
			a := &aggs[i]
			if a.count == nil {
				a.count = make([]int64, nb)
				a.sum = make([]float64, nb)
				a.min = make([]float64, nb)
				a.max = make([]float64, nb)
			}
			v := *p
			if a.count[b] == 0 {
				a.min[b], a.max[b] = v, v
			} else {
				if v < a.min[b] {
					a.min[b] = v
				}
				if v > a.max[b] {
					a.max[b] = v
				}
			}
			a.count[b]++
			a.sum[b] += v
			a.total++
			if !a.unitSet {
				a.unit, a.unitSet = ev.Unit, ev.Unit != ""
			} else if ev.Unit != "" && ev.Unit != a.unit {
				a.mixed = true
			}
		}
		return nil
	})
	partial := false
	switch {
	case err == nil:
	case errors.Is(err, ErrScanBudget):
		partial = true
	default:
		return res, err
	}

	res = SeriesResult{
		From: q.From.UTC(), To: q.To.UTC(), BucketMs: q.BucketMs, Buckets: int(nb),
		Partial: partial, Scanned: scanned, Series: make([]SeriesOut, len(sels)),
	}
	for i := range sels {
		a := &aggs[i]
		out := SeriesOut{Sel: q.Sel[i], Unit: a.unit, UnitMixed: a.mixed, Count: a.total, Points: []SeriesPoint{}}
		for b := range a.count {
			if a.count[b] == 0 {
				continue
			}
			out.Points = append(out.Points, SeriesPoint{
				T: fromMs + int64(b)*q.BucketMs, Min: a.min[b], Max: a.max[b],
				Mean: a.sum[b] / float64(a.count[b]), Count: a.count[b],
			})
		}
		res.Series[i] = out
	}
	return res, nil
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
