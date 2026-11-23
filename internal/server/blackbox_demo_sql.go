package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"time"

	"api-audit-explorer/internal/bbsql"
	"api-audit-explorer/internal/blackbox"
)

// The demo's SQL service samples: the same hourly files the SQL service writes
// (bb-sql-<hour>.ndjson, one bbsql.SampleDoc per line), generated from the same
// curve as the host CPU so a busy stretch also shows blocking, queued memory
// grants and slower file reads. None of it is a real reading; it exists so the
// charts view and the tests read these files through the real reader.

// simSQLFiles builds the SQL sample files for a recording. cpu is the host curve:
// above 80 the demo SQL Server is "busy".
func simSQLFiles(start, end time.Time, cpu func(time.Time) float64) map[string][]byte {
	if cpu == nil {
		cpu = func(time.Time) float64 { return 25 }
	}
	byHour := map[time.Time]*bytes.Buffer{}
	put := func(t time.Time, kind string, rows any) {
		doc, err := json.Marshal(bbsql.SampleDoc{V: 1, T: t, Kind: kind, Rows: rows})
		if err != nil {
			return
		}
		h := t.UTC().Truncate(time.Hour)
		b := byHour[h]
		if b == nil {
			b = &bytes.Buffer{}
			byHour[h] = b
		}
		b.Write(doc)
		b.WriteByte('\n')
	}
	i32 := func(v int32) *int32 { return &v }
	for t := start.UTC().Truncate(time.Minute); !t.After(end); t = t.Add(15 * time.Second) {
		c := cpu(t)
		busy := c > 80
		running := uint32(6 + c/20)
		req := []bbsql.Requests{{WaitType: bbsql.WaitTypeRunning, Tasks: running}, {WaitType: "PAGEIOLATCH_SH", Tasks: 2, MaxWaitMs: 40}}
		if busy {
			req = append(req, bbsql.Requests{WaitType: "LCK_M_X", Tasks: 4, MaxWaitMs: 8000, Blocked: 4, HeadBlockers: 1, MaxBlockMs: 7800})
		}
		put(t, "requests", req)
		put(t, "schedulers", bbsql.Schedulers{CurrentTasks: uint32(20 + c/4), RunnableTasks: uint32(c / 20)})
		waiters := int32(0)
		if busy {
			waiters = 4
		}
		put(t, "semaphores", []bbsql.Semaphores{
			{PoolID: i32(2), ResourceSemaphoreID: 0, Grantees: 3, Waiters: waiters, Forced: i32(0), Timeouts: i32(0)},
			{PoolID: i32(2), ResourceSemaphoreID: 1, Grantees: 1, Waiters: 0},
		})
		if t.Second() == 0 {
			dMs := uint64(2000 + c*40)
			put(t, "waits", []bbsql.Waits{
				{WaitType: "PAGEIOLATCH_SH", DeltaMs: dMs, DeltaTasks: 40 + uint64(c)},
				{WaitType: "WRITELOG", DeltaMs: 600, DeltaTasks: 300},
				{WaitType: "LCK_M_X", DeltaMs: map[bool]uint64{true: 30000, false: 200}[busy], DeltaTasks: map[bool]uint64{true: 20, false: 3}[busy]},
			})
			put(t, "tempdb", bbsql.Tempdb{InternalPages: uint64(2000 + c*20), UserPages: 5000})
			stall := uint64(40 + c*c/40)
			put(t, "filestats", []bbsql.FileStats{
				{DatabaseID: 5, FileID: 1, ReadStallMs: stall, WriteStallMs: 20, Reads: 60, Writes: 40, SizeOnDiskBytes: 1 << 30},
				{DatabaseID: 6, FileID: 1, ReadStallMs: 10, WriteStallMs: 8, Reads: 20, Writes: 15, SizeOnDiskBytes: 1 << 29},
			})
			state := uint8(2)
			if busy {
				state = 1
			}
			put(t, "ag", []bbsql.AG{{DatabaseID: 5, SyncState: state}, {DatabaseID: 6, SyncState: 2}})
		}
	}
	out := map[string][]byte{}
	for h, b := range byHour {
		out[blackbox.SQLFileNameFor(h)] = b.Bytes()
	}
	return out
}

// OpenSQLFile serves a demo recording's SQL sample files the way the recorder does.
func (s *simHandle) OpenSQLFile(ctx context.Context, name string) (io.ReadCloser, error) {
	if s.bb.down != nil {
		return nil, s.bb.down
	}
	if s.bb.sqlLocked {
		return nil, &blackbox.ReadError{Kind: blackbox.ReadUnreachable, Message: "locked", Cause: fs.ErrPermission}
	}
	data, ok := s.bb.sqlFiles[name]
	if !ok {
		return nil, &blackbox.ReadError{Kind: blackbox.ReadNotFound, Message: "No SQL samples for that hour.", Cause: fs.ErrNotExist}
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}
