package importer

// Defaults for the Limits fields an importer leaves at zero.
const (
	DefaultMaxLineBytes      = 64 << 10
	DefaultMaxFields         = 4096
	DefaultMaxRejectFraction = 0.05
)

// Limits are the caps a file must stay under. Over MaxBytes or MaxRows the
// whole file is refused; so it is over MaxLineBytes or MaxFields. Above
// MaxRejectFraction of rows rejected, the file is quarantined.
type Limits struct {
	MaxBytes, MaxRows int64   // over either: whole file refused
	MaxLineBytes      int     // default 64 KiB
	MaxFields         int     // default 4096
	MaxRejectFraction float64 // default 0.05; above it: quarantine
	// MaxEvents caps the events one file may emit (0: the importer's own
	// default, none if it has none; negative: no limit). Importers that emit
	// many events per row (wide counter files) enforce it before emitting, so a
	// file under MaxBytes and MaxRows cannot still produce an unbounded run.
	MaxEvents int64
}

// CheckEvents returns *LimitError (Limit "events") when n, the number of events
// emitted so far plus the ones about to be, passes MaxEvents. MaxEvents <= 0
// means no limit.
func (l Limits) CheckEvents(n int64) error {
	if l.MaxEvents > 0 && n > l.MaxEvents {
		return &LimitError{Limit: "events", Max: l.MaxEvents, At: n}
	}
	return nil
}

// NewLimits builds Limits with the three defaults filled in.
func NewLimits(maxBytes, maxRows int64) Limits {
	return Limits{MaxBytes: maxBytes, MaxRows: maxRows}.WithDefaults()
}

// WithDefaults fills MaxLineBytes, MaxFields and MaxRejectFraction when they
// are zero or negative. For zero tolerance of rejects pass a tiny positive
// fraction, since 0 means "default".
func (l Limits) WithDefaults() Limits {
	if l.MaxLineBytes <= 0 {
		l.MaxLineBytes = DefaultMaxLineBytes
	}
	if l.MaxFields <= 0 {
		l.MaxFields = DefaultMaxFields
	}
	if l.MaxRejectFraction <= 0 {
		l.MaxRejectFraction = DefaultMaxRejectFraction
	}
	return l
}

// CheckRows returns *LimitError when rows (the count so far) passes MaxRows.
// MaxRows <= 0 means no row limit.
func (l Limits) CheckRows(rows int64) error {
	if l.MaxRows > 0 && rows > l.MaxRows {
		return &LimitError{Limit: "rows", Max: l.MaxRows, At: rows}
	}
	return nil
}

// CheckRejects returns a QuarantineError when more than MaxRejectFraction of
// the rows were rejected. Call it once at the end of the parse.
func (l Limits) CheckRejects(rows, rejected int64) error {
	l = l.WithDefaults()
	if rows > 0 && float64(rejected)/float64(rows) > l.MaxRejectFraction {
		return &QuarantineError{Reason: ReasonRejectFraction}
	}
	return nil
}
