package cmdtree

import (
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"api-audit-explorer/internal/clicore"
)

var caseIDRE = regexp.MustCompile(`^[a-z2-7]{26}$`)

// casesListSpec is `cases list`. The fields are the arguments of the operation (internal/clicore/schemas/cases.list.json).
var casesListSpec = readSpec{op: "cases.list", use: "list", short: "List the cases of the running app (the same list as its window)",
	long: "Lists the cases the way the app's window does: id, title, dates, size and flags, followed by erase notices and any cases left in the old layout. A title reads (locked) while the key of that case is locked. " +
		"Use --archived to list the archived cases, 50 to a page.",
	flags: []fieldFlag{{"archived", "archived", "list the archived cases instead of the others", kBool, false},
		{"offset", "offset", "first archived case to list (only with --archived)", kInt, false}},
	table: casesTable}

func (st *state) casesCmd() *cobra.Command {
	g := &cobra.Command{Use: "cases", Short: "The cases of the running app (read only)", Args: cobra.NoArgs, RunE: func(c *cobra.Command, args []string) error { return st.groupHelp(c) }}
	show := &cobra.Command{
		Use:   "show ID",
		Short: "Show one case: dates, size, counts and history (no key text, path or ledger text)",
		Long: "Shows one case by its id (from 'unravis cases list'). A case whose key is locked answers CASE_LOCKED and no data: unlock it in the app. " +
			"Give the same arguments as one JSON object with --request FILE (or - for the input) instead of the id.",
		Args:        cobra.MaximumNArgs(1),
		Annotations: online("cases.show", ""),
		RunE: func(c *cobra.Command, args []string) error {
			if st.opts.request != "" && len(args) > 0 {
				return usage("--request cannot be combined with a case id.")
			}
			a, err := st.requestOr(c, nil, func() (json.RawMessage, error) {
				if len(args) != 1 || args[0] == "" {
					return nil, usage("The case id is required (it is on the list: 'unravis cases list').")
				}
				if !caseIDRE.MatchString(args[0]) {
					return nil, usage("That is not a case id (26 characters). Run 'unravis cases list' to see the ids.")
				}
				return json.Marshal(map[string]string{"id": args[0]})
			})
			if err != nil {
				return err
			}
			return st.runOp("cases.show", a, caseTable)
		},
	}
	spec := casesListSpec
	archivedFlag := false // --archived was given: the page is an archived page even when it is empty
	spec.table = func(w io.Writer, data json.RawMessage) error { return casesTablePage(w, data, archivedFlag) }
	list := spec.command(st)
	run := list.RunE
	list.RunE = func(c *cobra.Command, args []string) error {
		if st.opts.request == "" && c.Flags().Changed("offset") && !c.Flags().Changed("archived") {
			return usage("--offset goes with --archived.")
		}
		archivedFlag, _ = c.Flags().GetBool("archived")
		return run(c, args)
	}
	g.AddCommand(list, show)
	return g
}

type caseRowOut struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Created   *string `json:"created"`
	Closed    *string `json:"closed"`
	Bytes     int64   `json:"bytes"`
	Imports   int     `json:"imports"`
	OwnKey    bool    `json:"ownKey"`
	KeyLocked bool    `json:"keyLocked"`
	Archived  bool    `json:"archived"`
	Missing   bool    `json:"missing"`
	Untrusted bool    `json:"untrusted"`
}

// flagsOf is the short text of the yes/no fields of a case row.
func (c caseRowOut) flagsOf() string {
	var out string
	add := func(on bool, w string) {
		if on {
			if out != "" {
				out += ", "
			}
			out += w
		}
	}
	add(c.Closed != nil, "closed")
	add(c.Archived, "archived")
	add(c.OwnKey, "own key")
	add(c.KeyLocked, "key locked")
	add(c.Missing, "working copy missing")
	add(c.Untrusted, "untrusted")
	return out
}

func casesTable(w io.Writer, data json.RawMessage) error { return casesTablePage(w, data, false) }

