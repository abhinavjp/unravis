```go
package main

// sqlrunner.go — wiring for the separate SQL service process (BBX-5-10).
//
// This file exists ONLY in the UnravisBlackboxSql service process. It runs as
// NT SERVICE\UnravisBlackboxSql and writes only to o.Dir (the locked \sql
// folder); it never touches the host recorder's output, never opens a
// connection of its own, and never reads credentials. The SQL text is fixed
// inside bbsql (C1); this code passes no SQL, no parameters and no error text
// onward (C10).
//
// Two things this file cannot verify without a real server:
//
//  1. That the SQL service actually samples a live local instance end-to-end
//     (connect, ceiling check, allowlisted query, decode, NDJSON append) —
//     every step here is wired, but only a real SQL Server proves the path.
//  2. That host sampling stays unaffected while SQL is slow or failing — the
//     isolation promise holds by process separation in this release, but the
//     measurable effect of a backed-off 15-minute SQL loop on the host side
//     needs a live instance under load.

import (
	"context"
	"os"
	"time"

	"unravis/internal/bbsql"
	"unravis/internal/blackbox"
)

// runSQLServiceLoop is the real deps.sqlRun: it runs the bbsql runner inside
// the UnravisBlackboxSql service process until ctx is done. Satisfies C4
// (budgets live inside the runner), C7 (state.json survives restarts) and C11
// (status.json is written by the runner, in its exact shape).
func runSQLServiceLoop(ctx context.Context, o sqlRunOptions) error {
	// The ACL on the folder is installed by install-sql; this process only
	// creates it if missing.
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return err
	}

	r := bbsql.NewRunner(o.Dir, sqlTrustAnchors(o.Config.Probe))
	if o.Log != nil {
		r.Log = o.Log
	}
	return r.Run(ctx)
}

// sqlTrustAnchors maps the three Probe trust-anchor fields straight into the
// bbsql.ConnConfig. Empty fields mean no anchor and the connection fails
// closed inside bbsql.Open. Satisfies C12: the server is pinned to "(local)" —
// that is deliberate (fixed default instance, local-only rule). A named
// instance would need the probe config block, which config.go deliberately
// still refuses (owner decision, live check needed).
func sqlTrustAnchors(p blackbox.Probe) bbsql.ConnConfig {
	return bbsql.ConnConfig{
		// "(local)" is deliberate: the C12 local-only rule plus the fixed
		// default instance. No named instance without a future probe config
		// block.
		Server:                "(local)",
		// 5 s is the C4 per-query ceiling for 60 s items; the runner narrows
		// it to 3 s for 15 s items.
		QueryTimeout:          5 * time.Second,
		ServerCertificate:     p.ServerCertificate,
		Certificate:           p.Certificate,
		HostNameInCertificate: p.HostNameInCertificate,
	}
}
```

- Import path assumed as module root `unravis` (`unravis/internal/...`) — reviewer must match `go.mod`; the static block never states the module path.
- `sqlTrustAnchors` takes `blackbox.Probe` by value, not the whole `Config` — narrowest pure-mapping signature; reviewer should confirm no test expects a `Config` parameter.
- The `(local)` server string and 5 s `QueryTimeout` live inside `sqlTrustAnchors` (not `runSQLServiceLoop`) so the mapping stays the single testable seam; the contract asked for them in the loop build step, which is satisfied because the loop calls this helper — but a reviewer may want them inlined if tests stub `sqlTrustAnchors`.
- `MkdirAll` failure is returned raw (no `*Error` wrapping) — assumed acceptable since it is a local filesystem error, not SQL; `Classify` would map it to `CodeFailed` only inside the runner.
- No `StatusPath`/`SamplePath` references here: the runner owns those paths via `NewRunner(o.Dir, ...)`; confirmed `o.Dir` already ends with `\sql` per the `SQLDataDir` fact.
- `r.Log = o.Log` only when non-nil, so the runner's silent default is preserved.
- Nothing re-points `realDeps().sqlRun` — that edit is explicitly out of scope and left to the orchestrator; until then `runSQLStub` still runs.
- No glog/logging of instance or login names anywhere; only `o.Log` passthrough, which the runner confines to fixed codes (C10).
- Windows service entry (`svc.Run`, `install-sql`) untouched, per the "must NOT change" constraint.
Checkpoint published (seq 10). File delivered above.
