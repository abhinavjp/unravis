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
		return l, auditBroken(problem)
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
	Seq      int    `json:"seq"` // the number the outcome line will get
	Snapshot string `json:"snapshot"`
	Detail   string `json:"detail"`
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
		if fi.Mode().Type() != os.ModeDir { // a link or junction is not a plain folder
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
	return m.change(op, func(cur Config, dir string) (Config, error) {
		_, snap, err := latestSnapshot(dir)
		if err != nil {
			return snap, &ApplyError{Reason: ReasonNothingToRollBack, Message: "There is no earlier change to go back to."}
		}
		// Only collectors and counters.extra come back; the port, bind address, folder and the rest stay as they are now.
		c, terr := tunables(cur, snap)
		if terr != nil {
			return cur, refused("The earlier settings no longer fit the settings now in force.")
		}
		return c, nil
	})
}

// staleLock is how long a lock file may sit before its owner counts as dead. The longest honest run is about three
// minutes (apply and restart, the one-minute watch, then a roll-back with its own restart), and the owner refreshes the
// lock before the watch.
const staleLock = 6 * time.Minute

const lockFileName = "lock"

// acquireChangeLock takes the one lock of the change folder (created exclusively). A lock older than staleLock belongs
// to a process that died and is taken over. The returned func releases it.
func acquireChangeLock(dir string) (release func(), err error) {
	path := filepath.Join(dir, lockFileName)
	for try := 0; try < 2; try++ {
		f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // program folder
		if e == nil {
			_, _ = f.WriteString(strconv.Itoa(os.Getpid()))
			_ = f.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(e, os.ErrExist) {
			return nil, applySaveError(e)
		}
		if fi, serr := os.Lstat(path); serr == nil && time.Since(fi.ModTime()) > staleLock {
			_ = os.Remove(path)
			continue
		}
		break
	}
	return nil, refused("Another change is running. Try again in a few minutes.")
}

func touchLock(dir string) {
	now := time.Now()
	_ = os.Chtimes(filepath.Join(dir, lockFileName), now, now)
}

// tunables returns cur with only the settings a change may touch (collectors and counters.extra) taken from from. A
// roll-back or a recovery never brings back anything else: an administrator may have changed the port, the bind
// address or the folder since, and that must stay.
func tunables(cur, from Config) (Config, error) {
	d1, err := cur.Marshal()
	if err != nil {
		return cur, err
	}
	out, err := ParseConfig(d1)
	if err != nil {
		return cur, err
	}
	d2, err := from.Marshal()
	if err != nil {
		return cur, err
	}
	src, err := ParseConfig(d2)
	if err != nil {
		return cur, err
	}
	out.Collectors, out.Counters = src.Collectors, src.Counters
	return out, out.Validate()
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
	release, err := acquireChangeLock(dir)
	if err != nil {
		return res, err
	}
	defer release()
	// Holding the lock means no other change is running: a marker that is still here belongs to one that died.
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
		// Nothing to do, and nothing is written: a loop of no-ops must not fill the audit file.
		res.Config = cur
		return res, refused("That is already how it is set. Nothing was changed.")
	}
	// The snapshot of "before" is named for the line the outcome will get, so a roll-back finds the newest.
	lines, problem, rerr := ReadAudit(dir)
	if rerr != nil {
		return res, applySaveError(rerr)
	}
	if problem != "" {
		return res, auditBroken(problem)
	}
	seq := len(lines) + 1
	snap, err := writeSnapshot(dir, seq, cur)
	if err != nil {
		return res, applySaveError(err)
	}
	if err := writePending(dir, pendingMark{Seq: seq, Snapshot: snap, Detail: line.Detail}); err != nil {
		_ = os.Remove(filepath.Join(dir, snap))
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
		touchLock(dir)
		if herr := m.watchHealthy(want); herr != nil {
			touchLock(dir)
			// Not healthy: the previous settings go back by themselves, and the recorder is started again if it is down.
			_, rb := m.ApplyConfig(cur)
			running := rb == nil && m.recoverService()
			if rb != nil {
				// The roll-back did not take. The snapshot and the marker stay, so the next change command tries again.
				line.Result = "rollback-failed"
			} else {
				_ = os.Remove(filepath.Join(dir, snap)) // it equals the settings now in force: nothing to go back to
				line.Result, line.After = "rolled-back", before
			}
			l, aerr := appendAudit(dir, line)
			if rb == nil {
				_ = os.Remove(filepath.Join(dir, pendingFileName))
			}
			if aerr != nil {
				return res, aerr
			}
			res.Line, res.Config = l, cur
			return res, unhealthyError(rb == nil, running)
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

func auditBroken(problem string) error {
	return &ApplyError{Reason: ReasonAuditBroken, Message: "The change history does not verify (" + problem + "). Nothing was changed. An administrator must look at " + changeDirName + " in the program folder."}
}

func unhealthyError(restored, running bool) error {
	msg := "The recorder was not healthy a minute after the change, so the previous settings were put back."
	switch {
	case !restored:
		msg = "The recorder was not healthy a minute after the change and the previous settings could not be put back. Check the Blackbox now."
	case !running:
		msg = "The recorder was not healthy a minute after the change. The previous settings were put back, but the recorder is stopped. Press Start on the Blackbox, or check Services in Windows."
	}
	return &ApplyError{Reason: ReasonUnhealthy, Message: msg, Running: running}
}

func writePending(dir string, p pendingMark) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, pendingFileName), b, 0o600)
}

