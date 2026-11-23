// Package cmdtree is the Cobra command tree of the command line (spec 05-cli D3, 3.1). Only this package and cmd/unravis import Cobra;
// policy, data and server packages never do. This slice holds the commands that need no running app: version, schema, completion and
// `blackbox inspect --offline`. The online commands come with the attach layer.
package cmdtree

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"api-audit-explorer/internal/clicore"
)

// IO is what a run reads and writes. A test passes buffers; main passes the process streams.
type IO struct {
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	Getenv func(string) string // nil means the allowed environment variables only
	Exe    func() (string, error)
}

// Default and largest --timeout (spec 3.2).
const (
	DefaultTimeout = 30 * time.Second
	MaxTimeout     = time.Hour
)

// state is what the commands of one run share.
type state struct {
	io      IO
	mode    clicore.Mode
	reqID   string
	ctx     context.Context
	opts    globalFlags
	flagSet bool // a flag named the output mode
	cancel  context.CancelFunc
}

type globalFlags struct {
	output     string
	json       bool
	timeout    time.Duration
	quiet      bool
	noColor    bool
	outputFile string
	overwrite  bool
}

// opAnnotation names the operation id of a command, for the error envelope of a usage error.
const opAnnotation = "unravis.op"

// legacyFlags are the options of the app program (unravis.exe) that the command line does not take (spec D5). "--version" is answered
// here; "--quit" and "--check-update" are forwarded to the app program in a later slice.
var legacyFlags = map[string]bool{
	"demo": true, "port": true, "no-browser": true, "mcp": true, "mcp-allow-identifiers": true, "allow-broad-scans": true,
	"docs-examined-budget": true, "quit": true, "check-update": true, "background": true, "pipe-server": true, "pipe-only": true, "loopback": true,
}

// Run runs the command line and returns its exit code. Output goes to io; nothing else is written anywhere.
func Run(ctx context.Context, args []string, io IO) int {
	if io.Getenv == nil {
		io.Getenv = clicore.Getenv
	}
	st := &state{io: io, reqID: clicore.NewRequestID(), mode: clicore.PeekOutputMode(args, io.Getenv)}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	st.ctx = ctx

	if len(args) > 0 {
		if code, handled := st.legacy(args[0]); handled {
			return code
		}
	}
	root := st.root()
	root.SetArgs(args)
	root.SetContext(ctx)
	executed, err := root.ExecuteC()
	if st.cancel != nil {
		st.cancel()
	}
	if err == nil {
		return 0
	}
	if errors.Is(err, errOutputClosed) {
		return clicore.ExitCode(clicore.CodeCanceled) // nobody reads stdout any more: no text anywhere
	}
	op := "unknown"
	if executed != nil {
		if v, ok := executed.Annotations[opAnnotation]; ok {
			op = v
		}
	}
	return clicore.WriteError(io.Err, st.mode, op, st.reqID, st.convert(err, executed))
}

// legacy answers the first argument when it is an option of the app program.
func (st *state) legacy(first string) (int, bool) {
	name := strings.TrimLeft(first, "-")
	if !strings.HasPrefix(first, "-") || strings.HasPrefix(name, "-") {
		return 0, false
	}
	name, _, _ = strings.Cut(name, "=")
	if name == "version" && !strings.HasPrefix(first, "---") {
		return st.versionLine(), true
	}
	if legacyFlags[name] {
		e := clicore.NewError(clicore.CodeLegacyOption, "That option belongs to unravis.exe in the Unravis folder, not to the command line.", "Run 'unravis --help' to see the commands.")
		return clicore.WriteError(st.io.Err, st.mode, "unknown", st.reqID, e), true
	}
	return 0, false
}

var (
	reUnknownFlag = regexp.MustCompile(`unknown (?:shorthand )?flag: ('?-[-A-Za-z0-9]+'?)`)
	reBadValue    = regexp.MustCompile(`for ("?-[-A-Za-z0-9]+"?|'[A-Za-z0-9]'|"[-A-Za-z0-9]+") flag`)
	reNeedsValue  = regexp.MustCompile(`flag needs an argument: ('?-[-A-Za-z0-9]+'?|'[A-Za-z0-9]' in -[A-Za-z0-9])`)
)

