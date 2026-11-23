package cmdtree

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"api-audit-explorer/internal/clicore"
)

// The read commands that follow a comparison (spec 05 3.2): each one is a list of field flags, and the flag-to-field names are data, so
// TestReadFlagsMatchToolSchemas can prove every field is in the tool's input schema and every required field has a required flag.
type flagKind int

const (
	kString flagKind = iota
	kStrings
	kInt
	kFloat
	kBool
	kTime // RFC 3339, sent as UTC
)

type fieldFlag struct {
	name, field, usage string
	kind               flagKind
	required           bool
}

// readSpec describes one read command.
type readSpec struct {
	op, tool string
	use      string
	short    string
	long     string
	flags    []fieldFlag
	table    func(w io.Writer, data json.RawMessage) error
}

const (
	resultIDUsage = "the result id that 'audit compare' printed"
	apiKeyUsage   = "the exact group key from 'audit compare' or 'audit groups' (never rebuild it from names)"
)

var readSpecs = []readSpec{
	{op: "audit.timeline", use: "timeline --result-id ID --api-key KEY", short: "One group's timeline and baseline statistics (from the stored comparison)",
		flags: []fieldFlag{{"result-id", "resultId", resultIDUsage, kString, true}, {"api-key", "apiKey", apiKeyUsage, kString, true}}},
	{op: "audit.requests", use: "requests --result-id ID --api-key KEY", short: "A page of the newest requests of one group (a live read of metadata)",
		flags: []fieldFlag{{"result-id", "resultId", resultIDUsage, kString, true}, {"api-key", "apiKey", apiKeyUsage, kString, true},
			{"cursor", "cursor", "the nextCursor of the last page", kString, false}, {"limit", "limit", "rows on the page, 1 to 100", kInt, false}},
		table: requestsTable},
	{op: "audit.slowest", use: "slowest --result-id ID", short: "The slowest completed requests of the comparison (a live read of metadata)",
		flags: []fieldFlag{{"result-id", "resultId", resultIDUsage, kString, true}, {"cursor", "cursor", "the nextCursor of the last page", kString, false},
			{"limit", "limit", "rows on the page, 1 to 50", kInt, false}, {"min-ms", "minMs", "only requests at least this slow, in milliseconds", kFloat, false}},
		table: requestsTable},
	{op: "audit.breakdown", use: "breakdown --result-id ID --dim DIM", short: "Count the comparison's requests by status code, user, UI version, user agent, polling type or IP",
		flags: []fieldFlag{{"result-id", "resultId", resultIDUsage, kString, true},
			{"dim", "dim", "statusCode, userId, uiVersion, userAgent, pollingType or userIp", kString, true},
			{"top", "top", "values to list, 0 to 50 (default 20)", kInt, false}, {"include-ip", "includeIp", "needed for --dim userIp (the app must allow identifiers)", kBool, false}},
		table: breakdownTable},
	{op: "audit.activity", use: "activity --source ID --start T --end T", short: "Activity of the sources around a time, across their APIs and jobs (a live read, at most 24 hours)",
		flags: []fieldFlag{{"source", "sourceIds", "source id (repeat for more, 1 to 8)", kStrings, true}, {"start", "start", "start of the window (RFC 3339)", kTime, true},
			{"end", "end", "end of the window (RFC 3339)", kTime, true}, {"cursor", "cursor", "the nextCursor of the last page", kString, false},
			{"limit", "limit", "rows on the page, 1 to 100", kInt, false}},
		table: requestsTable},
}

// timelinesCompareSpec is `audit timelines compare`.
var timelinesCompareSpec = readSpec{op: "audit.timelines.compare", use: "compare --result-id ID --api-key KEY [--api-key KEY ...]",
	short: "Compare 1 to 8 groups on aligned time slots (from the stored comparison)",
	flags: []fieldFlag{{"result-id", "resultId", resultIDUsage, kString, true}, {"api-key", "apiKeys", apiKeyUsage + "; repeat for up to 8", kStrings, true}}}

func allReadSpecs() []readSpec {
	return append(append([]readSpec(nil), readSpecs...), timelinesCompareSpec)
}

// values holds what the flags of one command parsed to.
type values struct {
	s  map[string]*string
	ss map[string]*[]string
	i  map[string]*int
	f  map[string]*float64
	b  map[string]*bool
}

func (sp readSpec) register(f *pflag.FlagSet) values {
	v := values{map[string]*string{}, map[string]*[]string{}, map[string]*int{}, map[string]*float64{}, map[string]*bool{}}
	for _, fl := range sp.flags {
		switch fl.kind {
		case kString, kTime:
			v.s[fl.name] = f.String(fl.name, "", fl.usage)
		case kStrings:
			v.ss[fl.name] = f.StringArray(fl.name, nil, fl.usage)
		case kInt:
			v.i[fl.name] = f.Int(fl.name, 0, fl.usage)
		case kFloat:
			v.f[fl.name] = f.Float64(fl.name, 0, fl.usage)
		case kBool:
			v.b[fl.name] = f.Bool(fl.name, false, fl.usage)
		}
	}
	return v
}

