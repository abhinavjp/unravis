package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/blackbox"
)

// A Blackbox world that lives in memory: recordings, places that answer, a list of
// saved Blackboxes and a fake "this PC". The demo (--demo) uses it so the Blackbox
// pages can be shown and tried without a server, a service or a file, and the
// tests use it for the same reason. Nothing here touches the disk, the network, a
// service or the registry. The read keys in it are made-up demo text.

/* ----------------------------------------------------------- a recording */

// simBB is one Blackbox as a place answers it.
type simBB struct {
	man        blackbox.Manifest
	serverTime time.Time // what a URL says its clock is
	status     *blackbox.StatusDoc
	files      map[string][]byte
	key        string
	down       error
	// dropAt makes a file stop after that many bytes with a read error, the way a
	// share drops part way through a read.
	dropAt map[string]int
}

type simHandle struct {
	bb  *simBB
	url bool
}

func (s *simHandle) Manifest(ctx context.Context) (blackbox.ManifestRead, error) {
	if s.bb.down != nil {
		return blackbox.ManifestRead{}, s.bb.down
	}
	mr := blackbox.ManifestRead{Manifest: s.bb.man}
	if s.url {
		mr.ServerTime = s.bb.serverTime
	}
	return mr, nil
}

func (s *simHandle) Status(ctx context.Context) (*blackbox.StatusDoc, error) {
	if s.bb.down != nil {
		return nil, s.bb.down
	}
	if !s.url {
		return nil, nil
	}
	return s.bb.status, nil
}

