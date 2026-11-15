# STATIC CONTEXT — unravis Blackbox, package `internal/bbsql` (Go 1.27, Windows service)

You are contributing ONE file to a Go repository. You cannot see the repo, cannot run
Go, and must not invent APIs. Everything you may rely on is written below. Output only
code that compiles against this surface.

## Product rules that bind every line (non-negotiable)

- The Blackbox is a read-only diagnostic service. The SQL sampler runs ONLY in its own
  Windows service (`UnravisBlackboxSql`), never in the host recorder process.
- C1: SQL text may only come from `Statement(id)`; `Query` refuses anything else. Never
  build SQL, never pass parameters.
- C4: connect 5 s; per-query 3 s for 15 s items and 5 s for 60 s items; whole tick 5 s.
  A tick that is still running when the next one is due is SKIPPED and counted — never
  queued, never run in parallel.
- C6: row caps are enforced by `decodeRows`/`DBQueryer`; never grow them.
- C7: failures double the interval (15 s -> 15 min); 10 failures in a row, any permission
  error, or a ceiling failure latches auto-off; auto-off survives a service restart and
  only the owner clears it (`OwnerEnable`).
- C10: never write `err.Error()`, driver text, instance names, login names, hosts,
  database names or file paths into anything persisted or logged. Only fixed codes.
- C11: `status.json` holds exactly `{enabled, state, lastOK, failures}` — the host reads
  it with `json.DisallowUnknownFields()`, so any extra key or unknown state value makes
  the host report "off".
- C13: footprint budget — average tick cost under 1 % of one core, under 10 MB extra
  memory, under 2 KB per persisted sample.
- No HTTP server in this process. No `trustservercertificate`. No writes to SQL.

## Existing surface of package `bbsql` (EXACT, already committed — use as-is)

```go
type Row = map[string]any   // declared in conn.go as `type Row map[string]any`

type Queryer interface {
	Query(ctx context.Context, text string) ([]Row, error)
}

func Query(ctx context.Context, q Queryer, text string, timeout time.Duration) ([]Row, error)
    // the ONLY send path: refuses non-allowlisted text, applies the per-query timeout.
    // Returns *Error (fixed code) on refusal/timeout.

func Statement(id string) (string, bool)      // id -> fixed statement text
var Statements map[string]string              // "bb:requests","bb:waits","bb:schedulers",
                                              // "bb:semaphores","bb:tempdb","bb:filestats",
                                              // "bb:ag","bb:ceiling_role","bb:ceiling_perms"

type ConnConfig struct {
	Server                string
	QueryTimeout          time.Duration
	ServerCertificate     string
	Certificate           string
	HostNameInCertificate string
}
func Open(cfg ConnConfig) (*sql.DB, error)   // local-only enforced inside
func CheckLocalTarget(server string) error
type DBQueryer struct { DB *sql.DB; MaxRows int }
func (d DBQueryer) Query(ctx context.Context, text string) ([]Row, error)

type Error struct { Code, Message string }
func (e *Error) Error() string
func Classify(err error) *Error
const ( CodeLoginFailed="login-failed"; CodeDatabase="database"; CodePermission="permission";
        CodeConnectTimeout="timeout"; CodeNetwork="network"; CodeTLS="tls";
        CodeTooManyRights="too-many-rights"; CodeFailed="failed" )

func CheckCeiling(q Queryer) error            // C8; returns ErrTooManyRights or a fixed *Error

const ( BaseInterval=15*time.Second; MaxInterval=15*time.Minute; DemotedInterval=60*time.Second;
        AutoOffFailures=10; SlowStrikeLimit=3 )
const ( StateOn="on"; StateBackingOff="backing-off"; StateAutoOff="auto-off"; StateDenied="denied" )
type State struct { Interval time.Duration; Failures int; AutoOff bool; SlowStrikes int; State string }
func NewState() State
func (s *State) OnSuccess()
func (s *State) OnFailure(permission, ceiling bool)
func (s *State) OnSlow(used, limit time.Duration)
func OwnerEnable(s *State)
func Save(path string, s State) error
func Load(path string) (State, error)         // missing file -> NewState; corrupt -> auto-off
func StatePath(folder string) string          // == filepath.Join(folder, "sql", "state.json")

// fixed output structs (Requests, Waits, Schedulers, Semaphores, Tempdb, FileStats, AG)
// and, in output.go:
func decodeRows(rows []Row, dst interface{}, cap int) (truncated bool, err error)
const ( CapRequests=256; CapWaits=20; CapSchedulers=1; CapSemaphores=16; CapTempdb=1;
        CapFileStats=256; CapAG=64 )
```

Note the trap: `StatePath(folder)` takes the RECORDINGS root and appends `sql`. The runner
works with the already-resolved SQL folder, so it must join `state.json` directly and must
NOT call `StatePath`.

## CONTRACT for BBX-5-10 (all orders must match this EXACTLY — other files are written
## against it in parallel, so a renamed field or signature breaks the build)

