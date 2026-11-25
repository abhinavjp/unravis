// Package codemode is the broker of code mode (BL-363): it takes a script, has the types removed, records the run in the open case, starts
// the sandbox worker, serves the worker's host calls from the real read-only tools, enforces the budgets and returns the result. It is the
// only writer of the code-run audit record (casestore.CodeRunStore) and the only place that decides what a script may see: every answer
// that goes to the worker and every text that comes back from it passes the privacy Guard, and every call is written to the audit record
// before the script gets its answer.
//
// What this package does NOT do: run a script. There is no JavaScript engine here. The worker (BL-362, wazero and QuickJS) and the Windows
// spawner that confines it (BL-366, BL-367) are separate items; the broker talks to them through the Spawner and Worker interfaces and the
// wire protocol (internal/codemode/wire). A Broker without a Spawner refuses every run with sandbox_unavailable, and the production build
// has no other way to run a script, so nothing untrusted can run unsandboxed through this package.
//
// Trust. The worker is treated as hostile: the broker reads every frame with a size bound and a strict shape, counts calls itself,
// limits calls in flight itself, never uses a worker-supplied number as a size or an index, and ends the run on the first frame it does
// not accept. The script is hostile too: what it returns, prints or throws is swept before it is stored or returned.
package codemode

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/codemode/bindings"
	"api-audit-explorer/internal/codemode/privacy"
	"api-audit-explorer/internal/mcpsrv"
)

// Settings are the user's code-mode settings (code-mode.json), read at the start of every run.
type Settings struct {
	// Enabled is the switch of spec D8. Off by default: a Broker whose Settings function is nil is off.
	Enabled bool
	// AllowDegraded lets a run start in the restricted-token fallback (the network is not blocked there).
	AllowDegraded bool
	// KeepResults keeps a compressed copy of every call result in the case (spec D10, "all"); false stores hashes only.
	KeepResults bool
}

// CaseResolver finds the open case a run is recorded in (spec D9). caseID may be empty: the resolver then returns the one open case, or
// an error that wraps ErrNoCase when there is none or more than one. The id it returns names the case for the run-once sweep of orphan
// runs.
type CaseResolver func(ctx context.Context, caseID string) (store casestore.CodeRunStore, id string, err error)

// ErrNoCase is what a CaseResolver returns for "no case open".
var ErrNoCase = errors.New("codemode: no open case")

// Config is everything a Broker needs from the app.
type Config struct {
	// Spawner starts the worker. Nil: every run is refused with sandbox_unavailable.
	Spawner Spawner
	// Hosts makes the tool host of a run. Required.
	Hosts HostFactory
	// Cases finds the case a run is recorded in. Required.
	Cases CaseResolver
	// Settings returns the current settings. Nil: code mode is off.
	Settings func() Settings
	// Authorize is the extra gate of spec D7 (the code grant of area 03). Nil: no extra gate. An error is returned to the caller as a
	// refusal with its code (denied if it is not a *Refusal).
	Authorize func(ctx context.Context, req RunRequest) error
	// AllowIdentifiers is the operator's permission (--mcp-allow-identifiers, the UI switch) to run with raw identifiers. A request
	// that asks for them without it is refused; a script can never turn them on.
	AllowIdentifiers bool
	// RequiredMitigations are the names in the worker's hello that must all be true (spec 3.3), for example win32kOff and
	// childRestricted. The worker's word is only trusted for these, and only before any script code runs.
	RequiredMitigations []string
	// Admit charges every call of a scoped run (one that carries a grant) to the budgets and the call ledger of the grant's profile, the
	// same ones the host's own tool calls use: the app gives it Dispatcher.AdmitCodeCall. A scoped run is refused when it is nil. A
	// refused call is recorded as denied and the script gets that error answer; the run goes on. The owner's own runs are not charged.
	Admit CallAdmitter
	// Now is the clock of the broker (time.Now when nil).
	Now func() time.Time
}

