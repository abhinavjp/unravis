package blackbox

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// B5: small, checked settings changes with a history, an undo, an automatic roll-back and a hash-chained audit file.
//
// The only changes that exist are the verbs in ChangeOp. A change is applied through the same path as `apply`
// (Manager.ApplyConfig: checked, saved through the locked-file path, restarted, undone when the restart fails), and is
// then watched: the recorder must be running and answer on its status port, three polls in a row, within a minute, or
// the previous settings are put back by themselves. Everything is written in the program folder, which only
// administrators can change. There is no listener and no way to run anything: a verb names a collector id or a counter
// path that the normal settings checks accept.
//
// Limits, stated plainly: the watch runs in the process that made the change. If that process dies in the middle of
// the minute, a pending marker is left; the next config command finishes the roll-back (recoverPending). The audit
// chain proves that lines were not changed or removed in the middle; it cannot prove that the newest lines were not cut
// off, because anyone who can write to the program folder can also write the file (administrators only).

// Change verbs.
const (
	OpCollector     = "collector"      // switch a collector on or off, or change how often it runs
	OpCounterAdd    = "counter-add"    // add a PDH path to counters.extra
	OpCounterRemove = "counter-remove" // take one out
	OpRollback      = "rollback"       // put the previous settings back (written by RollbackConfig)
)

// Reasons added by B5.
const (
	// ReasonUnhealthy: the new settings were saved but the recorder was not healthy within the minute, so the previous
	// settings were put back.
	ReasonUnhealthy Reason = "unhealthy-rolled-back"
	// ReasonNothingToRollBack: there is no earlier settings snapshot.
	ReasonNothingToRollBack Reason = "nothing-to-roll-back"
	// ReasonChangeRefused: the change is not allowed (unknown collector, bad path, always-on collector ...).
	ReasonChangeRefused Reason = "change-refused"
	// ReasonAuditBroken: the audit chain does not verify, so nothing is changed until an administrator looks at it.
	ReasonAuditBroken Reason = "audit-broken"
)

const (
	changeDirName   = "config-changes"
	auditFileName   = "audit.jsonl"
	pendingFileName = "pending.json"
	historyKeep     = 20
	healthWindow    = 60 * time.Second
	healthStep      = 5 * time.Second
	healthStable    = 3 // polls in a row
	auditMaxLine    = 4096
	auditMaxLines   = 100000
)

// changeNow is the clock of the audit file. A variable so tests are exact.
var changeNow = time.Now

// ChangeOp is one settings change. Zero fields mean "leave as is".
type ChangeOp struct {
	Verb     string
	ID       string // collector id
	On       *bool
	EverySec *int // 0 = every sample
	Path     string
}

// Describe is the fixed one-line text of the change, safe for the audit file (ids and checked paths only).
func (op ChangeOp) Describe() string {
	switch op.Verb {
	case OpCollector:
		var parts []string
		if op.On != nil {
			if *op.On {
				parts = append(parts, "on")
			} else {
				parts = append(parts, "off")
			}
		}
		if op.EverySec != nil {
			parts = append(parts, "every "+strconv.Itoa(*op.EverySec)+" s")
		}
		return "collector " + op.ID + " " + strings.Join(parts, ", ")
	case OpCounterAdd:
		return "counters.extra add " + op.Path
	case OpCounterRemove:
		return "counters.extra remove " + op.Path
	case OpRollback:
		return "rollback"
	}
	return "unknown"
}

func refused(msg string) error { return &ApplyError{Reason: ReasonChangeRefused, Message: msg} }

