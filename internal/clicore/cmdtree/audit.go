package cmdtree

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"api-audit-explorer/internal/clicore"
)

// compareFlags maps each field flag of `audit compare` to the field of the query it sets; TestFlagsMatchToolSchemas proves that every
// field exists in the tool's input schema.
var compareFlags = map[string]string{
	"source": "sourceIds", "start": "start", "end": "end", "baseline-start": "baselineStart", "baseline-end": "baselineEnd",
	"kind": "kind", "outcome": "outcome", "metric": "metric", "bucket-seconds": "bucketSeconds",
	"controller": "controller", "action": "action", "operation": "operation", "user-id": "userIds", "ui-version": "uiVersions",
	"user-agent-contains": "userAgentContains", "user-ip": "userIps", "url-contains": "urlContains", "audit-id": "auditId",
	"status-code": "statusCodes", "completion": "completion",
}

// compareTop maps the flags that are top-level arguments of the tool.
var compareTop = map[string]string{"limit": "limit", "sort": "sort"}

func (st *state) auditCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "audit",
		Short: "Compare API timings between two periods, from the running app",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, args []string) error { return st.groupHelp(c) },
	}
	group.AddCommand(st.compareCmd(), st.groupsCmd(), st.guideCmd())
	group.AddCommand(st.readCmds()...)
	return group
}

type compareFlagValues struct {
	sources, userIDs, uiVersions, userIPs, statusCodes []string
	start, end, bStart, bEnd                           string
	kind, outcome, metric, completion                  string
	bucket, limit                                      int
	controller, action, operation                      string
	agent, url, auditID, sort                          string
}

func (st *state) compareCmd() *cobra.Command {
	var v compareFlagValues
	cmd := &cobra.Command{
		Use:   "compare --source ID --start T --end T --baseline-start T --baseline-end T",
		Short: "Compare the timing of every API between a period and a baseline period",
		Long: "Runs one comparison on the running app and prints the groups that slowed down most. The result id it prints keeps the result in the app for ten minutes: " +
			"use 'unravis audit groups --result-id ID' to page or filter it with no second query on the sources.\n\n" +
			"Times are RFC 3339 with an explicit zone, for example 2026-11-23T09:00:00Z. Give the same arguments as one JSON object with --request FILE (or - for the input) " +
			"to use every field of the tool ('unravis schema audit.compare' lists them). A comparison can take five minutes.",
		Args:        cobra.NoArgs,
		Annotations: online("audit.compare", CompareTimeout.String()),
		RunE: func(c *cobra.Command, args []string) error {
			fields := make([]string, 0, len(compareFlags)+len(compareTop))
			for f := range compareFlags {
				fields = append(fields, f)
			}
			for f := range compareTop {
				fields = append(fields, f)
			}
			a, err := st.requestOr(c, fields, func() (json.RawMessage, error) { return buildCompare(c, v) })
			if err != nil {
				return err
			}
			return st.runOp("audit.compare", a, compareTable)
		},
	}
	f := cmd.Flags()
	f.StringArrayVar(&v.sources, "source", nil, "source id (repeat for more, 1 to 8)")
	f.StringVar(&v.start, "start", "", "start of the period (RFC 3339)")
	f.StringVar(&v.end, "end", "", "end of the period (RFC 3339)")
	f.StringVar(&v.bStart, "baseline-start", "", "start of the baseline period (RFC 3339)")
	f.StringVar(&v.bEnd, "baseline-end", "", "end of the baseline period (RFC 3339)")
	f.StringVar(&v.kind, "kind", "all", "request kind: all, synchronous, asynchronous, hangfire, http, http_unknown")
	f.StringVar(&v.outcome, "outcome", "all", "outcome: all, success, failure, client_error, server_error")
	f.StringVar(&v.metric, "metric", "total", "timing: total or api")
	f.IntVar(&v.bucket, "bucket-seconds", 60, "width of a chart slot, 10 to 86400")
	f.StringVar(&v.controller, "controller", "", "only this controller or job type")
	f.StringVar(&v.action, "action", "", "only this action or job method")
	f.StringVar(&v.operation, "operation", "", "only this Controller.Action")
	f.StringArrayVar(&v.userIDs, "user-id", nil, "only this user id (repeat)")
	f.StringArrayVar(&v.uiVersions, "ui-version", nil, "only this UI version (repeat)")
	f.StringVar(&v.agent, "user-agent-contains", "", "only user agents that contain this text")
	f.StringArrayVar(&v.userIPs, "user-ip", nil, "only this IP address (repeat)")
	f.StringVar(&v.url, "url-contains", "", "only URLs that contain this text")
	f.StringVar(&v.auditID, "audit-id", "", "only this audit record id (24 hex characters)")
	f.StringArrayVar(&v.statusCodes, "status-code", nil, "only this status code (repeat)")
	f.StringVar(&v.completion, "completion", "", "completion: all, complete, incomplete, pending, invalid")
	f.IntVar(&v.limit, "limit", 20, "groups on the first page, 1 to 100")
	f.StringVar(&v.sort, "sort", "", "order: p95_delta_ms, p95_ratio or current_count")
	return cmd
}

