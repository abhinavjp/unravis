# TASK — write `cmd/unravis-blackbox/sqlrunner.go` (package main)

Wire the SQL runner into the separate `sql-service` process. `runSQLStub` in `main.go` is
the placeholder being replaced; the mode (`sqlRunOptions`, `runSQLService`, the service
entry) already exists and must NOT change. Existing tests inject `deps.sqlRun` with a fake,
so only `realDeps()` and the stub itself are affected.

Facts about the existing code you must code against (EXACT):

```go
// main.go
type sqlRunOptions struct {
	Config blackbox.Config   // the loaded blackbox.json
	Dir    string            // == blackbox.SQLDataDir(cfg.Folder): the locked \sql folder
	Log    func(format string, args ...any)
}
// main.go deps: sqlRun func(ctx context.Context, o sqlRunOptions) error
// realDeps() currently sets: sqlRun: runSQLStub
// runSQLService calls: d.sqlRun(ctx, sqlRunOptions{Config: cfg, Dir: blackbox.SQLDataDir(cfg.Folder), Log: logf})

// internal/blackbox
func SQLDataDir(folder string) string // filepath.Join(folder, "sql")
type Probe struct {
	Enabled               bool   `json:"enabled"` // config validation REFUSES true in this release
	ServerCertificate     string `json:"serverCertificate,omitempty"`
	Certificate           string `json:"certificate,omitempty"`
	HostNameInCertificate string `json:"hostNameInCertificate,omitempty"`
}
// blackbox.Config has field: Probe Probe

// internal/bbsql (per the BBX-5-10 CONTRACT in the static block)
func NewRunner(dir string, conn ConnConfig) *Runner
func (r *Runner) Run(ctx context.Context) error
type ConnConfig struct { Server string; QueryTimeout time.Duration; ServerCertificate, Certificate, HostNameInCertificate string }
```

Write ONE new file with:

1. `func runSQLServiceLoop(ctx context.Context, o sqlRunOptions) error` — the real
   `deps.sqlRun`:
   - `os.MkdirAll(o.Dir, 0o700)` (the ACL on the folder is installed by `install-sql`;
     this process only creates it).
   - Build `bbsql.ConnConfig`: `Server: "(local)"` with a comment that the C12 local-only
     rule and the fixed default instance are deliberate — a named instance needs the probe
     config block, which is deliberately still refused by `config.go` (owner decision, live
     check needed); `QueryTimeout: 5 * time.Second` (the C4 per-query ceiling; the runner
     narrows it to 3 s for 15 s items); map the three `Probe` trust-anchor fields straight
     through (empty means no anchor and the connection fails closed).
   - `r := bbsql.NewRunner(o.Dir, conn)`; `if o.Log != nil { r.Log = o.Log }`.
   - `return r.Run(ctx)`.
   - Nothing else: no HTTP server, no status endpoint, no host recorder, no `Open` call of
     your own (the runner owns the connection), no reading of credentials.
2. `func sqlTrustAnchors(p blackbox.Probe) bbsql.ConnConfig` — the pure mapping helper, so
   the anchor wiring is directly testable, and use it from `runSQLServiceLoop`.
3. Doc comments in the style of the package: terse, stating the control each rule satisfies
   and the isolation promise (this code path exists ONLY in the `UnravisBlackboxSql` service
   process, runs as `NT SERVICE\UnravisBlackboxSql`, and writes only to `o.Dir`).

Also state, in one comment block at the top of the file, the two things that cannot be
checked without a real server: the SQL service sampling a live instance and host sampling
staying unaffected when SQL is slow.

Do NOT touch `main.go` (a human/orchestrator re-points `realDeps().sqlRun` to
`runSQLServiceLoop` and deletes `runSQLStub`). Reply with the new file only, then list any
assumption a reviewer must check.
