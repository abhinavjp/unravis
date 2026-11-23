package casestore

import (
	"context"
	"sort"
	"time"
)

// The in-memory CodeRunStore, for tests and demo mode. It runs the same
// cleaning, record and verification code as the sqlite store (coderuns.go), so
// the contract suite holds both to one behaviour. Tests in this package may
// reach into memCode to damage a row, the way the sqlite tests use raw SQL.

var _ CodeRunStore = (*memCase)(nil)
var _ CodeChainSource = memSource{}

type memCode struct {
	runs  map[string]*CodeRun
	order []string // run ids in creation order
	calls map[string][]CodeCall
	chain []ChainRow
	head  *ChainAnchor // the head kept beside the chain
}

func (d *memData) codeLog() *memCode {
	if d.code == nil {
		d.code = &memCode{runs: map[string]*CodeRun{}, calls: map[string][]CodeCall{}}
	}
	return d.code
}

// codeWrite is the gate of every write; d.mu is held and the handle was checked. seal is true for the writes that belong to
// a run already begun (a call, the end, the crash close): a closed case still takes them.
func (c *memCase) codeWrite(seal bool) error {
	switch {
	case c.d.erased:
		return ErrNotFound
	case c.d.info.Archive != nil && c.d.info.Archive.Active:
		return ErrArchived
	case c.d.info.Mark == MarkArchiving:
		return ErrCaseClosed
	case !seal && c.d.info.Closed != nil:
		return ErrCaseClosed
	}
	return nil
}

func (m *memCode) tail() (int64, string) {
	if len(m.chain) == 0 {
		return 0, ChainZeroHash
	}
	last := m.chain[len(m.chain)-1]
	return last.N, last.RecordHash
}

func (m *memCode) append(row ChainRow) {
	m.chain = append(m.chain, row)
	m.head = &ChainAnchor{N: row.N, Hash: row.RecordHash}
}

func (c *memCase) BeginRun(ctx context.Context, in RunStart) (CodeRun, error) {
	run, err := NewCodeRun(in, c.s.now())
	if err != nil {
		return CodeRun{}, err
	}
	if err := c.check(ctx); err != nil {
		return CodeRun{}, err
	}
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	if err := c.codeWrite(false); err != nil {
		return CodeRun{}, err
	}
	m := c.d.codeLog()
	if _, dup := m.runs[run.RunID]; dup {
		return CodeRun{}, ErrRunExists
	}
	if len(m.runs) >= MaxCodeRuns {
		return CodeRun{}, ErrCapExceeded
	}
	n, prev := m.tail()
	row, err := BeginChainRow(run, n+1, prev)
	if err != nil {
		return CodeRun{}, err
	}
	m.runs[run.RunID] = &run
	m.order = append(m.order, run.RunID)
	m.append(row)
	return run, nil
}

func (c *memCase) RecordCall(ctx context.Context, runID string, rec CallRecord) error {
	if err := c.check(ctx); err != nil {
		return err
	}
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	if err := c.codeWrite(true); err != nil {
		return err
	}
	m := c.d.codeLog()
	run, ok := m.runs[runID]
	if !ok {
		return ErrRunNotFound
	}
	if run.Status != RunRunning {
		return ErrRunFinished
	}
	existing := m.calls[runID]
	if len(existing) >= MaxCallsPerRun {
		return ErrCapExceeded
	}
	var used int64
	for _, e := range existing {
		if e.Seq == rec.Seq || e.DoneSeq == rec.DoneSeq {
			return ErrCallExists
		}
		used += int64(len(e.Blob))
	}
	call, err := NewCodeCall(*run, rec, used)
	if err != nil {
		return err
	}
	n, prev := m.tail()
	row, err := CallChainRow(call, n+1, prev)
	if err != nil {
		return err
	}
	m.calls[runID] = append(existing, call)
	m.append(row)
	return nil
}