func (s *simHandle) OpenFile(ctx context.Context, name string) (io.ReadCloser, error) {
	if s.bb.down != nil {
		return nil, s.bb.down
	}
	data, ok := s.bb.files[name]
	if !ok {
		return nil, &blackbox.ReadError{Kind: blackbox.ReadNotFound, Message: "The recording " + name + " is not there any more."}
	}
	if n, drop := s.bb.dropAt[name]; drop && n < len(data) {
		return io.NopCloser(&droppingReader{r: bytes.NewReader(data[:max(n, 0)])}), nil
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// droppingReader reads what it has and then fails like a connection that was cut.
type droppingReader struct{ r *bytes.Reader }

func (d *droppingReader) Read(p []byte) (int, error) {
	n, err := d.r.Read(p)
	if err == io.EOF {
		return n, errors.New("the network name is no longer available")
	}
	return n, err
}

// failSource fails every read with one error.
type failSource struct{ err error }

func (f failSource) Manifest(context.Context) (blackbox.ManifestRead, error) {
	return blackbox.ManifestRead{}, f.err
}
func (f failSource) Status(context.Context) (*blackbox.StatusDoc, error) { return nil, f.err }
func (f failSource) OpenFile(context.Context, string) (io.ReadCloser, error) {
	return nil, f.err
}

// simRec describes a recording to build.
type simRec struct {
	Name     string
	Host     string
	Interval int // seconds, 15 when zero
	Start    time.Time
	End      time.Time
	// Skip lists the stretches with no sample (a gap in the recording).
	Skip [][2]time.Time
	// CPU gives the host CPU percent at a time (25 when nil).
	CPU func(time.Time) float64
	// Busy names the process that shows up on top while CPU is over 80.
	Busy string
	// Now is the recorder's own clock when it is read; ClockSkew is added to it.
	Now       time.Time
	ClockSkew time.Duration
	// Stopped writes the manifest as a stopped Blackbox.
	Stopped bool
	// FreePercent is the free space the status reports.
	FreePercent float64
	// DropBefore makes the newest file stop reading before the last this-much of
	// the recording (a share that dropped).
	DropBefore time.Duration
	Key        string
}

func f64(v float64) *float64 { return &v }

func (r simRec) build() *simBB {
	iv := r.Interval
	if iv == 0 {
		iv = blackbox.DefaultIntervalSecond
	}
	cpu := r.CPU
	if cpu == nil {
		cpu = func(time.Time) float64 { return 25 }
	}
	step := time.Duration(iv) * time.Second
	byHour := map[time.Time]*bytes.Buffer{}
	counts := map[time.Time]int{}
	firstOf, lastOf := map[time.Time]time.Time{}, map[time.Time]time.Time{}
	var hours []time.Time
	var times []time.Time
	seq := uint64(0)
	for t := r.Start.UTC().Truncate(time.Second); !t.After(r.End); t = t.Add(step) {
		skipped := false
		for _, sk := range r.Skip {
			if !t.Before(sk[0]) && t.Before(sk[1]) {
				skipped = true
			}
		}
		if skipped {
			continue
		}
		seq++
		c := cpu(t)
		s := blackbox.Sample{V: 1, Run: "run-1", Seq: seq, T: t, CPU: f64(c)}
		top, share := "System", 1.0
		if c > 80 && r.Busy != "" {
			top, share = r.Busy, c-5
		}
		s.Procs = []blackbox.ProcRow{
			{PID: 1204, Name: top, CPUPct: f64(share), Threads: 24},
			{PID: 4, Name: "System", CPUPct: f64(1), Threads: 180},
		}
		line, _ := json.Marshal(s)
		h := t.Truncate(time.Hour)
		buf := byHour[h]
		if buf == nil {
			buf = &bytes.Buffer{}
			byHour[h] = buf
			hours = append(hours, h)
		}
		buf.Write(line)
		buf.WriteByte('\n')
		counts[h]++
		if firstOf[h].IsZero() {
			firstOf[h] = t
		}
		lastOf[h] = t
		times = append(times, t)
	}
	name := r.Name
	host := r.Host
	if host == "" {
		host = strings.ToUpper(name)
	}
	bb := &simBB{files: map[string][]byte{}, key: r.Key, dropAt: map[string]int{}}
	m := blackbox.Manifest{
		Format: blackbox.FormatVersion, Name: name, Version: "0.9.0", Host: host, IntervalSeconds: iv,
		Created: r.Start.UTC(), Updated: r.End.UTC(), State: "running",
		Runs:   []blackbox.RunEntry{{ID: "run-1", Start: r.Start.UTC(), Version: "0.9.0"}},
		Gaps:   blackbox.DetectGaps(times, iv, blackbox.CauseLate),
		Events: []blackbox.Event{},
	}
	if r.Stopped {
		m.State = "stopped"
	}
	for _, h := range hours {
		fn := blackbox.FileNameFor(h)
		data := byHour[h].Bytes()
		bb.files[fn] = data
		m.Files = append(m.Files, blackbox.FileEntry{Name: fn, Hour: h, Bytes: int64(len(data)), Samples: counts[h], First: firstOf[h], Last: lastOf[h]})
	}
	if len(times) > 0 {
		first, last := times[0], times[len(times)-1]
		m.FirstSample, m.LastSample = &first, &last
		if r.DropBefore > 0 {
			newest := blackbox.FileNameFor(last)
			cut := 0
			for _, line := range bytes.SplitAfter(bb.files[newest], []byte("\n")) {
				var s blackbox.Sample
				if json.Unmarshal(line, &s) == nil && !s.T.Before(last.Add(-r.DropBefore)) {
					break
				}
				cut += len(line)
			}
			bb.dropAt[newest] = max(cut-3, 0)
		}
	}
	bb.man = m
	now := r.Now
	if now.IsZero() {
		now = r.End
	}
	bb.serverTime = now.Add(r.ClockSkew)
	free := r.FreePercent
	if free == 0 {
		free = 62
	}
	var total int64
	for _, f := range m.Files {
		total += f.Bytes
	}
	st := &blackbox.StatusDoc{
		Format: 1, Time: bb.serverTime, Name: name, Version: "0.9.0", Host: host, State: m.State, Run: "run-1",
		IntervalSeconds: iv, Started: r.Start.UTC(), FirstSample: m.FirstSample, LastSample: m.LastSample,
		Samples: len(times), Files: len(m.Files), Bytes: total, Gaps: len(m.Gaps), FreePercent: f64(free),
		MinFreePercent: 10, KeepDays: 14, MaxGB: 5, ManifestOK: true, WriteOK: true,
	}
	bb.status = st
	return bb
}

/* ------------------------------------------------------------- the world */

// simWorld holds the places that answer, by their canonical spelling.
type simWorld struct {
	mu     sync.Mutex
	places map[string]*simBB
}

func newSimWorld() *simWorld { return &simWorld{places: map[string]*simBB{}} }

func simCanon(loc string) string {
	l, err := blackbox.ParseLocation(loc)
	if err != nil {
		return strings.ToLower(loc)
	}
	return l.Canonical()
}

func (w *simWorld) put(loc string, bb *simBB) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.places[simCanon(loc)] = bb
}

func (w *simWorld) get(loc string) *simBB {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.places[simCanon(loc)]
}

func (w *simWorld) drop(loc string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.places, simCanon(loc))
}