// CallAdmitter is mcpsrv.Dispatcher.AdmitCodeCall: it admits one call, or refuses it, and gives the function to call when it ended.
type CallAdmitter func(ctx context.Context, pr mcpsrv.Principal, tool string, args []byte) (end func(result []byte, isError bool), refused *mcpsrv.CodeCallRefusal)

// Broker runs scripts. It is safe for concurrent use.
type Broker struct {
	cfg       Config
	schema    string
	tools     []string
	slots     chan struct{}
	runCap    time.Duration // RunCap; tests shorten it
	minTime   time.Duration // MinTimeout; tests shorten it
	helloWait time.Duration // HelloTimeout; tests shorten it
	// learn names the identifiers of an answer that the tools did not hide, so the Guard can learn them before it lets the answer
	// out. Nil in production: in hidden mode the tools return hidden-<rank> and no address, and the sweep finds addresses by itself.
	// A test sets it to reach the Guard's Incomplete state.
	learn func(tool string, answer []byte) privacy.IDs
	// tamper, in tests only, edits what a Guard call returned (stage names the call), so a test can prove that the second nets that
	// stand behind the Guard really stop an address the Guard let through. Nil in production.
	tamper func(stage, text string) string
	mu     sync.Mutex
	active map[string]*run
	swept  map[string]bool
}