// Apply returns cfg with the change made. cfg itself is not touched. The result is checked by Validate, so every rule
// of the settings file (known ids, always-on collectors, allowed intervals, counter path form) holds.
func (op ChangeOp) Apply(cfg Config) (Config, error) {
	data, err := cfg.Marshal()
	if err != nil {
		return cfg, refused("The settings could not be read.")
	}
	out, err := ParseConfig(data) // a deep copy
	if err != nil {
		return cfg, refused("The settings could not be read.")
	}
	switch op.Verb {
	case OpCollector:
		if _, ok := LookupCollector(op.ID); !ok {
			return cfg, refused("That collector id is not known. See `unravis-blackbox config show`.")
		}
		if op.On == nil && op.EverySec == nil {
			return cfg, refused("Say on or off, or how often (--every).")
		}
		set := out.Collectors[op.ID]
		if op.On != nil {
			v := *op.On
			set.On = &v
		}
		if op.EverySec != nil {
			set.EverySec = *op.EverySec
		}
		if set.On != nil && *set.On && set.EverySec == 0 {
			set.On = nil // the default is on
		}
		if out.Collectors == nil {
			out.Collectors = map[string]CollectorSetting{}
		}
		if set.On == nil && set.EverySec == 0 {
			delete(out.Collectors, op.ID)
		} else {
			out.Collectors[op.ID] = set
		}
	case OpCounterAdd, OpCounterRemove:
		if out.Counters == nil {
			out.Counters = &CounterSettings{}
		}
		var next []string
		found := false
		for _, p := range out.Counters.Extra {
			if strings.EqualFold(p, op.Path) {
				found = true
				if op.Verb == OpCounterRemove {
					continue
				}
			}
			next = append(next, p)
		}
		switch {
		case op.Verb == OpCounterAdd && found:
			return cfg, refused("That counter is already in the list.")
		case op.Verb == OpCounterRemove && !found:
			return cfg, refused("That counter is not in the list.")
		case op.Verb == OpCounterAdd:
			next = append(next, op.Path)
		}
		out.Counters.Extra = next
		if len(next) == 0 {
			out.Counters = nil
		}
	default:
		return cfg, refused("That change is not one the Blackbox makes.")
	}
	if err := out.Validate(); err != nil {
		return cfg, refused("The settings would not be valid: " + oneLine(err.Error()))
	}
	return out, nil
}

func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// ---- audit chain ----

// AuditLine is one line of audit.jsonl. Hash covers Prev and every other field.
type AuditLine struct {
	Seq    int       `json:"seq"`
	At     time.Time `json:"at"`
	Who    string    `json:"who"`
	Verb   string    `json:"verb"`
	Detail string    `json:"detail"`
	Before string    `json:"before"` // SHA-256 of the settings before
	After  string    `json:"after"`  // SHA-256 of the settings after (equals Before when nothing changed)
	Result string    `json:"result"` // applied, rolled-back, rollback, refused, recovered
	Prev   string    `json:"prev"`
	Hash   string    `json:"hash"`
}

func (l AuditLine) computeHash() string {
	c := l
	c.Hash = ""
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ReadAudit returns the audit lines and the first problem in the chain ("" when it verifies).
func ReadAudit(dir string) (lines []AuditLine, problem string, err error) {
	f, err := os.Open(filepath.Join(dir, auditFileName)) //nolint:gosec // the program folder, administrators only
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), auditMaxLine)
	prev := ""
	for sc.Scan() {
		if len(lines) >= auditMaxLines {
			return lines, "the audit file is too long", nil
		}
		var l AuditLine
		dec := json.NewDecoder(strings.NewReader(sc.Text()))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&l); err != nil {
			return lines, fmt.Sprintf("line %d is not valid", len(lines)+1), nil
		}
		switch {
		case l.Seq != len(lines)+1:
			return lines, fmt.Sprintf("line %d has the wrong number", len(lines)+1), nil
		case l.Prev != prev:
			return lines, fmt.Sprintf("line %d does not follow line %d", l.Seq, l.Seq-1), nil
		case l.Hash != l.computeHash():
			return lines, fmt.Sprintf("line %d was changed", l.Seq), nil
		}
		prev = l.Hash
		lines = append(lines, l)
	}
	if err := sc.Err(); err != nil {
		return lines, "the audit file could not be read to the end", nil
	}
	return lines, "", nil
}