// convert turns what Cobra or a command returns into a command line error. A usage message never repeats the value the person typed.
func (st *state) convert(err error, executed *cobra.Command) *clicore.Error {
	var ce *clicore.Error
	if errors.As(err, &ce) {
		return ce
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return clicore.NewError(clicore.CodeTimeout, "The command ran out of time.", "")
	}
	if errors.Is(err, context.Canceled) {
		return clicore.NewError(clicore.CodeCanceled, "Canceled.", "")
	}
	msg := err.Error()
	hint := "Run 'unravis --help' to see the commands and options."
	switch {
	case strings.HasPrefix(msg, "unknown command") && executed != nil && executed.HasParent():
		return clicore.NewError(clicore.CodeInvalidArgument, "The command was given an argument it does not take.", hint)
	case strings.HasPrefix(msg, "unknown command"):
		return clicore.NewError(clicore.CodeUnknownOperation, "That is not a command.", hint)
	case reUnknownFlag.MatchString(msg):
		return clicore.NewError(clicore.CodeInvalidArgument, "Unknown option "+strings.Trim(reUnknownFlag.FindStringSubmatch(msg)[1], `'"`)+".", hint)
	case reNeedsValue.MatchString(msg):
		return clicore.NewError(clicore.CodeInvalidArgument, "An option needs a value.", hint)
	case reBadValue.MatchString(msg):
		return clicore.NewError(clicore.CodeInvalidArgument, "The value of "+strings.Trim(reBadValue.FindStringSubmatch(msg)[1], `'"`)+" is not valid.", hint)
	case strings.Contains(msg, "accepts") && strings.Contains(msg, "arg"):
		return clicore.NewError(clicore.CodeInvalidArgument, "The command was given arguments it does not take.", hint)
	}
	return clicore.NewError(clicore.CodeInvalidArgument, "The command line is not valid.", hint)
}

func usage(msg string) *clicore.Error {
	return clicore.NewError(clicore.CodeInvalidArgument, msg, "Run 'unravis --help' to see the commands and options.")
}

// root builds the command tree of one run.
func (st *state) root() *cobra.Command {
	root := &cobra.Command{
		Use:           "unravis",
		Short:         "Unravis command line",
		Long:          "The Unravis command line. Commands that need no running app: version, schema, completion and blackbox inspect --offline.",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return c.Help()
		},
	}
	root.SetOut(st.io.Out)
	root.SetErr(st.io.Err)
	root.SetIn(st.io.In)
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error { return err })
	root.CompletionOptions.DisableDefaultCmd = false
	root.CompletionOptions.HiddenDefaultCmd = false
	f := root.PersistentFlags()
	f.StringVarP(&st.opts.output, "output", "o", "table", "output format: table, json or jsonl")
	f.BoolVar(&st.opts.json, "json", false, "same as -o json")
	f.DurationVar(&st.opts.timeout, "timeout", DefaultTimeout, "give up after this long (at most 1h)")
	f.BoolVarP(&st.opts.quiet, "quiet", "q", false, "no progress text on stderr")
	f.BoolVar(&st.opts.noColor, "no-color", false, "no colour (there is none yet)")
	f.StringVar(&st.opts.outputFile, "output-file", "", "write JSON output to a new file (json mode only)")
	f.BoolVar(&st.opts.overwrite, "overwrite", false, "with --output-file: replace an existing file")
	root.PersistentPreRunE = func(c *cobra.Command, args []string) error {
		st.flagSet = c.Flags().Changed("output") || c.Flags().Changed("json")
		if st.opts.timeout <= 0 || st.opts.timeout > MaxTimeout {
			return usage("--timeout must be more than 0 and at most 1h.")
		}
		if c.Flags().Changed("output") {
			if _, ok := clicore.ParseMode(st.opts.output); !ok {
				return clicore.NewError(clicore.CodeUnsupportedFormat, "The output format must be table, json or jsonl.", "")
			}
		}
		if !st.flagSet {
			if v := st.io.Getenv("UNRAVIS_OUTPUT"); v != "" {
				if _, ok := clicore.ParseMode(v); !ok {
					return clicore.NewError(clicore.CodeUnsupportedFormat, "UNRAVIS_OUTPUT must be table, json or jsonl.", "")
				}
			}
		}
		if st.opts.outputFile != "" && st.mode == clicore.ModeTable {
			return usage("--output-file needs -o json or -o jsonl.")
		}
		if st.opts.overwrite && st.opts.outputFile == "" {
			return usage("--overwrite goes with --output-file.")
		}
		st.ctx, st.cancel = context.WithTimeout(st.ctx, st.opts.timeout) // Run cancels it when the command ends
		return nil
	}
	root.AddCommand(st.versionCmd(), st.schemaCmd(), st.blackboxCmd())
	return root
}

// emit writes the result of a command: a JSON line (or a file) in json mode, the table in table mode, and an error for jsonl, which
// only the paging commands have.
func (st *state) emit(op string, data any, meta clicore.Meta, table func(io.Writer) error) error {
	switch st.mode {
	case clicore.ModeJSON:
		b, err := clicore.MarshalSuccess(op, st.reqID, data, meta)
		if err != nil {
			return clicore.NewError(clicore.CodeInternal, "The result could not be written.", "")
		}
		if st.opts.outputFile != "" {
			if e := clicore.WriteNewFile(st.opts.outputFile, b, st.opts.overwrite); e != nil {
				return e
			}
			return nil
		}
		_, err = st.io.Out.Write(b)
		return writeErr(err)
	case clicore.ModeJSONL:
		return clicore.NewError(clicore.CodeUnsupportedFormat, "This command has no JSON Lines form. Use -o json.", "")
	}
	return writeErr(table(st.io.Out))
}

func writeErr(err error) error {
	if err == nil {
		return nil
	}
	// A reader that closed the pipe: stop quietly with the stopped code (Run writes no error text for it).
	return errOutputClosed
}

var errOutputClosed = errors.New("the output was closed")

func fmtTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