// Open is BlackboxDeps.Open: the reader of one place.
func (w *simWorld) Open(loc blackbox.Location, key string) blackbox.Source {
	w.mu.Lock()
	bb := w.places[loc.Canonical()]
	w.mu.Unlock()
	if bb == nil {
		if loc.Kind == blackbox.LocURL {
			return failSource{&blackbox.ReadError{Kind: blackbox.ReadUnreachable, Message: "Nothing answered at " + loc.Display() + "."}}
		}
		return failSource{&blackbox.ReadError{Kind: blackbox.ReadNotFound, Message: "There is no Blackbox at " + loc.Display() + "."}}
	}
	if loc.Kind == blackbox.LocURL && key != bb.key {
		return failSource{&blackbox.ReadError{Kind: blackbox.ReadKeyRefused, Message: "The Blackbox at " + loc.Display() + " did not accept the read key."}}
	}
	return &simHandle{bb: bb, url: loc.Kind == blackbox.LocURL}
}

/* ------------------------------------------------------ the saved list */

// memBBStore is the saved list in memory, with the same rules as bbsaved. The
// read keys are held here and only Key gives one back.
type memBBStore struct {
	mu   sync.Mutex
	n    int
	now  func() time.Time
	list []bbsaved.Entry
	keys map[string]string
}

func newMemBBStore(now func() time.Time) *memBBStore {
	return &memBBStore{now: now, keys: map[string]string{}}
}

func (s *memBBStore) List() ([]bbsaved.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]bbsaved.Entry{}, s.list...), nil
}

func (s *memBBStore) Get(id string) (bbsaved.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.list {
		if e.ID == id {
			return e, nil
		}
	}
	return bbsaved.Entry{}, bbsaved.ErrNotFound
}

func memSame(a bbsaved.Entry, canon, host, name string) bool {
	return a.Canon == canon || (host != "" && name != "" && strings.EqualFold(a.RecorderHost, host) && strings.EqualFold(a.RecorderName, name))
}

func (s *memBBStore) Find(canon, host, name string) (bbsaved.Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.list {
		if memSame(e, canon, strings.TrimSpace(host), strings.TrimSpace(name)) {
			return e, true
		}
	}
	return bbsaved.Entry{}, false
}

func (s *memBBStore) Add(in bbsaved.AddInput) (bbsaved.Entry, error) {
	name, key := strings.TrimSpace(in.Name), strings.TrimSpace(in.Key)
	if name == "" || in.Location.Canonical() == "" || (in.Location.NeedsKey() && key == "") {
		return bbsaved.Entry{}, bbsaved.ErrInvalid
	}
	if !in.Location.NeedsKey() {
		key = ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.list {
		if memSame(e, in.Location.Canonical(), in.RecorderHost, in.RecorderName) {
			return e, bbsaved.ErrDuplicate
		}
	}
	if len(s.list) >= bbsaved.MaxEntries {
		return bbsaved.Entry{}, bbsaved.ErrTooMany
	}
	s.n++
	e := bbsaved.Entry{
		ID: fmt.Sprintf("bb-%024x", s.n), Name: name, Kind: in.Location.Kind, Location: in.Location.Display(),
		Canon: in.Location.Canonical(), ThisPC: in.ThisPC, RecorderHost: strings.TrimSpace(in.RecorderHost),
		RecorderName: strings.TrimSpace(in.RecorderName), HasKey: key != "", AddedAt: s.now().UTC(),
	}
	s.list = append(s.list, e)
	if key != "" {
		s.keys[e.ID] = key
	}
	return e, nil
}

func (s *memBBStore) update(id string, f func(*bbsaved.Entry)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.list {
		if s.list[i].ID == id {
			f(&s.list[i])
			return nil
		}
	}
	return bbsaved.ErrNotFound
}

func (s *memBBStore) SetThisPC(id string, v bool) error {
	return s.update(id, func(e *bbsaved.Entry) { e.ThisPC = v })
}

func (s *memBBStore) SetKey(id, key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return bbsaved.ErrInvalid
	}
	err := s.update(id, func(e *bbsaved.Entry) {
		if e.Kind == blackbox.LocURL {
			e.HasKey = true
		}
	})
	if err == nil {
		s.mu.Lock()
		s.keys[id] = key
		s.mu.Unlock()
	}
	return err
}