// recoverPending finishes a change whose process died (the caller holds the lock, so no other change is running): the
// settings of the snapshot (collectors and counters.extra only) are applied again and one line is written. When the
// audit already has the line, the change did finish and only the marker is removed.
func (m *Manager) recoverPending(_ string, dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, pendingFileName)) //nolint:gosec // program folder
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return applySaveError(err)
	}
	remove := func() { _ = os.Remove(filepath.Join(dir, pendingFileName)) }
	fail := func(why string) error {
		_, aerr := appendAudit(dir, AuditLine{Who: "local-admin", Verb: "recover", Detail: "an unfinished change could not be undone (" + why + ")", Result: "recover-failed"})
		remove()
		return aerr
	}
	var p pendingMark
	if json.Unmarshal(b, &p) != nil || p.Seq < 1 || p.Snapshot != snapName(p.Seq) {
		return fail("the marker is not valid")
	}
	detail := oneLine(p.Detail)
	if len(detail) > 120 {
		detail = detail[:120]
	}
	lines, problem, rerr := ReadAudit(dir)
	if rerr != nil {
		return applySaveError(rerr)
	}
	if problem != "" {
		return auditBroken(problem)
	}
	if len(lines) >= p.Seq && lines[p.Seq-1].Result != "rollback-failed" {
		remove() // the change finished and wrote its line; only the marker was left
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, p.Snapshot)) //nolint:gosec // program folder
	if err != nil {
		return fail("its snapshot is missing")
	}
	snap, err := ParseConfig(data)
	if err != nil {
		return fail("its snapshot is not valid")
	}
	installed := installedPaths(filepath.Dir(dir)).ConfigPath
	cur, err := LoadConfig(installed)
	if err != nil {
		return err
	}
	old, err := tunables(cur, snap)
	if err != nil {
		return fail("the snapshot does not fit the settings now in force")
	}
	if _, err := m.ApplyConfig(old); err != nil {
		return err // the marker stays; the next command tries again
	}
	_ = os.Remove(filepath.Join(dir, p.Snapshot)) // it equals the settings now in force
	_, aerr := appendAudit(dir, AuditLine{Who: "local-admin", Verb: "recover", Detail: detail + " (the process that made it did not finish)", After: hashConfig(old), Result: "recovered"})
	remove()
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
		return ConfigHistory{}, errors.New("The change history could not be read. Run this as an administrator.")
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