func sortedCalls(in []CodeCall) []CodeCall {
	out := append([]CodeCall(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// endLocked seals one run; d.mu is held.
func (c *memCase) endLocked(m *memCode, run *CodeRun, end RunEnd, now time.Time) error {
	calls := sortedCalls(m.calls[run.RunID])
	fin, err := FinishCodeRun(*run, calls, end, now)
	if err != nil {
		return err
	}
	n, prev := m.tail()
	row, err := EndChainRow(fin, calls, n+1, prev)
	if err != nil {
		return err
	}
	*run = fin
	m.append(row)
	return nil
}

func (c *memCase) EndRun(ctx context.Context, runID string, end RunEnd) (CodeRun, error) {
	if err := c.check(ctx); err != nil {
		return CodeRun{}, err
	}
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	if err := c.codeWrite(true); err != nil {
		return CodeRun{}, err
	}
	m := c.d.codeLog()
	run, ok := m.runs[runID]
	if !ok {
		return CodeRun{}, ErrRunNotFound
	}
	if run.Status != RunRunning {
		return CodeRun{}, ErrRunFinished
	}
	if err := c.endLocked(m, run, end, c.s.now()); err != nil {
		return CodeRun{}, err
	}
	return *run, nil
}

func (c *memCase) CloseOrphanRuns(ctx context.Context) (int, error) {
	if err := c.check(ctx); err != nil {
		return 0, err
	}
	c.d.mu.Lock()
	defer c.d.mu.Unlock()
	if err := c.codeWrite(true); err != nil {
		return 0, err
	}
	m := c.d.codeLog()
	n := 0
	now := c.s.now()
	for _, id := range m.order {
		if run := m.runs[id]; run.Status == RunRunning && now.UnixMilli()-run.CreatedMS >= OrphanGrace.Milliseconds() {
			if err := c.endLocked(m, run, CrashedEnd(), now); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

func listView(r CodeRun) CodeRun {
	r.Source, r.ConsoleLogs = "", ""
	return r
}

func (c *memCase) ListRuns(ctx context.Context, q RunQuery) ([]CodeRun, error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > MaxRunsPage {
		limit = MaxRunsPage
	}
	c.d.mu.RLock()
	defer c.d.mu.RUnlock()
	m := c.d.code
	if m == nil {
		return nil, nil
	}
	var out []CodeRun
	for i := len(m.order) - 1 - q.Offset; i >= 0 && len(out) < limit; i-- {
		out = append(out, listView(*m.runs[m.order[i]]))
	}
	return out, nil
}

func (c *memCase) GetRun(ctx context.Context, runID string) (CodeRun, error) {
	if err := c.check(ctx); err != nil {
		return CodeRun{}, err
	}
	c.d.mu.RLock()
	defer c.d.mu.RUnlock()
	if c.d.code == nil || c.d.code.runs[runID] == nil {
		return CodeRun{}, ErrRunNotFound
	}
	return *c.d.code.runs[runID], nil
}

func (c *memCase) RunCalls(ctx context.Context, runID string) ([]CodeCall, error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	c.d.mu.RLock()
	defer c.d.mu.RUnlock()
	if c.d.code == nil || c.d.code.runs[runID] == nil {
		return nil, ErrRunNotFound
	}
	out := sortedCalls(c.d.code.calls[runID])
	for i := range out {
		out[i].BlobBytes, out[i].Blob = len(out[i].Blob), nil
	}
	return out, nil
}

func (c *memCase) CallResult(ctx context.Context, runID string, seq int) ([]byte, error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	c.d.mu.RLock()
	defer c.d.mu.RUnlock()
	if c.d.code == nil || c.d.code.runs[runID] == nil {
		return nil, ErrRunNotFound
	}
	for _, call := range c.d.code.calls[runID] {
		if call.Seq == seq {
			return ResultFromCall(call)
		}
	}
	return nil, ErrRunNotFound
}

// ResultFromCall returns the kept result of a call row after checking it against
// its hash. It is the one place both stores turn a stored copy back into bytes.
func ResultFromCall(c CodeCall) ([]byte, error) {
	if c.BlobState != BlobKept {
		return nil, ErrBlobNotKept
	}
	out, err := DecodeCallBlob(c.Blob)
	if err != nil {
		return nil, err
	}
	if int64(len(out)) != c.ResultBytes || sha256Hex(out) != c.ResultHash {
		return nil, ErrCodeTampered
	}
	return out, nil
}

func (c *memCase) ChainRows(ctx context.Context, afterN int64, limit int) ([]ChainRow, error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > MaxChainRowsPage {
		limit = MaxChainRowsPage
	}
	c.d.mu.RLock()
	defer c.d.mu.RUnlock()
	if c.d.code == nil {
		return nil, nil
	}
	var out []ChainRow
	for _, r := range c.d.code.chain {
		if r.N > afterN && len(out) < limit {
			out = append(out, r)
		}
	}
	return out, nil
}

func (c *memCase) VerifyCodeChain(ctx context.Context, anchor *ChainAnchor) (ChainReport, error) {
	if err := c.check(ctx); err != nil {
		return ChainReport{}, err
	}
	// One read lock for the whole walk, so a write that lands meanwhile cannot make a
	// sound record look torn (the sqlite store uses one read transaction).
	c.d.mu.RLock()
	defer c.d.mu.RUnlock()
	return VerifyCodeChain(ctx, memSource{c}, anchor)
}

func (c *memCase) CodeRunStats(ctx context.Context) (CodeStats, error) {
	if err := c.check(ctx); err != nil {
		return CodeStats{}, err
	}
	c.d.mu.RLock()
	defer c.d.mu.RUnlock()
	var s CodeStats
	if m := c.d.code; m != nil {
		s.Runs = len(m.runs)
		for id, r := range m.runs {
			if r.Status == RunRunning {
				s.OpenRuns++
			}
			s.TextBytes += int64(len(r.Source) + len(r.ConsoleLogs))
			for _, call := range m.calls[id] {
				s.Calls++
				s.BlobBytes += int64(len(call.Blob))
			}
		}
	}
	return s, nil
}

// memSource reads the memory case for VerifyCodeChain. It is its own type so the
// reading methods are not part of the case's own surface.
type memSource struct{ c *memCase }

// The CodeChainSource methods below read without locking: VerifyCodeChain holds the read lock.

func (s memSource) EachChainRow(ctx context.Context, fn func(ChainRow) error) error {
	var rows []ChainRow
	if s.c.d.code != nil {
		rows = append(rows, s.c.d.code.chain...)
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].N < rows[j].N })
	for _, r := range rows {
		if err := fn(r); err != nil {
			return err
		}
	}
	return nil
}

func (s memSource) EachRunID(ctx context.Context, fn func(string) error) error {
	var ids []string
	if s.c.d.code != nil {
		ids = append(ids, s.c.d.code.order...)
	}
	for _, id := range ids {
		if err := fn(id); err != nil {
			return err
		}
	}
	return nil
}

func (s memSource) LoadRun(ctx context.Context, id string) (CodeRun, bool, error) {
	if s.c.d.code == nil || s.c.d.code.runs[id] == nil {
		return CodeRun{}, false, nil
	}
	return *s.c.d.code.runs[id], true, nil
}

func (s memSource) LoadCalls(ctx context.Context, id string) ([]CodeCall, error) {
	if s.c.d.code == nil {
		return nil, nil
	}
	return sortedCalls(s.c.d.code.calls[id]), nil
}

func (s memSource) CountCalls(ctx context.Context) (int64, error) {
	var n int64
	if s.c.d.code != nil {
		for _, cs := range s.c.d.code.calls {
			n += int64(len(cs))
		}
	}
	return n, nil
}

func (s memSource) StoredHead(ctx context.Context) (int64, string, bool, error) {
	if s.c.d.code == nil || s.c.d.code.head == nil {
		return 0, "", false, nil
	}
	return s.c.d.code.head.N, s.c.d.code.head.Hash, true, nil
}

func (s memSource) MissingGuards(ctx context.Context) ([]string, error) { return nil, nil }