func (s *memBBStore) SetRecorder(id, host, name string) error {
	host, name = strings.TrimSpace(host), strings.TrimSpace(name)
	if host == "" || name == "" {
		return bbsaved.ErrInvalid
	}
	return s.update(id, func(e *bbsaved.Entry) {
		if e.RecorderHost == "" && e.RecorderName == "" {
			e.RecorderHost, e.RecorderName = host, name
		}
	})
}

func (s *memBBStore) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, e := range s.list {
		if e.ID == id {
			s.list = append(s.list[:i], s.list[i+1:]...)
			delete(s.keys, id)
			return nil
		}
	}
	return bbsaved.ErrNotFound
}

func (s *memBBStore) Key(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.list {
		if e.ID == id {
			return s.keys[id], nil
		}
	}
	return "", bbsaved.ErrNotFound
}

/* --------------------------------------------------------- this PC, fake */

// simPC is the Blackbox on "this PC" in memory. It answers the same calls as
// blackbox.ThisPC and the scan, and installs into the world.
type simPC struct {
	mu        sync.Mutex
	world     *simWorld
	now       time.Time
	installed bool
	running   bool
	cfg       blackbox.Config
	keys      int

	// failNext is returned by the next change, then cleared (tests).
	failNext error
	// entered gets a value when a change starts and hold, when set, keeps it there
	// until it is closed (tests of two presses at once).
	entered chan struct{}
	hold    chan struct{}

	calls []string
}

func newSimPC(w *simWorld, now time.Time) *simPC { return &simPC{world: w, now: now} }

func (p *simPC) enter(ctx context.Context, what string) error {
	p.mu.Lock()
	p.calls = append(p.calls, what)
	fail := p.failNext
	p.failNext = nil
	entered, hold := p.entered, p.hold
	p.mu.Unlock()
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return fail
}

func (p *simPC) loopback() string { return fmt.Sprintf("http://127.0.0.1:%d", p.cfg.Port) }

func (p *simPC) record(key string) {
	p.world.put(p.loopback(), simRec{
		Name: p.cfg.Name, Host: strings.ToUpper(p.cfg.Name), Start: p.now.Add(-2 * time.Minute), End: p.now.Add(-5 * time.Second),
		Now: p.now, Key: key, Interval: p.cfg.IntervalSeconds,
	}.build())
}

// seed marks a Blackbox as already installed here (the demo's starting point).
func (p *simPC) seed(cfg blackbox.Config, bb *simBB) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.installed, p.running, p.cfg = true, true, cfg
	p.world.put(p.loopback(), bb)
}

