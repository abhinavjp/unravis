package localci

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// Verdicts of one compared commit.
const (
	VerdictAgree      = "agree"       // the local run and GitHub reached the same pass or fail
	VerdictDisagree   = "disagree"    // they differ, or the local run could not decide (error)
	VerdictLocalOnly  = "local-only"  // a local result, and GitHub has none (or has not finished)
	VerdictGitHubOnly = "github-only" // a GitHub result and no local run
)

// Outcomes both sides are reduced to.
const (
	OutcomePass    = "pass"
	OutcomeFail    = "fail"
	OutcomePending = "pending"
	OutcomeNone    = "none"
	OutcomeError   = "error"
)

// CompareGate is how many pull requests in a row must agree before the shadow phase may end.
const CompareGate = 20

// GHCheck is one check run or one commit status of GitHub, reduced to what a comparison reads.
type GHCheck struct {
	Name string `json:"name"`
	// State is the check run's conclusion (or its status while it runs), or the commit status's state.
	State       string    `json:"state"`
	Outcome     string    `json:"outcome"` // pass, fail, pending or none
	Description string    `json:"description,omitempty"`
	At          time.Time `json:"at,omitempty"`
}

// GHChecks is everything GitHub says about one commit.
type GHChecks struct {
	Runs     []GHCheck `json:"runs"`     // check runs
	Statuses []GHCheck `json:"statuses"` // commit statuses (the latest per context)
}

// PullHead is a pull request head the comparison looks at.
type PullHead struct {
	Number int    `json:"number"`
	Base   string `json:"base"`
	SHA    string `json:"sha"`
	State  string `json:"state"`
}

// CompareSource is the read-only side of GitHub a comparison needs. A fake serves tests.
type CompareSource interface {
	RecentPulls(ctx context.Context, repo string, limit int) ([]PullHead, error)
	BranchTip(ctx context.Context, repo, branch string) (string, error)
	Checks(ctx context.Context, repo, sha string) (GHChecks, error)
}

// LocalSide is the local result for one commit and gate.
type LocalSide struct {
	RunID      string    `json:"runId"`
	Conclusion string    `json:"conclusion"`
	Outcome    string    `json:"outcome"`
	Reason     string    `json:"reason,omitempty"`
	End        time.Time `json:"end,omitempty"`
}

// CompareRow is one commit and one gate: the local result next to GitHub's.
type CompareRow struct {
	Kind     string     `json:"kind"` // pr or push
	Number   int        `json:"number,omitempty"`
	Branch   string     `json:"branch"` // the base of a pull request, or the pushed branch
	SHA      string     `json:"sha"`
	Workflow string     `json:"workflow,omitempty"`
	Context  string     `json:"context"` // the name GitHub knows the gate by
	Local    *LocalSide `json:"local,omitempty"`
	Check    *GHCheck   `json:"check,omitempty"`  // the check run of that name
	Status   *GHCheck   `json:"status,omitempty"` // the commit status of that name (it can exist as well)
	GitHub   string     `json:"github"`           // pass, fail, pending or none: both of the above together
	Verdict  string     `json:"verdict"`
	Note     string     `json:"note,omitempty"`
}

// CompareReport is the answer of Compare. It is what the compare command prints with --json and what a
// dashboard can read.
type CompareReport struct {
	Repo        string       `json:"repo"`
	Generated   time.Time    `json:"generated"`
	Rows        []CompareRow `json:"rows"` // newest first
	Agree       int          `json:"agree"`
	Disagree    int          `json:"disagree"`
	LocalOnly   int          `json:"localOnly"`
	GitHubOnly  int          `json:"githubOnly"`
	Streak      int          `json:"streak"` // pull request heads in a row, newest first, that agree
	Gate        int          `json:"gate"`
	GateMet     bool         `json:"gateMet"`
	Disagreeing []string     `json:"disagreeing,omitempty"` // "pr 12 abc1234 build: local pass, github fail" for each
	Warnings    []string     `json:"warnings,omitempty"`
}