// stamp checks a time flag and returns it as UTC. The value is never repeated in an error.
func stamp(name, v string) (string, error) {
	if v == "" {
		return "", usage("--" + name + " is required.")
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return "", usage("--" + name + " must be a time like 2026-11-23T09:00:00Z.")
	}
	return t.UTC().Format(time.RFC3339Nano), nil // keeps fractions of a second: a window is judged by its exact length
}

func buildCompare(c *cobra.Command, v compareFlagValues) (json.RawMessage, error) {
	if len(v.sources) == 0 {
		return nil, usage("--source is required (give it once for each source).")
	}
	// Every key comes from compareFlags, the map that TestFlagsMatchToolSchemas checks against the tool's schema.
	q := map[string]any{compareFlags["source"]: v.sources, compareFlags["kind"]: v.kind, compareFlags["outcome"]: v.outcome,
		compareFlags["metric"]: v.metric, compareFlags["bucket-seconds"]: v.bucket}
	for _, p := range []struct{ flag, val string }{{"start", v.start}, {"end", v.end}, {"baseline-start", v.bStart}, {"baseline-end", v.bEnd}} {
		t, err := stamp(p.flag, p.val)
		if err != nil {
			return nil, err
		}
		q[compareFlags[p.flag]] = t
	}
	for _, p := range []struct{ flag, val string }{{"controller", v.controller}, {"action", v.action}, {"operation", v.operation}, {"user-agent-contains", v.agent},
		{"url-contains", v.url}, {"audit-id", v.auditID}, {"completion", v.completion}} {
		if p.val != "" {
			q[compareFlags[p.flag]] = p.val
		}
	}
	for _, p := range []struct {
		flag string
		val  []string
	}{{"ui-version", v.uiVersions}, {"user-ip", v.userIPs}, {"status-code", v.statusCodes}} {
		if len(p.val) > 0 {
			q[compareFlags[p.flag]] = p.val
		}
	}
	if len(v.userIDs) > 0 {
		ids := make([]int64, 0, len(v.userIDs))
		for _, s := range v.userIDs {
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return nil, usage("--user-id must be a whole number.")
			}
			ids = append(ids, n)
		}
		q[compareFlags["user-id"]] = ids
	}
	args := map[string]any{"query": q}
	if c.Flags().Changed("limit") {
		args[compareTop["limit"]] = v.limit
	}
	if v.sort != "" {
		args[compareTop["sort"]] = v.sort
	}
	b, err := json.Marshal(args)
	if err != nil {
		return nil, clicore.NewError(clicore.CodeInternal, "The request could not be built.", "")
	}
	return b, nil
}

// group is the part of a comparison group that the table shows.
type group struct {
	Controller      string   `json:"controller"`
	Action          string   `json:"action"`
	SourceID        string   `json:"sourceId"`
	Kind            string   `json:"kind"`
	Outcome         string   `json:"outcome"`
	State           string   `json:"state"`
	CurrentEvents   int64    `json:"currentEvents"`
	ReferenceEvents int64    `json:"referenceEvents"`
	P95Ratio        *float64 `json:"p95Ratio"`
	P95DeltaMs      *float64 `json:"p95DeltaMs"`
}

func num(f *float64, format string) string {
	if f == nil {
		return "-"
	}
	return fmt.Sprintf(format, *f)
}

func groupRows(gs []group) [][]string {
	rows := make([][]string, len(gs))
	for i, g := range gs {
		rows[i] = []string{g.Controller + "." + g.Action, g.SourceID, g.Kind, g.Outcome, g.State,
			strconv.FormatInt(g.CurrentEvents, 10), strconv.FormatInt(g.ReferenceEvents, 10), num(g.P95DeltaMs, "%.0f"), num(g.P95Ratio, "%.2f")}
	}
	return rows
}