func (p *simPC) Install(ctx context.Context, cfg blackbox.Config) (blackbox.InstallOutcome, error) {
	if err := p.enter(ctx, "install"); err != nil {
		return blackbox.InstallOutcome{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.installed {
		return blackbox.InstallOutcome{}, &blackbox.ThisPCError{Reason: string(blackbox.ReasonExists), Message: "A service named UnravisBlackbox already exists."}
	}
	if cfg.Name == "" {
		cfg.Name = "this-pc"
	}
	p.keys++
	key := fmt.Sprintf("ubk-demo-%02d-NOT-A-REAL-KEY", p.keys)
	p.installed, p.running, p.cfg = true, true, cfg
	p.record(key)
	return blackbox.InstallOutcome{ReadKey: key, Name: cfg.Name, Folder: cfg.Folder, Port: cfg.Port}, nil
}

func (p *simPC) Start(ctx context.Context) error {
	if err := p.enter(ctx, "start"); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.installed {
		return &blackbox.ThisPCError{Reason: blackbox.ReasonNotInstall, Message: "No Blackbox is installed on this PC."}
	}
	p.running = true
	if bb := p.world.get(p.loopback()); bb != nil {
		bb.down = nil
	}
	return nil
}

func (p *simPC) Stop(ctx context.Context) error {
	if err := p.enter(ctx, "stop"); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.installed {
		return &blackbox.ThisPCError{Reason: blackbox.ReasonNotInstall, Message: "No Blackbox is installed on this PC."}
	}
	p.running = false
	if bb := p.world.get(p.loopback()); bb != nil {
		bb.down = &blackbox.ReadError{Kind: blackbox.ReadUnreachable, Message: "Nothing answered at " + p.loopback() + "."}
	}
	return nil
}

func (p *simPC) Apply(ctx context.Context, cfg blackbox.Config) (bool, error) {
	if err := p.enter(ctx, "apply"); err != nil {
		return false, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.installed {
		return false, &blackbox.ThisPCError{Reason: blackbox.ReasonNotInstall, Message: "No Blackbox is installed on this PC."}
	}
	restart := cfg.Port != p.cfg.Port || cfg.IntervalSeconds != p.cfg.IntervalSeconds || cfg.Bind != p.cfg.Bind
	cfg.Name = p.cfg.Name
	p.cfg = cfg
	return restart, nil
}

func (p *simPC) Remove(ctx context.Context, name string, deleteRecordings bool, confirmName string) (blackbox.RemoveOutcome, error) {
	if err := p.enter(ctx, "remove"); err != nil {
		return blackbox.RemoveOutcome{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.installed {
		return blackbox.RemoveOutcome{}, &blackbox.ThisPCError{Reason: blackbox.ReasonNotInstall, Message: "No Blackbox is installed on this PC."}
	}
	if deleteRecordings && confirmName != name {
		return blackbox.RemoveOutcome{}, &blackbox.ThisPCError{Reason: blackbox.ReasonConfirm, Message: "The name typed is not the Blackbox name."}
	}
	out := blackbox.RemoveOutcome{ServiceRemoved: true}
	if deleteRecordings {
		out.RecordingsDeleted, out.FilesDeleted = true, 4
	}
	p.world.drop(p.loopback())
	p.installed, p.running = false, false
	return out, nil
}

func (p *simPC) Snapshot(ctx context.Context) (blackbox.SnapshotOutcome, error) {
	if err := p.enter(ctx, "snapshot"); err != nil {
		return blackbox.SnapshotOutcome{}, err
	}
	return blackbox.SnapshotOutcome{
		Dir: `D:\UnravisBlackbox\snapshots\2026-10-20-0900`, Bytes: 2_412_345, Recordings: 4, Logs: 2,
	}, nil
}

// Scan is BlackboxDeps.Scan.
func (p *simPC) Scan() (blackbox.LocalFound, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.installed {
		return blackbox.LocalFound{}, nil
	}
	return blackbox.LocalFound{
		Installed: true, Running: p.running, Name: p.cfg.Name, Folder: p.cfg.Folder, Port: p.cfg.Port, Bind: p.cfg.Bind,
	}, nil
}

func (p *simPC) callLog() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string{}, p.calls...)
}

/* -------------------------------------------------------------- program */

// bytesProgram is a recorder program held in memory (the pack builder reads it).
type bytesProgram []byte

func (b bytesProgram) Open() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(b)), nil
}

/* ----------------------------------------------------------------- demo */

// demoBlackboxes builds the demo world around the demo clock now: one Blackbox in
// each state the page can show.
func demoBlackboxes(now time.Time) (*memBBStore, *simWorld, *simPC) {
	world := newSimWorld()
	store := newMemBBStore(func() time.Time { return now })
	pc := newSimPC(world, now)

	// A busy stretch the host lane can find: web-a is at 94% from 80 to 65 minutes
	// before the clock, with the web worker on top.
	spike := func(t time.Time) float64 {
		if !t.Before(now.Add(-80*time.Minute)) && t.Before(now.Add(-65*time.Minute)) {
			return 94
		}
		return 24
	}
	four := now.Add(-4 * time.Hour)

	type seedBox struct {
		name, loc string
		rec       simRec
		thisPC    bool
	}
	cfg := blackbox.Default()
	cfg.Name = "web-a"
	cfg.Folder = `D:\UnravisBlackbox`
	boxes := []seedBox{
		{name: "web-a", loc: "http://127.0.0.1:7788", thisPC: true, rec: simRec{
			Name: "web-a", Start: four, End: now.Add(-5 * time.Second), Now: now, CPU: spike, Busy: "w3wp",
			Key: "ubk-demo-a-NOT-A-REAL-KEY",
			// A short hiccup (90 seconds) well before the busy stretch.
			Skip: [][2]time.Time{{now.Add(-3 * time.Hour), now.Add(-3*time.Hour + 90*time.Second)}},
		}},
		{name: "web-b", loc: `\\web-b\UnravisBlackbox`, rec: simRec{
			Name: "web-b", Start: four, End: now.Add(-2 * time.Minute), Now: now, CPU: spike, Busy: "w3wp",
			// A 3-minute gap: too long to test those minutes.
			Skip: [][2]time.Time{{now.Add(-2 * time.Hour), now.Add(-2*time.Hour + 3*time.Minute)}},
		}},
		{name: "web-old", loc: `D:\Boxes\web-old`, rec: simRec{
			Name: "web-old", Start: four, End: now.Add(-6 * time.Hour), Now: now, Stopped: true,
		}},
		{name: "web-c", loc: "http://web-c:7788", rec: simRec{
			Name: "web-c", Start: four, End: now.Add(-5 * time.Second), Now: now, FreePercent: 6, Key: "ubk-demo-c-NOT-A-REAL-KEY",
		}},
		{name: "web-d", loc: "http://web-d:7788", rec: simRec{
			Name: "web-d", Start: four, End: now.Add(-5 * time.Second), Now: now, ClockSkew: 3400 * time.Millisecond,
			Key: "ubk-demo-d-NOT-A-REAL-KEY",
		}},
		{name: "web-e", loc: "http://web-e:7788", rec: simRec{
			Name: "web-e", Start: four, End: now.Add(-5 * time.Second), Now: now, Key: "ubk-demo-e-NOT-A-REAL-KEY",
		}},
		{name: "web-f", loc: `\\web-f\Blackbox`, rec: simRec{
			Name: "web-f", Start: four, End: now.Add(-20 * time.Second), Now: now, DropBefore: 5 * time.Minute,
		}},
	}
	for _, b := range boxes {
		bb := b.rec.build()
		if b.name == "web-e" {
			bb.down = &blackbox.ReadError{Kind: blackbox.ReadUnreachable, Message: "Nothing answered at http://web-e:7788. The server may be off, or the port is blocked."}
		}
		if b.thisPC {
			pc.seed(cfg, bb)
		} else {
			world.put(b.loc, bb)
		}
		loc, err := blackbox.ParseLocation(b.loc)
		if err != nil {
			continue
		}
		e, err := store.Add(bbsaved.AddInput{
			Name: b.name, Location: loc, Key: b.rec.Key, ThisPC: b.thisPC,
			RecorderHost: bb.man.Host, RecorderName: bb.man.Name,
		})
		_ = e
		_ = err
	}
	// Not found: the folder is in the list but nothing is there.
	if loc, err := blackbox.ParseLocation(`E:\OldBox`); err == nil {
		_, _ = store.Add(bbsaved.AddInput{Name: "old-box", Location: loc})
	}
	return store, world, pc
}

// WithBlackboxDemo attaches the Blackbox pages with the demo world: nine
// Blackboxes, one in each state, and a fake this PC that installs, starts, stops
// and removes in memory only.
func WithBlackboxDemo(now time.Time) Option {
	store, world, pc := demoBlackboxes(now)
	return WithBlackbox(BlackboxDeps{
		Store: store, ThisPC: pc, Scan: pc.Scan, Program: bytesProgram("MZ demo recorder program"),
		Open: world.Open, Now: func() time.Time { return now },
	})
}