// CompareOptions configures Compare.
type CompareOptions struct {
	Repo   string
	Index  []IndexEntry
	Source CompareSource
	Naming StatusNaming
	// Limit is how many pull request heads and local runs to look at (newest first); default 30.
	Limit int
	Now   func() time.Time
}

func localOutcome(conclusion string) string {
	switch conclusion {
	case "success":
		return OutcomePass
	case "failure":
		return OutcomeFail
	default:
		return OutcomeError
	}
}

// checkRunOutcome reduces a check run: status is queued, in_progress or completed; conclusion is set when completed.
func checkRunOutcome(status, conclusion string) string {
	if status != "" && status != "completed" {
		return OutcomePending
	}
	switch conclusion {
	case "success", "neutral", "skipped":
		return OutcomePass
	case "failure", "timed_out", "action_required", "startup_failure", "stale":
		return OutcomeFail
	default: // cancelled, or nothing: no verdict
		return OutcomeNone
	}
}

func statusOutcome(state string) string {
	switch state {
	case "success":
		return OutcomePass
	case "failure", "error":
		return OutcomeFail
	case "pending":
		return OutcomePending
	}
	return OutcomeNone
}

// combine puts the outcomes of a check run and a status of the same name together: a failure wins, then pending.
func combine(parts ...*GHCheck) string {
	out := OutcomeNone
	for _, p := range parts {
		if p == nil {
			continue
		}
		switch {
		case p.Outcome == OutcomeFail:
			return OutcomeFail
		case p.Outcome == OutcomePending:
			out = OutcomePending
		case p.Outcome == OutcomePass && out == OutcomeNone:
			out = OutcomePass
		}
	}
	return out
}

func pick(list []GHCheck, name string) *GHCheck {
	var best *GHCheck
	for i := range list {
		if list[i].Name == name && (best == nil || list[i].At.After(best.At)) {
			best = &list[i]
		}
	}
	return best
}

// gateFor finds the gate context of a local run.
func gateFor(n StatusNaming, e IndexEntry) (kind, branch string, rule GateRule, ok bool) {
	r := Request{Repo: e.Repo, SHA: e.HeadSHA, Workflow: e.Workflow}
	if e.Event == "pull_request" {
		r.Kind, r.Base = KindPR, e.BaseRef
		branch = e.BaseRef
	} else {
		r.Kind, r.Ref = KindPush, e.HeadRef
		branch = e.HeadRef
	}
	rule, ok = n.Rule(r)
	return r.Kind, branch, rule, ok
}

func verdictOf(local *LocalSide, gh string) (verdict, note string) {
	switch {
	case local == nil && (gh == OutcomePass || gh == OutcomeFail):
		return VerdictGitHubOnly, "no local run for this commit"
	case local == nil:
		return "", ""
	case gh == OutcomeNone:
		return VerdictLocalOnly, "GitHub has no result for it"
	case gh == OutcomePending:
		return VerdictLocalOnly, "GitHub has not finished"
	case local.Outcome == OutcomeError:
		return VerdictDisagree, "the local run ended without a verdict (" + firstNonEmpty(local.Reason, local.Conclusion) + ")"
	case local.Outcome == gh:
		return VerdictAgree, ""
	}
	return VerdictDisagree, "local " + local.Outcome + ", GitHub " + gh
}

type compareKey struct {
	kind, branch, sha, context string
	number                     int
}