var groupHeader = []string{"API", "SOURCE", "KIND", "OUTCOME", "STATE", "EVENTS", "BASELINE", "P95 DELTA MS", "P95 RATIO"}

// compareTable prints the groups, then the handle and how many groups the page holds.
func compareTable(w io.Writer, data json.RawMessage) error {
	var d struct {
		ResultID      string  `json:"resultId"`
		Groups        []group `json:"groups"`
		MatchedGroups int     `json:"matchedGroups"`
		TotalGroups   int     `json:"totalGroups"`
		NextOffset    *int    `json:"nextOffset"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return pretty(w, data)
	}
	if err := clicore.WriteTable(w, groupHeader, groupRows(d.Groups)); err != nil {
		return err
	}
	return pageFooter(w, d.ResultID, len(d.Groups), d.MatchedGroups, d.TotalGroups, d.NextOffset)
}

func pageFooter(w io.Writer, resultID string, shown, matched, total int, next *int) error {
	var sb strings.Builder
	fmt.Fprintf(&sb, "\n%d of %d groups shown", shown, matched)
	if total > matched {
		fmt.Fprintf(&sb, " (%d in the result)", total)
	}
	sb.WriteString(".\n")
	if next != nil {
		fmt.Fprintf(&sb, "More: add --offset %d.\n", *next)
	}
	if id := clicore.Printable(resultID); id != "" {
		fmt.Fprintf(&sb, "Result id: %s (kept ten minutes). Page it with: unravis audit groups --result-id %s\n", id, id)
	}
	_, err := io.WriteString(w, sb.String())
	return err
}

type groupsFlagValues struct {
	resultID, search, state, sort string
	offset, limit                 int
}

var groupsFlags = map[string]string{"result-id": "resultId", "offset": "offset", "limit": "limit", "search": "search", "state": "state", "sort": "sort"}

func (st *state) groupsCmd() *cobra.Command {
	var v groupsFlagValues
	cmd := &cobra.Command{
		Use:         "groups --result-id ID",
		Short:       "Page, search or sort the groups of the latest comparison (no new query on the sources)",
		Args:        cobra.NoArgs,
		Annotations: online("audit.groups", ""),
		RunE: func(c *cobra.Command, args []string) error {
			fields := make([]string, 0, len(groupsFlags))
			for f := range groupsFlags {
				fields = append(fields, f)
			}
			a, err := st.requestOr(c, fields, func() (json.RawMessage, error) {
				if v.resultID == "" {
					return nil, usage("--result-id is required (the id that 'unravis audit compare' printed).")
				}
				m := map[string]any{groupsFlags["result-id"]: v.resultID}
				if c.Flags().Changed("offset") {
					m[groupsFlags["offset"]] = v.offset
				}
				if c.Flags().Changed("limit") {
					m[groupsFlags["limit"]] = v.limit
				}
				for flag, s := range map[string]string{"search": v.search, "state": v.state, "sort": v.sort} {
					if s != "" {
						m[groupsFlags[flag]] = s
					}
				}
				return json.Marshal(m)
			})
			if err != nil {
				return err
			}
			return st.runOp("audit.groups", a, func(w io.Writer, data json.RawMessage) error {
				var d struct {
					Groups        []group `json:"groups"`
					MatchedGroups int     `json:"matchedGroups"`
					TotalGroups   int     `json:"totalGroups"`
					NextOffset    *int    `json:"nextOffset"`
				}
				if err := json.Unmarshal(data, &d); err != nil {
					return pretty(w, data)
				}
				if err := clicore.WriteTable(w, groupHeader, groupRows(d.Groups)); err != nil {
					return err
				}
				return pageFooter(w, "", len(d.Groups), d.MatchedGroups, d.TotalGroups, d.NextOffset)
			})
		},
	}
	f := cmd.Flags()
	f.StringVar(&v.resultID, "result-id", "", "the result id that 'audit compare' printed")
	f.IntVar(&v.offset, "offset", 0, "first group to show")
	f.IntVar(&v.limit, "limit", 20, "groups to show, 1 to 100")
	f.StringVar(&v.search, "search", "", "only groups whose controller, action, source, type or outcome contains this text")
	f.StringVar(&v.state, "state", "", "only groups in this state: compared, missing_current, missing_reference, insufficient_data, undefined_baseline")
	f.StringVar(&v.sort, "sort", "", "order: p95_delta_ms, p95_ratio or current_count")
	return cmd
}

func (st *state) guideCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "guide",
		Short:       "Print the investigation guide of the audit tools",
		Args:        cobra.NoArgs,
		Annotations: online("audit.guide", ""),
		RunE: func(c *cobra.Command, args []string) error {
			a, err := st.requestOr(c, nil, func() (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
			if err != nil {
				return err
			}
			return st.runOp("audit.guide", a, func(w io.Writer, data json.RawMessage) error {
				var d struct {
					Guide string `json:"guide"`
				}
				if json.Unmarshal(data, &d) != nil || d.Guide == "" {
					return pretty(w, data)
				}
				for _, line := range strings.Split(d.Guide, "\n") {
					if _, err := io.WriteString(w, clicore.Printable(line)+"\n"); err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}

func (st *state) sourcesCmd() *cobra.Command {
	g := &cobra.Command{Use: "sources", Short: "The sources connected in the running app", Args: cobra.NoArgs, RunE: func(c *cobra.Command, args []string) error { return st.groupHelp(c) }}
	g.AddCommand(&cobra.Command{
		Use:         "list",
		Short:       "List the connected sources",
		Args:        cobra.NoArgs,
		Annotations: online("sources.list", ""),
		RunE: func(c *cobra.Command, args []string) error {
			a, err := st.requestOr(c, nil, func() (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
			if err != nil {
				return err
			}
			return st.runOp("sources.list", a, func(w io.Writer, data json.RawMessage) error {
				var d struct {
					Sources []struct {
						ID, Label, Database, Collection string
					} `json:"sources"`
				}
				if json.Unmarshal(data, &d) != nil {
					return pretty(w, data)
				}
				rows := make([][]string, len(d.Sources))
				for i, s := range d.Sources {
					rows[i] = []string{s.ID, s.Label, s.Database, s.Collection}
				}
				return clicore.WriteTable(w, []string{"ID", "LABEL", "DATABASE", "COLLECTION"}, rows)
			})
		},
	})
	return g
}

func (st *state) appCmd() *cobra.Command {
	g := &cobra.Command{Use: "app", Short: "The running app", Args: cobra.NoArgs, RunE: func(c *cobra.Command, args []string) error { return st.groupHelp(c) }}
	g.AddCommand(&cobra.Command{
		Use:         "status",
		Short:       "Say whether the app runs and what it is",
		Args:        cobra.NoArgs,
		Annotations: online("app.status", ""),
		RunE: func(c *cobra.Command, args []string) error {
			a, err := st.requestOr(c, nil, func() (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
			if err != nil {
				return err
			}
			var none bytes.Buffer // app status takes no arguments, running or not
			if json.Compact(&none, a) != nil || none.String() != "{}" {
				return usage("app status takes no arguments.")
			}
			statusTable := func(w io.Writer, data json.RawMessage) error {
				var d struct {
					Running bool   `json:"running"`
					Owner   string `json:"owner"`
					PID     int    `json:"pid"`
					Version string `json:"version"`
					State   string `json:"state"`
				}
				if json.Unmarshal(data, &d) != nil {
					return pretty(w, data)
				}
				rows := [][]string{{"running", strconv.FormatBool(d.Running)}, {"state", d.State}}
				if d.Running { // the rest comes from a verified hello
					rows = append(rows, []string{"owner", d.Owner}, []string{"pid", strconv.Itoa(d.PID)}, []string{"version", d.Version})
				}
				return clicore.WriteTable(w, []string{"FIELD", "VALUE"}, rows)
			}
			// Asking whether the app runs is not a failure when it does not: say so and exit 0.
			sess, e := st.attach(st.ctx)
			if e != nil {
				state := ""
				switch e.Code {
				case clicore.CodeAppNotRunning:
					state = "stopped"
				case clicore.CodeStaleLocator:
					state = "stale"
				default:
					return e
				}
				data, _ := json.Marshal(map[string]any{"running": false, "state": state})
				return st.emit("app.status", json.RawMessage(data), clicore.Meta{Coverage: "complete", Identifiers: "hidden"}, func(w io.Writer) error { return statusTable(w, data) })
			}
			return st.runWith(sess, "app.status", a, statusTable)
		},
	})
	return g
}