// casesTablePage prints a list. The owner sends the erase notices and the old-layout count with every page, archived ones too, so they
// are printed on the first page of the other list only and not repeated on each archived page.
func casesTablePage(w io.Writer, data json.RawMessage, archivedAsked bool) error {
	var d struct {
		Cases         []caseRowOut `json:"cases"`
		ArchivedTotal int          `json:"archivedTotal"`
		Offset        int          `json:"offset"`
		PageSize      int          `json:"pageSize"`
		Erased        []struct {
			ID string  `json:"id"`
			At *string `json:"at"`
		} `json:"erased"`
		OldCases int `json:"oldCases"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return pretty(w, data)
	}
	rows := make([][]string, len(d.Cases))
	for i, c := range d.Cases {
		rows[i] = []string{c.ID, c.Title, shortTime(c.Created), strconv.FormatInt(c.Bytes, 10), strconv.Itoa(c.Imports), c.flagsOf()}
	}
	if err := clicore.WriteTable(w, []string{"ID", "TITLE", "CREATED", "BYTES", "IMPORTS", "STATE"}, rows); err != nil {
		return err
	}
	// an archived page: say where the next one starts; the other list: say how many are archived
	archivedPage := len(d.Cases) > 0
	for _, c := range d.Cases {
		archivedPage = archivedPage && c.Archived
	}
	archivedPage = archivedPage || archivedAsked || d.Offset > 0
	if archivedPage {
		foot := "\n" + strconv.Itoa(len(d.Cases)) + " archived cases shown (of " + strconv.Itoa(d.ArchivedTotal) + ").\n"
		if next := d.Offset + len(d.Cases); next < d.ArchivedTotal {
			foot += "More: add --archived --offset " + strconv.Itoa(next) + ".\n"
		}
		if _, err := io.WriteString(w, foot); err != nil {
			return err
		}
	} else if _, err := io.WriteString(w, "\n"+strconv.Itoa(len(d.Cases))+" cases shown; "+strconv.Itoa(d.ArchivedTotal)+" archived (use --archived).\n"); err != nil {
		return err
	}
	if archivedPage {
		return nil
	}
	if d.OldCases > 0 {
		if _, err := io.WriteString(w, strconv.Itoa(d.OldCases)+" cases from before the shared layout remain in the owner's folder.\n"); err != nil {
			return err
		}
	}
	if len(d.Erased) > 0 {
		erased := make([][]string, len(d.Erased))
		for i, e := range d.Erased {
			erased[i] = []string{e.ID, shortTime(e.At)}
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
		return clicore.WriteTable(w, []string{"ERASED CASE", "WHEN"}, erased)
	}
	return nil
}

func caseTable(w io.Writer, data json.RawMessage) error {
	var d struct {
		caseRowOut
		Envs    int `json:"envs"`
		Sources int `json:"sources"`
		History []struct {
			Kind string `json:"kind"`
			At   string `json:"at"`
		} `json:"history"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return pretty(w, data)
	}
	rows := [][]string{{"id", d.ID}, {"title", d.Title}, {"created", shortTime(d.Created)}, {"closed", shortTime(d.Closed)}, {"bytes", strconv.FormatInt(d.Bytes, 10)},
		{"imports", strconv.Itoa(d.Imports)}, {"environments", strconv.Itoa(d.Envs)}, {"sources", strconv.Itoa(d.Sources)}, {"state", d.flagsOf()}}
	if err := clicore.WriteTable(w, []string{"FIELD", "VALUE"}, rows); err != nil {
		return err
	}
	if len(d.History) == 0 {
		return nil
	}
	hr := make([][]string, len(d.History))
	for i, h := range d.History {
		hr[i] = []string{shortTime(&h.At), h.Kind}
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	return clicore.WriteTable(w, []string{"WHEN", "HISTORY"}, hr)
}

// shortTime is a time for a table: to the second, in UTC. Nothing (a locked case has no date) is empty.
func shortTime(s *string) string {
	if s == nil {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
