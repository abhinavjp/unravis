// backoff.go implements the C7 SQL failure back-off state machine with a
// persisted auto-off that service restarts cannot clear (owner-only re-enable).
package bbsql

import (
	"encoding/json"
	"errors"
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

// OnSuccess recovers back-off one step toward the base interval and resets
// the counters. It never clears AutoOff: auto-off is owner-only.
func (s *State) OnSuccess() {
	if s.AutoOff {
		return
	}
	s.Failures = 0
	s.SlowStrikes = 0
	s.Interval /= 2
	if s.Interval < BaseInterval {
		s.Interval = BaseInterval
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
	readFile  = os.ReadFile
	writeFile = func(path string, data []byte) error {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		return os.WriteFile(path, data, 0o600)
	}
)

// Save persists s as JSON (fixed fields only; no error text).
func Save(path string, s State) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return writeFile(path, data)
}

// Load reads the persisted state. A persisted AutoOff STAYS auto-off across
// restarts. Unreadable or corrupt state fails closed (auto-off) rather than
// silently re-enabling. A missing file means first start: on at base.
func Load(path string) (State, error) {
	closed := State{Interval: BaseInterval, AutoOff: true, State: StateAutoOff}
	data, err := readFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return NewState(), nil
	}
	if err != nil {
		return closed, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return closed, err
	}
	s.normalize()
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