// Package hsaction is the one list of the things a person may change in Homestretch: move a request first, cancel
// or retry one, pause the queue, drain or resume a machine, edit which machines may run a project, add or lift a
// hold. The unravis-ci commands and the page's POST /api/action/<id> both call Run, so the safety checks, the
// activity entry and the change itself are the same code whichever way the person comes in.
//
// Run writes one activity entry for every attempt that reaches an action, refused or failed ones included. It does
// not write one for a dry run (nothing was tried) and it never creates the CI store folder just to log.
package hsaction

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"reflect"
	"sort"
	"strings"
	"time"

	"api-audit-explorer/internal/activitylog"
	"api-audit-explorer/internal/localci"
	"api-audit-explorer/internal/releasehold"
)

// Status is how an action ended.
type Status int

const (
	Done    Status = iota // it ran (or there was nothing to change)
	Refused               // a safety check said no; nothing changed
	Failed                // it was tried and did not work, in part or in whole
	Invalid               // the arguments are wrong
	Broken                // the CI store could not be read or written
)

// ExitCode is the unravis-ci exit code of the status: 0 ok, 1 refused or failed, 2 usage or I/O.
func (s Status) ExitCode() int {
	switch s {
	case Done:
		return 0
	case Refused, Failed:
		return 1
	}
	return 2
}

// HTTPStatus is the answer code of the status on POST /api/action/<id>.
func (s Status) HTTPStatus() int {
	switch s {
	case Done:
		return 200
	case Refused, Failed:
		return 409
	case Invalid:
		return 400
	}
	return 500
}

// Outcome is the word the activity entry records.
func (s Status) Outcome() string {
	return [...]string{"done", "refused", "failed", "invalid", "error"}[s]
}

// Impact is what a policy edit does to the project's queued runs.
type Impact struct {
	Queued    int      `json:"queued"`
	Stranded  []string `json:"stranded"`  // queued runs no machine could take after the edit
	NoMachine int      `json:"noMachine"` // how many of the queued runs that is
	Line      string   `json:"line"`      // the same in plain words
	// StrandedCount is len(Stranded). Base is the stamp of the policy the edit was measured against. A confirm from
	// the page sends Base and Stranded back, and is refused (409) when either changed since.
	StrandedCount int    `json:"strandedCount"`
	Base          string `json:"base,omitempty"`
}

func impactOf(p localci.PolicyImpact, base string) *Impact {
	return &Impact{Queued: p.Queued, Stranded: append([]string{}, p.Stranded...), NoMachine: p.NoMachine, Line: p.Line(),
		StrandedCount: len(p.Stranded), Base: base}
}

// Result is the answer of an action. The JSON fields are what the page gets; the others are for the CLI.
type Result struct {
	OK      bool    `json:"ok"`
	Message string  `json:"message"`
	Impact  *Impact `json:"impact,omitempty"`
	Error   string  `json:"error,omitempty"`
	Field   string  `json:"field,omitempty"` // the argument the error is about, when it is about one
	DryRun  bool    `json:"dryRun,omitempty"`

	Status  Status `json:"-"`
	Changed bool   `json:"-"` // something was written; a log that could not be written then fails the command
	Data    any    `json:"-"` // the action's own record (see each action), for the CLI's text and --json output
	LogErr  error  `json:"-"` // the activity entry could not be written (ErrNoStore: the folder is missing)
}

// ErrNoStore is Result.LogErr when the CI store folder does not exist: Run does not create it just to log.
var ErrNoStore = errors.New("the CI store folder does not exist")

// Env is who acts and on which CI store.
type Env struct {
	Dir    string           // the CI store folder
	Actor  string           // ui, cli or claude
	Person string           // the person behind the actor (the user name of the PC), when known
	Now    func() time.Time // the clock of holds and activity entries
	// Wall is the clock a machine's heartbeat is judged by (default time.Now): heartbeats carry real time, so a
	// test that fixes Now for holds still sees a live machine as live.
	Wall func() time.Time
	// ShowPaths keeps folder paths in the messages (the CLI). The page gets "the CI store" instead.
	ShowPaths bool
	// AgentStale is how long after its last heartbeat a machine counts as gone (0: 3 heartbeats).
	AgentStale time.Duration
	// ConfirmNeedsBase makes a confirmed policy edit carry the base and stranded ids of its dry run (the page). The
	// CLI's --force measures and applies in one step, so it does not need them.
	ConfirmNeedsBase bool
}

func (e Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e Env) wall() time.Time {
	if e.Wall != nil {
		return e.Wall()
	}
	return time.Now()
}

func (e Env) stale() time.Duration {
	if e.AgentStale > 0 {
		return e.AgentStale
	}
	return 3 * localci.DefaultBeatEvery
}

// step is what an action's run returns: the result and what goes in the activity entry.
type step struct {
	res    Result
	target string
	reason string
	person string // instead of Env.Person
	noLog  bool   // a dry run: nothing was tried
}

func fail(st Status, format string, a ...any) step {
	return step{res: Result{Status: st, Message: fmt.Sprintf(format, a...)}}
}

func fieldFail(st Status, field string, err error) step {
	return step{res: Result{Status: st, Field: field, Message: err.Error()}}
}

