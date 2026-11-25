package cmdtree

import (
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"api-audit-explorer/internal/clicore"
)

// CodeTimeout is the default --timeout of code run: the longest a script may run (120 s) plus 30 seconds for the sandbox to start and stop.
const CodeTimeout = 2*time.Minute + 30*time.Second

// maxReasonRunes is the longest --reason; the owner checks it again.
const maxReasonRunes = 500

// codeCmd is `code` (BL-371): the typings a script is written against, and running one script in the app's code mode. The script runs in
// the app, in its sandbox, over the same read tools as the app's window; the run is recorded in the open case.
func (st *state) codeCmd() *cobra.Command {
	g := &cobra.Command{Use: "code", Short: "Run a script in the app's code mode, or print what a script may use", Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error { return st.groupHelp(c) }}
	types := &cobra.Command{
		Use:   "types",
		Short: "Print the typings a script is written against, with their hash and the limits",
		Long: "Prints what a script can use: the TypeScript typings of its tools, the hash of the tool list they were made from and the limits of a run. " +
			"It works whether or not code mode is switched on. Use -o json for the same as data.",
		Args:        cobra.NoArgs,
		Annotations: online("code.types", ""),
		RunE: func(c *cobra.Command, args []string) error {
			a, err := st.requestOr(c, nil, func() (json.RawMessage, error) { return json.RawMessage(`{}`), nil })
			if err != nil {
				return err
			}
			return st.runOp("code.types", a, typesTable)
		},
	}
	g.AddCommand(types, st.codeRunCmd())
	return g
}

func (st *state) codeRunCmd() *cobra.Command {
	var code, caseID, reason string
	var timeoutMS int
	var allowIdentifiers bool
	fieldFlags := []string{"code", "case", "timeout-ms", "reason", "allow-identifiers"}
	cmd := &cobra.Command{
		Use:   "run [FILE | -]",
		Short: "Run one script in the app's code mode (needs code mode switched on in the app)",
		Long: "Runs one TypeScript or JavaScript script in the app's sandbox, over the same read tools as the app's window, and prints what it returned. " +
			"Give the script as a FILE, as - to read it from the input, or with --code TEXT. 'unravis code types' shows what a script can use. " +
			"The run is recorded in the open case (name it with --case when more than one is open). A script that fails prints what it had and then exits with a non-zero code. " +
			"Ctrl+C stops the run. --timeout-ms limits the script itself (1000 to 120000, default 30000); --timeout limits this command. " +
			"Give the same arguments as one JSON object with --request FILE (or - for the input) instead.",
		Args:        cobra.MaximumNArgs(1),
		Annotations: online("code.run", CodeTimeout.String()),
		RunE: func(c *cobra.Command, args []string) error {
			if st.opts.request != "" && len(args) > 0 {
				return usage("--request cannot be combined with a script file.")
			}
			a, err := st.requestOr(c, fieldFlags, func() (json.RawMessage, error) {
				src, err := st.scriptSource(c, args, code)
				if err != nil {
					return nil, err
				}
				m := map[string]any{"code": src}
				if c.Flags().Changed("case") {
					if !caseIDRE.MatchString(caseID) {
						return nil, usage("That is not a case id (26 characters). Run 'unravis cases list' to see the ids.")
					}
					m["caseId"] = caseID
				}
				if c.Flags().Changed("timeout-ms") {
					if timeoutMS < 1000 || timeoutMS > clicore.MaxCodeTimeoutMS {
						return nil, usage("--timeout-ms must be between 1000 and 120000 (milliseconds).")
					}
					m["timeoutMs"] = timeoutMS
				}
				if c.Flags().Changed("reason") {
					if utf8.RuneCountInString(reason) > maxReasonRunes {
						return nil, usage("The reason is longer than 500 characters. Make it shorter.")
					}
					m["reason"] = reason
				}
				if allowIdentifiers {
					m["allowIdentifiers"] = true
				}
				return json.Marshal(m)
			})
			if err != nil {
				return err
			}
			return st.runCode(a)
		},
	}
	f := cmd.Flags()
	f.StringVar(&code, "code", "", "the script as text (instead of a file)")
	f.StringVar(&caseID, "case", "", "the id of the open case to record the run in (needed when more than one case is open)")
	f.IntVar(&timeoutMS, "timeout-ms", 0, "how long the script itself may run, 1000 to 120000 (default 30000)")
	f.StringVar(&reason, "reason", "", "why you are running it, kept with the run in the case (at most 500 characters)")
	f.BoolVar(&allowIdentifiers, "allow-identifiers", false, "show user identifiers in the result (the operator has to allow this in the app first)")
	return cmd
}

// scriptSource is the script the person gave: a file, - for the input, or --code. Exactly one of them.
func (st *state) scriptSource(c *cobra.Command, args []string, code string) (string, error) {
	fromCode := c.Flags().Changed("code")
	if fromCode == (len(args) == 1) {
		if fromCode {
			return "", usage("Give the script as a file or with --code, not both.")
		}
		return "", usage("Give the script as a file, as - to read it from the input, or with --code TEXT.")
	}
	var raw []byte
	switch {
	case fromCode:
		raw = []byte(code)
	case args[0] == "-":
		var err error
		if raw, err = st.readLimited(st.io.In, 2*clicore.MaxCodeSource+3, "The script input cannot be read."); err != nil {
			return "", err
		}
	default:
		// Look before opening: a named pipe or a device would block the open past --timeout.
		if fi, err := os.Stat(args[0]); err != nil || !fi.Mode().IsRegular() {
			return "", usage("The script file cannot be read.")
		}
		f, err := os.Open(args[0]) //nolint:gosec // the file the person named
		if err != nil {
			return "", usage("The script file cannot be read.")
		}
		defer func() { _ = f.Close() }()
		if raw, err = st.readLimited(f, 2*clicore.MaxCodeSource+3, "The script file cannot be read."); err != nil {
			return "", err
		}
	}
	b, ok := decodeText(raw)
	if !ok || !utf8.Valid(b) {
		return "", usage("The script is not valid text (UTF-8 or UTF-16).")
	}
	switch {
	case len(b) > clicore.MaxCodeSource:
		return "", clicore.CodeModeError("source_too_large")
	case strings.TrimSpace(string(b)) == "":
		return "", usage("The script is empty.")
	}
	return string(b), nil
}

