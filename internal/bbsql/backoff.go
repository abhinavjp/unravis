// backoff.go implements the C7 SQL failure back-off state machine with a
// persisted auto-off that service restarts cannot clear (owner-only re-enable).
package bbsql

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Fixed intervals and thresholds (C7).
const (
	BaseInterval    = 15 * time.Second
	MaxInterval     = 15 * time.Minute
	DemotedInterval = 60 * time.Second
	AutoOffFailures = 10
	SlowStrikeLimit = 3
)

// Fixed states: the only strings ever persisted. No error text, instance
// names, or driver messages are stored.
const (
	StateOn         = "on"
	StateBackingOff = "backing-off"
	StateAutoOff    = "auto-off"
	StateDenied     = "denied"
)

// State is the persisted back-off state (fixed fields only).
type State struct {
	Interval    time.Duration `json:"interval"`
	Failures    int           `json:"failures"`
	AutoOff     bool          `json:"autoOff"`
	SlowStrikes int           `json:"slowStrikes"`
	State       string        `json:"state"`
}

// NewState returns the initial on state at the base interval.
func NewState() State { return State{Interval: BaseInterval, State: StateOn} }

// OnSuccess recovers back-off one step toward the base interval and clears the
// failure count. It never clears AutoOff: auto-off is owner-only. It also never
// steps below the slow floor ONCE the three-strike threshold has demoted the
// 15 s items: a single lucky success must not undo that demotion on its own.
// The floor applies only at SlowStrikeLimit, not for any nonzero strike (W7-06):
// one or two slow samples below the threshold must not impose the 60 s floor.
// SlowStrikes is not reset here either - only a genuinely fast sample (OnSlow)
// calms the instance, and once it does, recovery proceeds back down to
// BaseInterval. Instance failure back-off (Failures/Interval doubling) is kept
// separate from this slow-demotion floor.
func (s *State) OnSuccess() {
	if s.AutoOff {
		return
	}
	s.Failures = 0
	floor := BaseInterval
	if s.SlowStrikes >= SlowStrikeLimit {
		floor = DemotedInterval
	}
	s.Interval /= 2
	if s.Interval < floor {
		s.Interval = floor
	}
	s.State = StateOn
}

// OnFailure doubles the interval (15 s base, 15 min cap). Ten failures in a
// row, any permission error, or a C8 ceiling failure latches AutoOff; the
// state is denied for permission/ceiling, auto-off otherwise.
func (s *State) OnFailure(permission, ceiling bool) {
	s.Failures++
	if s.Interval < BaseInterval {
		s.Interval = BaseInterval
	}
	s.Interval *= 2
	if s.Interval > MaxInterval {
		s.Interval = MaxInterval
	}
	s.State = StateBackingOff
	if s.Failures >= AutoOffFailures || permission || ceiling {
		s.AutoOff = true
		s.State = StateAutoOff
		if permission || ceiling {
			s.State = StateDenied
		}
	}
}

// OnSlow counts queries using over half their time limit; three consecutive
// strikes demote 15 s items to 60 s (never lowering a longer interval).
func (s *State) OnSlow(used, limit time.Duration) {
	if used > limit/2 {
		s.SlowStrikes++
	} else {
		s.SlowStrikes = 0
	}
	if s.SlowStrikes >= SlowStrikeLimit && s.Interval < DemotedInterval {
		s.Interval = DemotedInterval
	}
}

// OwnerEnable is the ONLY path that clears AutoOff (explicit owner action).
func OwnerEnable(s *State) { *s = NewState() }

// StatePath is the state file inside the SQL service's DACL-protected folder.
func StatePath(folder string) string { return filepath.Join(folder, "sql", "state.json") }