// Action is one entry of the registry.
type Action struct {
	ID      string // for example queue.top; the page posts to /api/action/<ID>
	CLI     string // the unravis-ci command that runs it
	Summary string // one line for the docs
	args    reflect.Type
	run     func(Env, any) step
}

// NewArgs is a pointer to a zero value of the action's argument type.
func (a Action) NewArgs() any { return reflect.New(a.args).Interface() }

func define[A any](id, cli, summary string, run func(Env, *A) step) Action {
	return Action{ID: id, CLI: cli, Summary: summary, args: reflect.TypeOf((*A)(nil)).Elem(),
		run: func(env Env, v any) step {
			switch a := v.(type) {
			case *A:
				if a != nil {
					return run(env, a)
				}
			case A:
				return run(env, &a)
			}
			return fail(Invalid, "the arguments of %s are not %s", id, reflect.TypeOf((*A)(nil)).Elem())
		}}
}

var registry = map[string]Action{}

func register(as ...Action) {
	for _, a := range as {
		if _, dup := registry[a.ID]; dup {
			panic("hsaction: two actions are called " + a.ID)
		}
		registry[a.ID] = a
	}
}

// Lookup finds an action by its exact id.
func Lookup(id string) (Action, bool) {
	a, ok := registry[id]
	return a, ok
}

// All is every action, by id.
func All() []Action {
	out := make([]Action, 0, len(registry))
	for _, a := range registry {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Decode reads the JSON arguments of an action strictly: one object, no field the action does not know, nothing
// after it. Empty input is the zero arguments.
func Decode(id string, raw []byte) (any, error) {
	a, ok := Lookup(id)
	if !ok {
		return nil, fmt.Errorf("there is no action %q", id)
	}
	v := a.NewArgs()
	if len(bytes.TrimSpace(raw)) == 0 {
		return v, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return nil, fmt.Errorf("the arguments of %s are not valid: %s", id, plainJSONError(err))
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("the arguments of %s must be one JSON object", id)
	}
	return v, nil
}

// plainJSONError says what is wrong with a body without echoing it back.
func plainJSONError(err error) string {
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		return fmt.Sprintf("%s must be %s", te.Field, te.Type)
	}
	m := err.Error()
	if strings.HasPrefix(m, "json: unknown field ") {
		return "unknown field " + strings.TrimPrefix(m, "json: unknown field ")
	}
	return "not a JSON object"
}

// Run runs one action and writes its activity entry. args is the action's argument struct (or a pointer to it), as
// Decode returns it or as the CLI builds it.
func Run(env Env, id string, args any) Result {
	a, ok := Lookup(id)
	if !ok {
		return finish(env, Result{Status: Invalid, Message: fmt.Sprintf("there is no action %q", id)})
	}
	if !activitylog.ValidActor(env.Actor) {
		return finish(env, Result{Status: Invalid, Message: fmt.Sprintf("actor %q is not one of %s", env.Actor, strings.Join(activitylog.Actors, ", "))})
	}
	if strings.TrimSpace(env.Dir) == "" {
		return finish(env, Result{Status: Invalid, Message: "no CI store folder is set"})
	}
	st := a.run(env, args)
	res := finish(env, st.res)
	if !st.noLog {
		res.LogErr = writeEntry(env, a.ID, st, res)
	}
	return res
}

func finish(env Env, r Result) Result {
	r.OK = r.Status == Done
	if !env.ShowPaths && env.Dir != "" {
		r.Message = hidePath(r.Message, env.Dir)
	}
	if !r.OK && r.Error == "" {
		r.Error = r.Message
		if r.Field != "" {
			r.Error = r.Field + ": " + r.Message
		}
	}
	return r
}

func hidePath(s, dir string) string {
	if s == "" {
		return s
	}
	s = strings.ReplaceAll(s, dir, "the CI store")
	return strings.ReplaceAll(s, strings.ReplaceAll(dir, `\`, "/"), "the CI store")
}

func writeEntry(env Env, id string, st step, res Result) error {
	if fi, err := os.Stat(env.Dir); err != nil || !fi.IsDir() {
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return ErrNoStore
		}
		return err
	}
	person := st.person
	if person == "" {
		person = env.Person
	}
	e := activitylog.Entry{Time: env.now(), Actor: env.Actor, Person: person, Action: id, Target: st.target, Reason: st.reason,
		Outcome: res.Status.Outcome()}
	if !res.OK {
		e.Detail = hidePath(res.Error, env.Dir)
	}
	return (activitylog.Log{Dir: env.Dir}).Append(e)
}

// isIOError says whether err came from the file system rather than from the input.
func isIOError(err error) bool {
	var pe *fs.PathError
	var le *os.LinkError
	var se *os.SyscallError
	return errors.As(err, &pe) || errors.As(err, &le) || errors.As(err, &se)
}

// ioOr is Broken for an error of the file system or of a store file that is corrupt (policy.json or holds.json that
// is not JSON, has the wrong types or a newer version), and otherwise the given status.
func ioOr(err error, otherwise Status) Status {
	if isIOError(err) || errors.Is(err, localci.ErrPolicyBroken) || errors.Is(err, releasehold.ErrStoreBroken) {
		return Broken
	}
	return otherwise
}