// runCode runs one script. A script that was accepted but failed (it threw, ran out of time, was cancelled) is an answer, not a refusal: the
// owner sends what it had, this prints it, and the exit code then says how it ended, so a script in a pipeline never sees exit 0 for a failure.
func (st *state) runCode(args json.RawMessage) error {
	sess, e := st.attach(st.ctx)
	if e != nil {
		return e
	}
	res, err := st.callOp(sess, "code.run", args)
	if err != nil {
		return err
	}
	if err := st.emit("code.run", res.Data, res.Meta, func(w io.Writer) error { return runTable(w, res.Data) }); err != nil {
		return err
	}
	var probe struct {
		Status string `json:"status"`
		Error  *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(res.Data, &probe) != nil || probe.Status != "ok" {
		failed := ""
		if probe.Error != nil {
			failed = probe.Error.Code
		}
		return clicore.CodeModeError(failed)
	}
	return nil
}

func line(w io.Writer, s string) error {
	_, err := io.WriteString(w, clicore.Printable(s)+"\n")
	return err
}

// block prints indented lines of text under a heading.
func block(w io.Writer, head, text string) error {
	if err := line(w, head); err != nil {
		return err
	}
	for _, l := range strings.Split(strings.TrimRight(text, "\r\n"), "\n") {
		if err := line(w, "  "+strings.TrimRight(l, "\r")); err != nil {
			return err
		}
	}
	return nil
}

func typesTable(w io.Writer, data json.RawMessage) error {
	var d struct {
		Namespace    string           `json:"namespace"`
		DTS          string           `json:"dts"`
		SchemaSHA256 string           `json:"schemaSha256"`
		Limits       map[string]int64 `json:"limits"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return pretty(w, data)
	}
	if err := clicore.WriteTable(w, []string{"FIELD", "VALUE"}, [][]string{{"namespace", d.Namespace}, {"tool list hash (sha256)", d.SchemaSHA256}}); err != nil {
		return err
	}
	rows := make([][]string, 0, len(typesLimits))
	for _, l := range typesLimits {
		if v, ok := d.Limits[l.key]; ok {
			rows = append(rows, []string{l.words, strconv.FormatInt(v, 10)})
		}
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	if err := clicore.WriteTable(w, []string{"LIMIT", "VALUE"}, rows); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	return block(w, "Typings (TypeScript):", d.DTS)
}

// typesLimits are the limits of a run, in the order and the words of the table.
var typesLimits = []struct{ key, words string }{
	{"maxSourceBytes", "largest script (bytes)"},
	{"defaultTimeoutMs", "script time limit, default (ms)"},
	{"minTimeoutMs", "script time limit, shortest (ms)"},
	{"maxTimeoutMs", "script time limit, longest (ms)"},
	{"runCapSeconds", "longest a whole run may take (seconds)"},
	{"maxCalls", "tool calls in one run"},
	{"maxInFlight", "tool calls at the same time"},
	{"maxResultBytes", "largest result (bytes)"},
	{"maxLogBytes", "largest log output (bytes)"},
	{"maxConcurrentRuns", "runs at the same time"},
}

func runTable(w io.Writer, data json.RawMessage) error {
	var d struct {
		RunID       string          `json:"runId"`
		Status      string          `json:"status"`
		Result      json.RawMessage `json:"result"`
		Logs        []string        `json:"logs"`
		Identifiers string          `json:"identifiers"`
		Stats       struct {
			Calls      int   `json:"calls"`
			DurationMS int64 `json:"durationMs"`
			ScriptMS   int64 `json:"scriptMs"`
		} `json:"stats"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return pretty(w, data)
	}
	rows := [][]string{{"run id", d.RunID}, {"status", d.Status}, {"tool calls", strconv.Itoa(d.Stats.Calls)},
		{"time (ms)", strconv.FormatInt(d.Stats.DurationMS, 10) + " (script " + strconv.FormatInt(d.Stats.ScriptMS, 10) + ")"}, {"identifiers", d.Identifiers}}
	if err := clicore.WriteTable(w, []string{"FIELD", "VALUE"}, rows); err != nil {
		return err
	}
	if d.Error != nil {
		if err := line(w, ""); err != nil {
			return err
		}
		if err := line(w, "The run did not finish well: "+d.Error.Code+" - "+d.Error.Message); err != nil {
			return err
		}
	}
	if len(d.Logs) > 0 {
		if err := line(w, ""); err != nil {
			return err
		}
		if err := block(w, "Log:", strings.Join(d.Logs, "\n")); err != nil {
			return err
		}
	}
	if len(d.Result) == 0 || string(d.Result) == "null" {
		if err := line(w, ""); err != nil {
			return err
		}
		return line(w, "The script returned no value.")
	}
	if err := line(w, ""); err != nil {
		return err
	}
	if err := line(w, "Result:"); err != nil {
		return err
	}
	return pretty(w, d.Result)
}