// Compare lists, for the recent local runs and the recent pull request heads, the local result next to what GitHub
// says about the same commit, and counts the pull requests in a row that agree.
func Compare(ctx context.Context, o CompareOptions) (*CompareReport, error) {
	if o.Source == nil {
		return nil, fmt.Errorf("compare: no source of GitHub results")
	}
	limit := o.Limit
	if limit <= 0 {
		limit = 30
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	rep := &CompareReport{Repo: o.Repo, Generated: now(), Gate: CompareGate}
	rows := map[compareKey]*CompareRow{}
	var keys []compareKey

	// local side: the newest run per commit and gate
	entries := append([]IndexEntry(nil), o.Index...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].End.After(entries[j].End) })
	for _, e := range entries {
		if e.Repo != o.Repo || e.HeadSHA == "" || (e.Event != "pull_request" && e.Event != "push") || e.Conclusion == "" {
			continue
		}
		if e.Conclusion == StateCancelled || e.Conclusion == StateSuperseded {
			continue // moot, not a verdict
		}
		kind, branch, rule, ok := gateFor(o.Naming, e)
		if !ok {
			continue
		}
		k := compareKey{kind, branch, e.HeadSHA, rule.Context, e.PR}
		if _, seen := rows[k]; seen {
			continue
		}
		if len(rows) >= limit {
			break
		}
		rows[k] = &CompareRow{Kind: kind, Number: e.PR, Branch: branch, SHA: e.HeadSHA, Workflow: e.Workflow, Context: rule.Context,
			Local: &LocalSide{RunID: e.RunID, Conclusion: e.Conclusion, Outcome: localOutcome(e.Conclusion), Reason: e.Reason, End: e.End}}
		keys = append(keys, k)
	}

	// GitHub side: recent pull request heads and the tips of the push gates, for commits nobody ran locally
	if pulls, err := o.Source.RecentPulls(ctx, o.Repo, limit); err != nil {
		rep.Warnings = append(rep.Warnings, "could not list recent pull requests: "+err.Error())
	} else {
		for _, p := range pulls {
			r := Request{Kind: KindPR, Base: p.Base}
			rule, ok := o.Naming.Rule(r)
			if !ok {
				for _, g := range o.Naming.Gates { // a rule that names a workflow still names the gate of its base
					if g.Kind == KindPR && g.Branch == p.Base {
						rule, ok = g, true
						break
					}
				}
			}
			if !ok {
				continue
			}
			k := compareKey{KindPR, p.Base, p.SHA, rule.Context, p.Number}
			if _, seen := rows[k]; !seen {
				rows[k] = &CompareRow{Kind: KindPR, Number: p.Number, Branch: p.Base, SHA: p.SHA, Workflow: rule.Workflow, Context: rule.Context}
				keys = append(keys, k)
			}
		}
	}
	seenTip := map[string]bool{}
	for _, g := range o.Naming.Gates {
		if g.Kind != KindPush || seenTip[g.Branch+"\x00"+g.Context] {
			continue
		}
		seenTip[g.Branch+"\x00"+g.Context] = true
		sha, err := o.Source.BranchTip(ctx, o.Repo, g.Branch)
		if err != nil {
			rep.Warnings = append(rep.Warnings, "could not read the tip of "+g.Branch+": "+err.Error())
			continue
		}
		k := compareKey{KindPush, g.Branch, sha, g.Context, 0}
		if _, seen := rows[k]; !seen {
			rows[k] = &CompareRow{Kind: KindPush, Branch: g.Branch, SHA: sha, Workflow: g.Workflow, Context: g.Context}
			keys = append(keys, k)
		}
	}

	// ask GitHub once per commit
	cache := map[string]GHChecks{}
	failed := map[string]bool{}
	for _, k := range keys {
		row := rows[k]
		if _, ok := cache[row.SHA]; !ok && !failed[row.SHA] {
			c, err := o.Source.Checks(ctx, o.Repo, row.SHA)
			if err != nil {
				failed[row.SHA] = true
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("could not read GitHub's results for %s: %v", shortSHA(row.SHA), err))
			} else {
				cache[row.SHA] = c
			}
		}
		if failed[row.SHA] {
			row.GitHub = OutcomeNone
			row.Verdict, row.Note = verdictOf(row.Local, OutcomeNone)
			if row.Verdict == VerdictLocalOnly {
				row.Note = "GitHub's result could not be read"
			}
			continue
		}
		c := cache[row.SHA]
		row.Check, row.Status = pick(c.Runs, row.Context), pick(c.Statuses, row.Context)
		row.GitHub = combine(row.Check, row.Status)
		var note string
		row.Verdict, note = verdictOf(row.Local, row.GitHub)
		if row.Check != nil && row.Status != nil && row.Check.Outcome != row.Status.Outcome {
			note = strings.TrimSpace(note + " (the check run and the status of that name differ)")
		}
		row.Note = note
	}

	// newest first, by what happened last
	out := make([]CompareRow, 0, len(keys))
	for _, k := range keys {
		if rows[k].Verdict != "" {
			out = append(out, *rows[k])
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return rowTime(out[i]).After(rowTime(out[j])) })
	rep.Rows = out
	streakOpen := true
	for _, r := range out {
		switch r.Verdict {
		case VerdictAgree:
			rep.Agree++
			if r.Kind == KindPR && streakOpen {
				rep.Streak++
			}
		case VerdictDisagree:
			rep.Disagree++
			if r.Kind == KindPR {
				streakOpen = false
			}
			rep.Disagreeing = append(rep.Disagreeing, fmt.Sprintf("%s %s %s: %s", rowLabel(r), shortSHA(r.SHA), r.Context, r.Note))
		case VerdictLocalOnly:
			rep.LocalOnly++
		case VerdictGitHubOnly:
			rep.GitHubOnly++
		}
	}
	rep.GateMet = rep.Streak >= rep.Gate
	return rep, nil
}