var auditMu sync.Mutex

func appendAudit(dir string, l AuditLine) (AuditLine, error) {
	auditMu.Lock()
	defer auditMu.Unlock()
	lines, problem, err := ReadAudit(dir)
	if err != nil {
		return l, err
	}
	if problem != "" {
		return l, &ApplyError{Reason: ReasonAuditBroken, Message: "The change history does not verify (" + problem + "). Nothing was changed. An administrator must look at " + changeDirName + " in the program folder."}
	}
	l.Seq = len(lines) + 1
	if len(lines) > 0 {
		l.Prev = lines[len(lines)-1].Hash
	}
	l.At = changeNow().UTC().Truncate(time.Second)
	l.Hash = l.computeHash()
	b, err := json.Marshal(l)
	if err != nil {
		return l, err
	}
	f, err := os.OpenFile(filepath.Join(dir, auditFileName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // program folder
	if err != nil {
		return l, err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return l, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return l, err
	}
	return l, f.Close()
}

// ---- snapshots and the pending marker ----

func snapName(seq int) string { return fmt.Sprintf("snap-%06d.json", seq) }

type pendingMark struct {
	Seq      int       `json:"seq"` // the number the outcome line will get
	Snapshot string    `json:"snapshot"`
	Deadline time.Time `json:"deadline"`
	Detail   string    `json:"detail"`
}

func hashConfig(c Config) string {
	b, _ := c.Marshal()
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (m *Manager) changeDir() (progDir, dir string, err error) {
	progDir, err = m.Env.ProgramDir()
	if err != nil {
		return "", "", applySaveError(err)
	}
	return progDir, filepath.Join(progDir, changeDirName), nil
}

// prepareChangeDir makes the folder, locked to administrators and SYSTEM.
func (m *Manager) prepareChangeDir(dir string) error {
	if fi, err := os.Lstat(dir); err == nil {
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return applySaveError(errors.New("change folder is not a folder"))
		}
	} else if err := os.Mkdir(dir, 0o700); err != nil {
		return applySaveError(err)
	}
	if err := m.Env.SecureDir(dir, false); err != nil {
		return applySaveError(err)
	}
	return nil
}

func writeSnapshot(dir string, seq int, c Config) (string, error) {
	data, err := c.Marshal()
	if err != nil {
		return "", err
	}
	name := snapName(seq)
	if err := writeFileAtomic(filepath.Join(dir, name), data, 0o600); err != nil {
		return "", err
	}
	return name, nil
}

// pruneSnapshots keeps the newest historyKeep snapshots.
func pruneSnapshots(dir string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "snap-") && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > historyKeep {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}

func latestSnapshot(dir string) (name string, cfg Config, err error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", cfg, err
	}
	var names []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), "snap-") && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return "", cfg, os.ErrNotExist
	}
	sort.Strings(names)
	name = names[len(names)-1]
	data, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // program folder
	if err != nil {
		return "", cfg, err
	}
	cfg, err = ParseConfig(data)
	return name, cfg, err
}

// ---- health ----

// watchHealthy returns nil once the recorder is running and answers on its status port healthStable polls in a row,
// within healthWindow. A stopped recorder is not watched (the new settings apply at its next start).
func (m *Manager) watchHealthy(cfg Config) error {
	ap, _ := m.Env.(AliveProber)
	good := 0
	for waited := time.Duration(0); waited <= healthWindow; waited += healthStep {
		ok := false
		if st, err := m.Svc.State(ServiceName); err == nil && st == SvcRunning {
			ok = true
			if ap != nil {
				ok = ap.ProbeAlive(ProbeHost(cfg.Bind), cfg.Port) == nil
			}
		}
		if ok {
			if good++; good >= healthStable {
				return nil
			}
		} else {
			good = 0
		}
		m.Env.Sleep(healthStep)
	}
	return errors.New("the recorder was not healthy in time")
}