// build makes the arguments of the tool from the flags that were set. A flag that was not set is not sent: the tool has its own defaults.
func (sp readSpec) build(c *cobra.Command, v values) (json.RawMessage, error) {
	args := map[string]any{}
	for _, fl := range sp.flags {
		set := c.Flags().Changed(fl.name)
		if fl.required && !set {
			return nil, usage("--" + fl.name + " is required.")
		}
		if !set {
			continue
		}
		switch fl.kind {
		case kString:
			if *v.s[fl.name] == "" {
				return nil, usage("--" + fl.name + " needs a value.")
			}
			args[fl.field] = *v.s[fl.name]
		case kTime:
			t, err := stamp(fl.name, *v.s[fl.name])
			if err != nil {
				return nil, err
			}
			args[fl.field] = t
		case kStrings:
			for _, s := range *v.ss[fl.name] {
				if s == "" {
					return nil, usage("--" + fl.name + " needs a value.")
				}
			}
			args[fl.field] = *v.ss[fl.name]
		case kInt:
			args[fl.field] = *v.i[fl.name]
		case kFloat:
			args[fl.field] = *v.f[fl.name]
		case kBool:
			args[fl.field] = *v.b[fl.name]
		}
	}
	b, err := json.Marshal(args)
	if err != nil {
		return nil, clicore.NewError(clicore.CodeInternal, "The request could not be built.", "")
	}
	return b, nil
}

func (sp readSpec) command(st *state) *cobra.Command {
	var v values
	long := sp.long
	if long == "" {
		long = sp.short + ". Give the same arguments as one JSON object with --request FILE (or - for the input) to use every field of the tool ('unravis schema " + sp.op + "' lists them)."
	}
	cmd := &cobra.Command{
		Use: sp.use, Short: sp.short, Long: long, Args: cobra.NoArgs, Annotations: online(sp.op, ""),
		RunE: func(c *cobra.Command, _ []string) error {
			fields := make([]string, len(sp.flags))
			for i, fl := range sp.flags {
				fields[i] = fl.name
			}
			a, err := st.requestOr(c, fields, func() (json.RawMessage, error) { return sp.build(c, v) })
			if err != nil {
				return err
			}
			table := sp.table
			if table == nil {
				table = pretty
			}
			return st.runOp(sp.op, a, table)
		},
	}
	v = sp.register(cmd.Flags())
	return cmd
}

func (st *state) readCmds() []*cobra.Command {
	var out []*cobra.Command
	for _, sp := range readSpecs {
		out = append(out, sp.command(st))
	}
	tl := &cobra.Command{Use: "timelines", Short: "Compare the timelines of several groups", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error { return st.groupHelp(c) }}
	tl.AddCommand(timelinesCompareSpec.command(st))
	return append(out, tl)
}

// cellText turns one JSON value into table text.
func cellText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return fmt.Sprint(v)
}

// dropEmpty removes the columns that hold no text in any row.
func dropEmpty(header []string, rows [][]string) ([]string, [][]string) {
	keep := make([]int, 0, len(header))
	for c := range header {
		for _, r := range rows {
			if r[c] != "" {
				keep = append(keep, c)
				break
			}
		}
	}
	if len(rows) == 0 {
		return header, rows
	}
	h := make([]string, len(keep))
	for i, c := range keep {
		h[i] = header[c]
	}
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = make([]string, len(keep))
		for j, c := range keep {
			out[i][j] = r[c]
		}
	}
	return h, out
}

// requestsTable prints the rows of `audit requests`, `audit slowest` and `audit activity`: one page of request records. A column that
// no row fills (the source of a one-source page, the API of a one-group page) is left out.
func requestsTable(w io.Writer, data json.RawMessage) error {
	var d struct {
		Rows       []map[string]any `json:"rows"`
		SourceID   string           `json:"sourceId"`
		Controller string           `json:"controller"`
		Action     string           `json:"action"`
		NextCursor string           `json:"nextCursor"`
	}
	if err := json.Unmarshal(data, &d); err != nil || d.Rows == nil {
		return pretty(w, data)
	}
	tr := make([][]string, len(d.Rows))
	for i, r := range d.Rows {
		source, api := cellText(r["sourceId"]), cellText(r["controller"])
		if source == "" {
			source = d.SourceID
		}
		if api != "" {
			api += "." + cellText(r["action"])
		} else if d.Controller != "" {
			api = d.Controller + "." + d.Action
		}
		tr[i] = []string{cellText(r["id"]), source, api, cellText(r["initiatedAt"]), cellText(r["state"]), cellText(r["status"]), cellText(r["durationMs"])}
	}
	h, tr := dropEmpty([]string{"ID", "SOURCE", "API", "STARTED", "STATE", "STATUS", "MS"}, tr)
	if err := clicore.WriteTable(w, h, tr); err != nil {
		return err
	}
	if d.NextCursor != "" {
		_, err := io.WriteString(w, "\nMore: add --cursor "+clicore.Printable(d.NextCursor)+"\n")
		return err
	}
	return nil
}

// breakdownTable prints the counts of `audit breakdown`; the Other row comes last.
func breakdownTable(w io.Writer, data json.RawMessage) error {
	var d struct {
		Rows  []map[string]any `json:"rows"`
		Other map[string]any   `json:"other"`
		Total float64          `json:"total"`
	}
	if err := json.Unmarshal(data, &d); err != nil || d.Rows == nil {
		return pretty(w, data)
	}
	rows := d.Rows
	if d.Other != nil {
		o := map[string]any{}
		for k, v := range d.Other {
			o[k] = v
		}
		if o["value"] == nil {
			o["value"] = "(other)"
		}
		rows = append(rows, o)
	}
	tr := make([][]string, len(rows))
	for i, r := range rows {
		tr[i] = []string{cellText(r["value"]), cellText(r["count"]), cellText(r["complete"]), cellText(r["incomplete"]), cellText(r["pending"]), cellText(r["count4xx"]), cellText(r["count5xx"])}
	}
	if err := clicore.WriteTable(w, []string{"VALUE", "COUNT", "COMPLETE", "INCOMPLETE", "PENDING", "4XX", "5XX"}, tr); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "\n%s requests in total.\n", strconv.FormatFloat(d.Total, 'f', -1, 64))
	return err
}