func rowTime(r CompareRow) time.Time {
	if r.Local != nil {
		return r.Local.End
	}
	return time.Time{}
}

func rowLabel(r CompareRow) string {
	if r.Kind == KindPR {
		return fmt.Sprintf("pr %d", r.Number)
	}
	return "push " + r.Branch
}

// WriteTable prints the report for people.
func (r *CompareReport) WriteTable(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "WHAT\tCOMMIT\tGATE\tLOCAL\tGITHUB (check run / status)\tVERDICT")
	for _, row := range r.Rows {
		local := "-"
		if row.Local != nil {
			local = row.Local.Outcome
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", rowLabel(row), shortSHA(row.SHA), row.Context, local, ghCell(row), row.Verdict)
	}
	_ = tw.Flush()
	for _, row := range r.Rows {
		if row.Note != "" {
			_, _ = fmt.Fprintf(w, "  %s %s %s: %s\n", rowLabel(row), shortSHA(row.SHA), row.Context, row.Note)
		}
	}
	_, _ = fmt.Fprintf(w, "\n%d agree, %d disagree, %d local-only, %d github-only\n", r.Agree, r.Disagree, r.LocalOnly, r.GitHubOnly)
	_, _ = fmt.Fprintf(w, "pull requests in a row that agree: %d of %d needed", r.Streak, r.Gate)
	if r.GateMet {
		_, _ = fmt.Fprint(w, " (enough; every disagreement above still needs an explanation)")
	}
	_, _ = fmt.Fprintln(w)
	for _, wn := range r.Warnings {
		_, _ = fmt.Fprintln(w, "warning:", wn)
	}
}

func ghCell(r CompareRow) string {
	part := func(c *GHCheck) string {
		if c == nil {
			return "-"
		}
		return c.Outcome
	}
	return part(r.Check) + " / " + part(r.Status)
}

// WriteJSON prints the report as indented JSON.
func (r *CompareReport) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// ---------------------------------------------------------------- GitHub source and fake

// GHCompareSource reads from GitHub with the gh command line tool. Everything it runs is a read.
type GHCompareSource struct {
	Env  []string
	exec func(ctx context.Context, env []string, args ...string) ([]byte, error) // test hook
}

func (g *GHCompareSource) run(ctx context.Context, args ...string) ([]byte, error) {
	if g.exec != nil {
		return g.exec(ctx, g.Env, args...)
	}
	return ghExec(ctx, g.Env, args...)
}