```go
// Sampler is one allowlisted SQL sample. Samplers register from their own package init
// (packets BBX-5-11..5-14). Decode may hold per-sampler state (cumulative counters from
// the previous tick) and turns them into deltas in Go.
type Sampler interface {
	ID() string                            // allowlisted statement id, e.g. "bb:requests"
	Kind() string                          // NDJSON key, e.g. "requests"
	Interval() time.Duration               // 15*time.Second or 60*time.Second
	Decode(rows []Row) (any, bool, error)  // payload for SampleDoc.Rows, truncated, fixed error
}

func RegisterSampler(s Sampler)   // nil-safe; registering the same Kind twice is a no-op
func Samplers() []Sampler         // snapshot, sorted by Kind (stable order)

// ConnectFunc opens the single connection and returns the send seam plus its closer.
// nil means defaultConnect (Open + DBQueryer{MaxRows: 0}).
type ConnectFunc func(ConnConfig) (Queryer, func() error, error)

// SampleDoc is one NDJSON line. Only numbers, enums and validated wait types can appear:
// Rows is whatever a Sampler's Decode produced from the fixed structs.
type SampleDoc struct {
	V         int       `json:"v"`  // always 1
	T         time.Time `json:"t"`  // UTC tick time
	Kind      string    `json:"kind"`
	Truncated bool      `json:"truncated,omitempty"`
	Rows      any       `json:"rows"`
}

// StatusDoc is the C11 shape. Field tags MUST stay exactly as below (host reader is
// strict).
type StatusDoc struct {
	Enabled  bool       `json:"enabled"`
	State    string     `json:"state"`
	LastOK   *time.Time `json:"lastOK,omitempty"`
	Failures int        `json:"failures"`
}

type Runner struct {
	Dir     string      // the SQL data folder (already ends with \sql); holds state.json,
	                    // status.json and bb-sql-YYYYMMDD-HH.ndjson
	Conn    ConnConfig
	Connect ConnectFunc // test seam; nil -> defaultConnect
	Now     func() time.Time // nil -> time.Now (UTC)
	Log     func(format string, args ...any) // fixed-code-only lines; nil -> silent

	// unexported: state State, samplers []Sampler, lastRun map[string]time.Time,
	// lastOK time.Time, running bool, skipped int, q Queryer, closeConn func() error,
	// connected bool
}

func NewRunner(dir string, conn ConnConfig) *Runner   // loads state.json (fails closed)
func (r *Runner) Tick(now time.Time) error            // one tick, never re-entrant
func (r *Runner) Run(ctx context.Context) error       // ticker loop; returns on ctx.Done
func (r *Runner) Skipped() int
func (r *Runner) Running() bool
func (r *Runner) CurrentState() State                 // copy, for tests and status
func StatusPath(dir string) string                    // dir\status.json
func SamplePath(dir string, t time.Time) string       // dir\bb-sql-20060102-15.ndjson (UTC hour)
```

Tick semantics, in order (implement exactly):
1. `if r.running { r.skipped++; return nil }` (never parallel, never queued).
2. `r.running = true` and `defer` clearing it.
3. If `r.state.AutoOff` -> write status `{enabled:false, state, lastOK, failures}` and
   return nil. Do not connect. Do not clear auto-off.
4. Whole-tick budget: `ctx, cancel := context.WithTimeout(now, TickBudget)` where
   `const TickBudget = 5 * time.Second`.
5. If not connected: `Connect` -> `CheckCeiling(q)`. On connect error: `Classify` it,
   `state.OnFailure(code==CodePermission||code==CodeLoginFailed, false)`, close, save,
   write status, return the fixed error. On ceiling error: `OnFailure(false, true)` (or
   permission=true when the code is CodePermission), save, write status, return it.
6. For each registered sampler that is due — due means `now.Sub(lastRun[kind]) >= eff` or
   never run, with `eff = max(r.state.Interval, s.Interval())` — run
   `Query(ctx, q, text, timeout)` where the item limit is 3 s when `s.Interval() <= 15 s`
   else 5 s, and `timeout = min(itemLimit, time remaining in ctx)`.
   On success: `s.Decode(rows)`; on Decode error treat it as a failure (see next bullet);
   on success append one marshaled `SampleDoc` line to `SamplePath(dir, now)` (O_APPEND,
   0600, MkdirAll 0700), `state.OnSuccess()`, `state.OnSlow(used, itemLimit)`,
   `lastRun[kind] = now`, `r.lastOK = now`.
   On failure: `Classify(err)` -> `state.OnFailure(permission, ceiling)` where
   `permission = code in {CodePermission, CodeLoginFailed, CodeTooManyRights}` and
   `ceiling = code == CodeTooManyRights`; then stop the loop (do not pile more load on a
   struggling instance), drop the connection (so the next tick re-checks the ceiling) and
   return the fixed error.
7. `Save(StatePathJoin(r.Dir), r.state)` then write status.json atomically (temp + rename
   in the same folder). `enabled = !r.state.AutoOff`, `state = r.state.State`,
   `lastOK` (nil when zero), `failures = r.state.Failures`. Never persist error text.
8. `Run` waits `r.state.Interval` between ticks (back-off must take effect immediately, so
   use a `time.Timer` re-armed per tick, not a fixed `time.Ticker`), calls `Tick`, logs
   only the fixed code of an error, and returns `nil` on `ctx.Done()`.

## Output format (STRICT)

Reply with exactly one fenced ```go code block containing the complete file (package clause
`bbsql` for files inside internal/bbsql, with the import block). After the block, at most
10 short bullet lines: what you assumed and anything a reviewer must double-check. No
other prose, no diff markers, no placeholders like `// ... rest unchanged`.