// Persistence seam: injectable so tests need no real disk.
var (
	readFile = os.ReadFile
	// writeFile replaces the state atomically: a temp file in the same folder,
	// flushed, then renamed. An in-place overwrite could leave a torn state file
	// after a power loss, which Load would then refuse - turning a healthy
	// service permanently auto-off (W10-04).
	writeFile = func(path string, data []byte) error {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(dir, ".state-*.tmp")
		if err != nil {
			return err
		}
		name := tmp.Name()
		defer os.Remove(name)
		if _, err := tmp.Write(data); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Sync(); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		return os.Rename(name, path)
	}
)

// errStateCorrupt is the fixed reason for a state file that cannot be trusted.
// It carries no path and no parser text (C10).
var errStateCorrupt = errors.New("sql state is not a complete, consistent record")

// persistedState mirrors State with pointers so a MISSING field is detectable:
// json.Unmarshal into State would silently default it, and a missing autoOff
// would re-enable a service that had latched itself off (W7-04).
type persistedState struct {
	Interval    *int64  `json:"interval"`
	Failures    *int    `json:"failures"`
	AutoOff     *bool   `json:"autoOff"`
	SlowStrikes *int    `json:"slowStrikes"`
	State       *string `json:"state"`
}

// parseState accepts only a complete, consistent, single JSON record. null, {},
// an unknown key, trailing data, a missing field, a negative count, an unknown
// state label, or a latch that disagrees with its label are all refused.
func parseState(data []byte) (State, error) {
	var p persistedState
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return State{}, errStateCorrupt
	}
	if err := dec.Decode(new(json.RawMessage)); err != io.EOF {
		return State{}, errStateCorrupt
	}
	if p.Interval == nil || p.Failures == nil || p.AutoOff == nil || p.SlowStrikes == nil || p.State == nil {
		return State{}, errStateCorrupt
	}
	s := State{
		Interval:    time.Duration(*p.Interval),
		Failures:    *p.Failures,
		AutoOff:     *p.AutoOff,
		SlowStrikes: *p.SlowStrikes,
		State:       *p.State,
	}
	if s.Interval <= 0 || s.Failures < 0 || s.SlowStrikes < 0 {
		return State{}, errStateCorrupt
	}
	switch s.State {
	case StateOn, StateBackingOff, StateAutoOff, StateDenied:
	default:
		return State{}, errStateCorrupt
	}
	// The latch and its label must agree, and a reached failure threshold must
	// already be latched: neither may be relabelled into an enabled state.
	if s.AutoOff != (s.State == StateAutoOff || s.State == StateDenied) {
		return State{}, errStateCorrupt
	}
	if !s.AutoOff && s.Failures >= AutoOffFailures {
		return State{}, errStateCorrupt
	}
	s.normalize()
	return s, nil
}

// Save persists s as JSON (fixed fields only; no error text).
func Save(path string, s State) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return writeFile(path, data)
}

// Load reads the persisted state. A persisted AutoOff STAYS auto-off across
// restarts. Unreadable, incomplete or inconsistent state fails closed (auto-off)
// rather than silently re-enabling. A missing file means first start: on at base.
func Load(path string) (State, error) {
	closed := State{Interval: BaseInterval, AutoOff: true, State: StateAutoOff}
	data, err := readFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return NewState(), nil
	}
	if err != nil {
		return closed, err
	}
	s, err := parseState(data)
	if err != nil {
		return closed, err
	}
	return s, nil
}

// normalize clamps values and keeps the state string within the fixed set;
// a persisted AutoOff can never be relabeled into an enabled state.
func (s *State) normalize() {
	if s.Interval < BaseInterval {
		s.Interval = BaseInterval
	}
	if s.Interval > MaxInterval {
		s.Interval = MaxInterval
	}
	if s.Failures < 0 {
		s.Failures = 0
	}
	if s.SlowStrikes < 0 {
		s.SlowStrikes = 0
	}
	if s.AutoOff {
		if s.State != StateDenied {
			s.State = StateAutoOff
		}
		return
	}
	switch s.State {
	case StateOn, StateBackingOff:
	default:
		if s.Interval > BaseInterval {
			s.State = StateBackingOff
		} else {
			s.State = StateOn
		}
	}
}