// ---- the operations ----

// ChangeResult is a finished change.
type ChangeResult struct {
	Line      AuditLine
	Restarted bool
	Config    Config
}

// ChangeConfig makes one change to the installed settings: checked, applied, watched, and rolled back by itself when the
// recorder is not healthy within a minute. It needs administrator rights and an installed recorder.
func (m *Manager) ChangeConfig(op ChangeOp) (ChangeResult, error) {
	return m.change(op, func(cur Config, _ string) (Config, error) { return op.Apply(cur) })
}

// RollbackConfig puts the settings from before the last change back, with the same watch. It is itself a change, so
// running it twice goes back and forth.
func (m *Manager) RollbackConfig() (ChangeResult, error) {
	op := ChangeOp{Verb: OpRollback}
	return m.change(op, func(_ Config, dir string) (Config, error) {
		_, c, err := latestSnapshot(dir)
		if err != nil {
			return c, &ApplyError{Reason: ReasonNothingToRollBack, Message: "There is no earlier change to go back to."}
		}
		return c, nil
	})
}

func (m *Manager) change(op ChangeOp, next func(cur Config, dir string) (Config, error)) (res ChangeResult, err error) {
	if !m.Env.IsAdmin() {
		return res, ErrNotAdmin
	}
	st, err := m.Svc.State(ServiceName)
	if err != nil {
		return res, err
	}
	if st == SvcNotInstalled {
		return res, ErrNotInstalled
	}
	progDir, dir, err := m.changeDir()
	if err != nil {
		return res, err
	}
	if err := m.prepareChangeDir(dir); err != nil {
		return res, err
	}
	if err := m.recoverPending(progDir, dir); err != nil {
		return res, err
	}
	installed := installedPaths(progDir).ConfigPath
	cur, err := LoadConfig(installed)
	if err != nil {
		return res, err
	}
	want, err := next(cur, dir)
	if err != nil {
		return res, err
	}
	before, after := hashConfig(cur), hashConfig(want)
	line := AuditLine{Who: "local-admin", Verb: op.Verb, Detail: op.Describe(), Before: before, After: after}
	if before == after {
		line.Result = "refused"
		line.Detail += " (no change)"
		l, aerr := appendAudit(dir, line)
		if aerr != nil {
			return res, aerr
		}
		res.Line, res.Config = l, cur
		return res, refused("That is already how it is set. Nothing was changed.")
	}
	// The snapshot of "before" is named for the line the outcome will get, so a roll-back finds the newest.
	lines, problem, rerr := ReadAudit(dir)
	if rerr != nil {
		return res, applySaveError(rerr)
	}
	if problem != "" {
		return res, &ApplyError{Reason: ReasonAuditBroken, Message: "The change history does not verify (" + problem + "). Nothing was changed. An administrator must look at " + changeDirName + " in the program folder."}
	}
	seq := len(lines) + 1
	snap, err := writeSnapshot(dir, seq, cur)
	if err != nil {
		return res, applySaveError(err)
	}
	mark := pendingMark{Seq: seq, Snapshot: snap, Deadline: changeNow().Add(healthWindow + 30*time.Second), Detail: line.Detail}
	if err := writePending(dir, mark); err != nil {
		return res, applySaveError(err)
	}
	ar, aerr := m.ApplyConfig(want)
	if aerr != nil {
		// ApplyConfig already put the previous settings back.
		_ = os.Remove(filepath.Join(dir, pendingFileName))
		_ = os.Remove(filepath.Join(dir, snap))
		line.Result, line.After = "refused", before
		line.Detail += " (not applied)"
		if _, e := appendAudit(dir, line); e != nil {
			return res, e
		}
		return res, aerr
	}
	res.Restarted, res.Config = ar.Restarted, want
	line.Result = "applied"
	if op.Verb == OpRollback {
		line.Result = "rollback"
	}
	if ar.Restarted {
		if herr := m.watchHealthy(want); herr != nil {
			// Not healthy: the previous settings go back by themselves.
			_, rb := m.ApplyConfig(cur)
			_ = os.Remove(filepath.Join(dir, snap)) // the snapshot equals the settings now in force: nothing to go back to
			line.Result, line.After = "rolled-back", before
			l, aerr := appendAudit(dir, line)
			_ = os.Remove(filepath.Join(dir, pendingFileName))
			if aerr != nil {
				return res, aerr
			}
			res.Line, res.Config = l, cur
			return res, unhealthyError(rb == nil)
		}
	}
	l, aerr := appendAudit(dir, line)
	_ = os.Remove(filepath.Join(dir, pendingFileName))
	pruneSnapshots(dir)
	if aerr != nil {
		return res, aerr
	}
	res.Line = l
	return res, nil
}

