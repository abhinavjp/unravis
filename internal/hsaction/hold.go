package hsaction

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/releasehold"
)

func init() {
	register(
		define("hold.add", "unravis-ci hold add --project P|--all --until T --reason R", "Hold Promote for a project (or all) until a time.", holdAdd),
		define("hold.lift", "unravis-ci hold lift <id> --reason R", "End a hold early.", holdLift),
	)
}

// MaxHoldDays is the furthest ahead a duration may reach (ten years).
const MaxHoldDays = 3650

// ParseWhen reads a time a person typed. A duration (90m, 2h, 3d) counts from now; a date and time without a zone is
// local time; a bare date is the start of that day, or with endOfDay the end of it (so 2026-12-01 holds the whole of
// the first).
func ParseWhen(s string, now time.Time, endOfDay bool) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("empty time")
	}
	if len(s) > 64 {
		return time.Time{}, errors.New("that is not a time")
	}
	if strings.HasSuffix(s, "d") {
		if n, err := strconv.Atoi(strings.TrimSuffix(s, "d")); err == nil && n > 0 {
			if n > MaxHoldDays { // also keeps n * 24h far from the int64 limit of a Duration
				return time.Time{}, fmt.Errorf("%q is too far ahead: at most %dd", s, MaxHoldDays)
			}
			return now.Add(time.Duration(n) * 24 * time.Hour), nil
		}
	}
	if d, err := time.ParseDuration(s); err == nil { // ParseDuration itself refuses a value past the Duration limit
		if d <= 0 {
			return time.Time{}, fmt.Errorf("%q is not a time in the future", s)
		}
		if d > MaxHoldDays*24*time.Hour {
			return time.Time{}, fmt.Errorf("%q is too far ahead: at most %dd", s, MaxHoldDays)
		}
		return now.Add(d), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		if endOfDay {
			t = t.AddDate(0, 0, 1)
		}
		return t, nil
	}
	return time.Time{}, fmt.Errorf("%q is not a time: use a duration (90m, 2h, 3d), a date (2026-12-01), a local date and time (2026-12-01 18:00) or RFC 3339", s)
}

// WhenText prints a stored time for a person: local time with its offset, and UTC when they differ.
func WhenText(t time.Time) string {
	l := t.Local()
	s := l.Format("2006-01-02 15:04 -07:00")
	if _, off := l.Zone(); off != 0 {
		s += " (" + t.UTC().Format("15:04") + " UTC)"
	}
	return s
}

// ProjectText names the project of a hold for a person.
func ProjectText(p string) string {
	if p == releasehold.AllProjects {
		return "every project"
	}
	return "project " + p
}

// HoldAddArgs is the arguments of hold.add. Owner defaults to the person acting.
type HoldAddArgs struct {
	Project string `json:"project,omitempty"` // a project id, or empty with All
	All     bool   `json:"all,omitempty"`
	Until   string `json:"until"`          // 90m, 2h, 3d, 2026-12-01, 2026-12-01 18:00 or RFC 3339
	From    string `json:"from,omitempty"` // default now
	Reason  string `json:"reason"`
	Owner   string `json:"owner,omitempty"`
}

// HoldAddedText is how the CLI and the page say a hold was added.
func HoldAddedText(h releasehold.Hold) string {
	return fmt.Sprintf("hold %s added: Promote is held for %s from %s until %s.\n  owner: %s\n  reason: %s\n  lift it early with: unravis-ci hold lift %s --reason \"...\"",
		h.ID, ProjectText(h.Project), WhenText(h.Start), WhenText(h.End), h.Owner, h.Reason, h.ID)
}

func holdAdd(env Env, a *HoldAddArgs) step {
	if (a.Project == "") == !a.All {
		return step{res: Result{Status: Invalid, Field: "project", Message: "give exactly one of a project or all"}}
	}
	p := a.Project
	if a.All {
		p = releasehold.AllProjects
	}
	now := env.now()
	if strings.TrimSpace(a.Until) == "" {
		return step{res: Result{Status: Invalid, Field: "until", Message: "an end time is needed (for example 2h or 2026-12-01)"}}
	}
	end, err := ParseWhen(a.Until, now, true)
	if err != nil {
		st := fieldFail(Invalid, "until", err)
		st.target, st.reason = p, a.Reason
		return st
	}
	if end.After(now.AddDate(0, 0, MaxHoldDays)) {
		st := step{res: Result{Status: Invalid, Field: "until", Message: fmt.Sprintf("a hold ends at most %d days from now", MaxHoldDays)}}
		st.target, st.reason = p, a.Reason
		return st
	}
	var start time.Time
	if strings.TrimSpace(a.From) != "" {
		if start, err = ParseWhen(a.From, now, false); err != nil {
			st := fieldFail(Invalid, "from", err)
			st.target, st.reason = p, a.Reason
			return st
		}
	}
	owner := a.Owner
	if strings.TrimSpace(owner) == "" {
		owner = env.Person
	}
	h, err := (releasehold.Store{Dir: env.Dir}).Add(releasehold.NewHold{Project: p, Reason: a.Reason, Owner: owner, Start: start, End: end}, now)
	if err != nil {
		st := fail(ioOr(err, Invalid), "%v", err)
		st.target, st.reason = p, a.Reason
		return st
	}
	// The entry names who acted; the owner (who may be someone else) goes in the target.
	person := env.Person
	if person == "" {
		person = h.Owner
	}
	return step{res: Result{Message: HoldAddedText(h), Data: h, Changed: true},
		target: h.ID + " " + h.Project + " (owner " + h.Owner + ")", reason: h.Reason, person: person}
}

// HoldLiftArgs is the arguments of hold.lift. By (the CLI's --by) defaults to the person acting.
type HoldLiftArgs struct {
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
	By     string `json:"-"`
}

// HoldLiftedText is how the CLI and the page say a hold was lifted.
func HoldLiftedText(h releasehold.Hold) string {
	return fmt.Sprintf("hold %s lifted by %s: Promote is no longer held by it (%s, owner %s, was until %s).",
		h.ID, h.LiftedBy, ProjectText(h.Project), h.Owner, WhenText(h.End))
}

const maxHoldID = 64

func holdLift(env Env, a *HoldLiftArgs) step {
	id := strings.TrimSpace(a.ID)
	if id == "" || len(id) > maxHoldID {
		return step{res: Result{Status: Invalid, Field: "id", Message: "give one hold id"}}
	}
	who := a.By
	if strings.TrimSpace(who) == "" {
		who = env.Person
	}
	h, err := (releasehold.Store{Dir: env.Dir}).Lift(id, who, a.Reason, env.now())
	if err != nil {
		st := fail(ioOr(err, Invalid), "%v", err)
		if errors.Is(err, releasehold.ErrNotFound) {
			st.res.Status = Refused
		}
		st.target, st.reason, st.person = id, a.Reason, who
		return st
	}
	return step{res: Result{Message: HoldLiftedText(h), Data: h, Changed: true},
		target: h.ID + " " + h.Project, reason: h.LiftReason, person: h.LiftedBy}
}