// RecentPulls lists the latest pull requests of any state, newest first.
func (g *GHCompareSource) RecentPulls(ctx context.Context, repo string, limit int) ([]PullHead, error) {
	out, err := g.run(ctx, "pr", "list", "--repo", repo, "--state", "all", "--limit", fmt.Sprint(limit), "--json", "number,baseRefName,headRefOid,state")
	if err != nil {
		return nil, err
	}
	var v []struct {
		Number      int    `json:"number"`
		BaseRefName string `json:"baseRefName"`
		HeadRefOid  string `json:"headRefOid"`
		State       string `json:"state"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return nil, fmt.Errorf("gh pr list: %w", err)
	}
	res := make([]PullHead, 0, len(v))
	for _, p := range v {
		res = append(res, PullHead{Number: p.Number, Base: p.BaseRefName, SHA: p.HeadRefOid, State: p.State})
	}
	return res, nil
}

// BranchTip reads the head commit of a branch.
func (g *GHCompareSource) BranchTip(ctx context.Context, repo, branch string) (string, error) {
	out, err := g.run(ctx, "api", "repos/"+repo+"/commits/"+branch, "--jq", ".sha")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Checks reads the check runs and the commit statuses of one commit.
func (g *GHCompareSource) Checks(ctx context.Context, repo, sha string) (GHChecks, error) {
	if !shaRe.MatchString(sha) {
		return GHChecks{}, fmt.Errorf("%q is not a commit id", sha)
	}
	var res GHChecks
	out, err := g.run(ctx, "api", "repos/"+repo+"/commits/"+sha+"/check-runs?per_page=100")
	if err != nil {
		return res, err
	}
	var cr struct {
		CheckRuns []struct {
			Name        string    `json:"name"`
			Status      string    `json:"status"`
			Conclusion  string    `json:"conclusion"`
			StartedAt   time.Time `json:"started_at"`
			CompletedAt time.Time `json:"completed_at"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(out, &cr); err != nil {
		return res, fmt.Errorf("check runs of %s: %w", shortSHA(sha), err)
	}
	for _, c := range cr.CheckRuns {
		state := c.Conclusion
		if c.Status != "completed" {
			state = c.Status
		}
		at := c.CompletedAt
		if at.IsZero() {
			at = c.StartedAt
		}
		res.Runs = append(res.Runs, GHCheck{Name: c.Name, State: state, Outcome: checkRunOutcome(c.Status, c.Conclusion), At: at})
	}
	out, err = g.run(ctx, "api", "repos/"+repo+"/commits/"+sha+"/statuses?per_page=100")
	if err != nil {
		return res, err
	}
	var sts []struct {
		Context     string    `json:"context"`
		State       string    `json:"state"`
		Description string    `json:"description"`
		CreatedAt   time.Time `json:"created_at"`
	}
	if err := json.Unmarshal(out, &sts); err != nil {
		return res, fmt.Errorf("statuses of %s: %w", shortSHA(sha), err)
	}
	seen := map[string]bool{}
	for _, s := range sts { // newest first: the first of a context is its current state
		if seen[s.Context] {
			continue
		}
		seen[s.Context] = true
		res.Statuses = append(res.Statuses, GHCheck{Name: s.Context, State: s.State, Outcome: statusOutcome(s.State), Description: s.Description, At: s.CreatedAt})
	}
	return res, nil
}

// FakeCompareSource serves canned answers (tests).
type FakeCompareSource struct {
	Pulls   []PullHead
	Tips    map[string]string
	Results map[string]GHChecks
	Err     map[string]error // by sha
}

// RecentPulls implements CompareSource.
func (f *FakeCompareSource) RecentPulls(context.Context, string, int) ([]PullHead, error) {
	return f.Pulls, nil
}

// BranchTip implements CompareSource.
func (f *FakeCompareSource) BranchTip(_ context.Context, _, branch string) (string, error) {
	if sha, ok := f.Tips[branch]; ok {
		return sha, nil
	}
	return "", fmt.Errorf("no branch %s", branch)
}

// Checks implements CompareSource.
func (f *FakeCompareSource) Checks(_ context.Context, _, sha string) (GHChecks, error) {
	if err := f.Err[sha]; err != nil {
		return GHChecks{}, err
	}
	return f.Results[sha], nil
}