func unhealthyError(restored bool) error {
	msg := "The recorder was not healthy a minute after the change, so the previous settings were put back."
	if !restored {
		msg = "The recorder was not healthy a minute after the change and the previous settings could not be put back. Check the Blackbox now."
	}
	return &ApplyError{Reason: ReasonUnhealthy, Message: msg}
}

func writePending(dir string, p pendingMark) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, pendingFileName), b, 0o600)
}

// recoverPending finishes a change whose watching process died: when the marker is older than its deadline, the
// snapshot is applied and one "recovered" line is written. A marker still inside its deadline means another change is
// running, which is refused.
func (m *Manager) recoverPending(_ string, dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, pendingFileName)) //nolint:gosec // program folder
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return applySaveError(err)
	}
	var p pendingMark
	if json.Unmarshal(b, &p) != nil || p.Snapshot != snapName(p.Seq) {
		_ = os.Remove(filepath.Join(dir, pendingFileName))
		return nil
	}
	if changeNow().Before(p.Deadline) {
		return refused("Another change is still being checked. Try again in a minute.")
	}
	data, err := os.ReadFile(filepath.Join(dir, p.Snapshot)) //nolint:gosec // program folder
	if err != nil {
		_ = os.Remove(filepath.Join(dir, pendingFileName))
		return nil
	}
	old, err := ParseConfig(data)
	if err != nil {
		_ = os.Remove(filepath.Join(dir, pendingFileName))
		return nil
	}
	if _, err := m.ApplyConfig(old); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(dir, p.Snapshot)) // it equals the settings now in force
	_, aerr := appendAudit(dir, AuditLine{Who: "local-admin", Verb: "recover", Detail: p.Detail + " (the process that made it did not finish)", Before: "", After: hashConfig(old), Result: "recovered"})
	_ = os.Remove(filepath.Join(dir, pendingFileName))
	return aerr
}

// ConfigHistory is what `config history` shows: the newest changes (at most historyKeep) and whether the chain verifies.
type ConfigHistory struct {
	Lines    []AuditLine `json:"lines"`
	Verified bool        `json:"verified"`
	Problem  string      `json:"problem,omitempty"`
	Pending  bool        `json:"pending"`
}

// History reads the audit file. It needs no administrator rights.
func (m *Manager) History() (ConfigHistory, error) {
	_, dir, err := m.changeDir()
	if err != nil {
		return ConfigHistory{}, err
	}
	lines, problem, err := ReadAudit(dir)
	if err != nil {
		return ConfigHistory{}, applySaveError(err)
	}
	h := ConfigHistory{Verified: problem == "", Problem: problem}
	if len(lines) > historyKeep {
		lines = lines[len(lines)-historyKeep:]
	}
	h.Lines = lines
	if _, err := os.Stat(filepath.Join(dir, pendingFileName)); err == nil {
		h.Pending = true
	}
	return h, nil
}