// New builds a Broker. It reads the tool catalog once (the same source tools/list and unravis.d.ts come from), so the tool names a
// script may call and the tool_schema_hash of its run cannot drift from the types the model was shown.
func New(cfg Config) (*Broker, error) {
	if cfg.Hosts == nil || cfg.Cases == nil {
		return nil, errors.New("codemode: Hosts and Cases are required")
	}
	types, err := bindings.Types()
	if err != nil {
		return nil, fmt.Errorf("codemode: tool types: %w", err)
	}
	var names []string
	for _, t := range bindings.Spec().Tools {
		names = append(names, t.Name)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Broker{cfg: cfg, schema: types.SchemaSHA256, tools: names, slots: make(chan struct{}, MaxConcurrentRuns),
		runCap: RunCap, minTime: MinTimeout, helloWait: HelloTimeout, active: map[string]*run{}, swept: map[string]bool{}}, nil
}

// ToolSchemaHash is the hash every run row carries (the schema_sha256 of unravis.d.ts).
func (b *Broker) ToolSchemaHash() string { return b.schema }

// RunRequest is one script run (spec 3.4).
type RunRequest struct {
	Code             string
	CaseID           string
	TimeoutMS        int
	Reason           string
	Surface          string // cli | mcp | ui
	CallerID         string
	CallerDeclared   bool // the name is the caller's own word (MCP clientInfo)
	AllowIdentifiers bool
	// Scoped limits the run's tool calls to Sources (the source scope of the caller's grant). A run on the "mcp" surface must be scoped:
	// a host that reaches the broker through MCP has a grant, and the grant says which sources. An empty list reaches none.
	Scoped  bool
	Sources []string
	// Tier is the tier of the caller's grant (mcpsrv.TierAggregate or mcpsrv.TierEvidence). A scoped run must carry a known one: it limits
	// the tools the run may call and the detail it sees (counts below the cohort floor, record ids), as the grant does for the host's own
	// tool calls. It is written into the scope note of the audit record.
	Tier string
	// ProfileID, ProfileRev and Tools are the grant's own: the profile the budgets are charged to, its revision (for the call ledger) and
	// its tool list (Profile.Tools). A scoped run may call only the tools of that list that are also in the tier (mcpsrv.GrantTools),
	// exactly as the dispatcher decides a call of the host itself.
	ProfileID  string
	ProfileRev int
	Tools      []string
}

// Stats are the numbers of a run.
type Stats struct {
	Calls         int   `json:"calls"`
	DurationMS    int64 `json:"durationMs"`
	ScriptMS      int64 `json:"scriptMs"`
	Truncated     bool  `json:"truncated"`
	LogsTruncated bool  `json:"logsTruncated"`
}

// Sandbox says how the worker was confined.
type Sandbox struct {
	Mode    string `json:"mode"`
	Runtime string `json:"runtime"`
	Worker  string `json:"worker"`
}

// RunError is the failure of a run.
type RunError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RunResult is the answer for a run that was accepted and recorded (the status says how it ended).
type RunResult struct {
	V           int             `json:"v"`
	RunID       string          `json:"runId"`
	Status      string          `json:"status"`
	Result      json.RawMessage `json:"result"`
	Logs        []string        `json:"logs"`
	Stats       Stats           `json:"stats"`
	Sandbox     Sandbox         `json:"sandbox"`
	Identifiers string          `json:"identifiers"`
	Error       *RunError       `json:"error,omitempty"`
}

// Cancel ends the run with that id (cancelled). It reports whether the run was live.
func (b *Broker) Cancel(runID string) bool {
	b.mu.Lock()
	r := b.active[runID]
	b.mu.Unlock()
	if r == nil {
		return false
	}
	r.fail(CodeCancelled, "")
	return true
}

// OnCaseOpen closes the runs of a case that were left open by a crash (spec 3.5 step 9). The app calls it when a case is opened; Run
// also calls it once per case before its first run, so a caller that forgets cannot leave an old open run behind. It only closes runs
// older than casestore.OrphanGrace, so a live run in another window is left alone.
func (b *Broker) OnCaseOpen(ctx context.Context, store casestore.CodeRunStore) (int, error) {
	return store.CloseOrphanRuns(ctx)
}

func (b *Broker) sweepOrphans(ctx context.Context, caseID string, store casestore.CodeRunStore) {
	b.mu.Lock()
	done := b.swept[caseID]
	b.mu.Unlock()
	if done {
		return
	}
	if _, err := store.CloseOrphanRuns(ctx); err != nil {
		return // an import is running, or the store is busy: try again at the next run
	}
	b.mu.Lock()
	if len(b.swept) > 1024 {
		b.swept = map[string]bool{}
	}
	b.swept[caseID] = true
	b.mu.Unlock()
}

// cleanRequest checks and cleans what the caller typed. Nothing here touches the audit record.
func (b *Broker) cleanRequest(req *RunRequest) *Refusal {
	switch req.Surface {
	case "cli", "mcp", "ui":
	default:
		return refuse(CodeBadRequest, "surface must be cli, mcp or ui")
	}
	if len(req.Code) > MaxSource {
		return refuse(CodeSourceTooLarge, plain[CodeSourceTooLarge])
	}
	if !utf8.ValidString(req.Code) || strings.ContainsRune(req.Code, 0) {
		return refuse(CodeSyntaxError, "The script is not valid text (UTF-8 without NUL).")
	}
	if req.Surface == "mcp" && !req.Scoped {
		return refuse(CodeDenied, "A run from MCP must carry the source scope and tier of its grant.")
	}
	if req.Scoped && mcpsrv.ToolsForTier(req.Tier) == nil {
		return refuse(CodeDenied, "A run with a source scope must carry the tier of its grant (aggregate or evidence).")
	}
	if !req.Scoped && (req.Tier != "" || req.ProfileID != "" || req.ProfileRev != 0 || len(req.Tools) != 0) {
		return refuse(CodeBadRequest, "A tier, a profile and a tool list go with a source scope.")
	}
	if req.Scoped {
		// The grant's own tool list and profile: a run with no tool of its grant, or with no profile to charge, is refused (fail closed).
		if len(mcpsrv.GrantTools(req.Tier, req.Tools)) == 0 || req.ProfileID == "" {
			return refuse(CodeDenied, "A run with a source scope must carry the profile and the tool list of its grant.")
		}
		if b.cfg.Admit == nil {
			return refuse(CodeDenied, "A run with a source scope needs the call budgets of its host, and none are set up.")
		}
	}
	req.CallerID = cleanText(req.CallerID, casestore.MaxRunCallerID, false)
	req.Reason = composeReason(req)
	return nil
}

// composeReason is the reason the record gets: the scope note FIRST and whole, then the caller's words. The scope is written here because
// the begin record has no field of its own for it (a field is BL-574). The caller's text is model-controlled on the MCP surface, and
// the store changes the reason after the broker (in hidden mode it replaces addresses again, and a short one such as ::f becomes the
// longer [ip]; then it cuts the text to the cap from the END). So the note stands in front: whatever the store does, it can only shorten
// the caller's part. And the caller's text cannot forge a note:
//   - square brackets in it become round ones, so it can hold no "[sources: ...]" of its own;
//   - invisible, bidi and control characters are dropped (cleanText);
//   - in hidden mode the addresses in it are replaced here, and the caller's part is cut to the room that is left.
func composeReason(req *RunRequest) string {
	user := strings.NewReplacer("[", "(", "]", ")").Replace(cleanText(req.Reason, 4*casestore.MaxRunReason, true))
	note := scopeNote(req)
	if !req.AllowIdentifiers {
		user, _ = casestore.ReplaceAddresses(user)
		note, _ = casestore.ReplaceAddresses(note)
	}
	room := casestore.MaxRunReason - utf8.RuneCountInString(note) - 1
	if r := []rune(user); len(r) > room {
		user = string(r[:max(room, 0)])
	}
	return strings.TrimSpace(note + " " + user)
}

// scopeNote is the fixed head of the reason: the sources the run could reach and, for a grant, its tier. The tier is one of the two
// known words (cleanRequest refused any other), so it adds a fixed length; the whole note is at most 100 characters.
func scopeNote(req *RunRequest) string {
	if !req.Scoped {
		return "[sources: all]"
	}
	tier := "; tier: " + req.Tier
	if len(req.Sources) == 0 {
		return "[sources: none" + tier + "]"
	}
	list := cleanText(strings.Join(req.Sources, ","), 60, true)
	if len(strings.Join(req.Sources, ",")) > len(list) {
		list += ",..."
	}
	return "[sources: " + list + tier + "]"
}

// toolsFor lists the tools a run may call: all ten for the owner's own run, and for a run that carries a grant only the tools of its
// list within its tier (mcpsrv.GrantTools, the dispatcher's own rule; in catalog order). The run frame names exactly these, and a call to any other tool is a protocol violation.
func (b *Broker) toolsFor(req *RunRequest) []string {
	if !req.Scoped {
		return b.tools
	}
	allowed := mcpsrv.GrantTools(req.Tier, req.Tools)
	out := make([]string, 0, len(allowed))
	for _, t := range b.tools {
		if slices.Contains(allowed, t) {
			out = append(out, t)
		}
	}
	return out
}

// cleanText drops control characters and cuts to max bytes (or max characters when chars is set) at a character boundary.
func cleanText(s string, max int, chars bool) string {
	var out []rune
	size := 0
	for _, r := range strings.ToValidUTF8(s, "") {
		// Control characters, and the invisible ones that can reorder or hide text: bidi overrides and isolates, zero-width and
		// other format characters (Cf), line and paragraph separators.
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			continue
		}
		if chars {
			if len(out) >= max {
				break
			}
		} else {
			if size+utf8.RuneLen(r) > max {
				break
			}
			size += utf8.RuneLen(r)
		}
		out = append(out, r)
	}
	return string(out)
}

func (b *Broker) timeout(ms int) time.Duration {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case ms <= 0:
		d = DefaultTimeout
	case d < b.minTime:
		d = b.minTime
	case d > MaxTimeout:
		d = MaxTimeout
	}
	return d
}

func newSeed() (string, error) { return privacy.NewSeed(rand.Reader) }

func hashHex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
