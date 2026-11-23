package cmdtree

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/clioffline"
)

func (st *state) blackboxCmd() *cobra.Command {
	group := &cobra.Command{
		Use:   "blackbox",
		Short: "Blackbox recordings",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, args []string) error { return c.Help() },
	}
	var (
		offline  bool
		input    string
		from, to string
		maxBytes int64
		samples  int
	)
	inspect := &cobra.Command{
		Use:         "inspect --offline --input PATH",
		Short:       "Read a Blackbox recording from disk, with no running app, and say what it holds",
		Long:        "Reads a recordings folder, or one hourly file (bb-YYYYMMDD-HH.ndjson), read-only. It never starts a service, never writes, and refuses a folder that holds case data. Copy a recording to a folder of its own first if it sits next to cases.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{opAnnotation: "blackbox.inspect"},
		RunE: func(c *cobra.Command, args []string) error {
			if !offline {
				return usage("blackbox inspect needs --offline: it reads files and does not ask the running app.")
			}
			if input == "" {
				return usage("blackbox inspect needs --input PATH.")
			}
			opt := clioffline.Options{MaxBytes: maxBytes, Samples: samples}
			var err error
			if opt.From, err = parseTime(from, "--from"); err != nil {
				return err
			}
			if opt.To, err = parseTime(to, "--to"); err != nil {
				return err
			}
			if c.Flags().Changed("max-bytes") && maxBytes <= 0 {
				return usage("--max-bytes must be more than 0.")
			}
			res, e := clioffline.Inspect(st.ctx, input, opt)
			if e != nil {
				return e
			}
			meta := clicore.Meta{Coverage: "complete", Identifiers: "shown", Warnings: res.Warnings}
			if len(res.Warnings) > 0 {
				meta.Coverage = "partial"
			}
			return st.emit("blackbox.inspect", res, meta, func(w io.Writer) error { return inspectTable(w, res) })
		},
	}
	f := inspect.Flags()
	f.BoolVar(&offline, "offline", false, "read the files directly (required)")
	f.StringVar(&input, "input", "", "a recordings folder, or one hourly file")
	f.StringVar(&from, "from", "", "start of the window, RFC 3339 (default: the whole recording)")
	f.StringVar(&to, "to", "", "end of the window, RFC 3339, exclusive")
	f.Int64Var(&maxBytes, "max-bytes", clioffline.DefaultMaxBytes, "most bytes of recording files to read")
	f.IntVar(&samples, "samples", 0, "also print the first N samples (0 to 1000)")
	group.AddCommand(inspect)
	return group
}

func parseTime(v, flag string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, usage(flag + " must be a time like 2026-11-23T09:00:00Z.")
	}
	return t, nil
}

func inspectTable(w io.Writer, r *clioffline.Result) error {
	rows := [][]string{{"input", r.Input}, {"kind", r.Kind}}
	if m := r.Manifest; m != nil {
		rows = append(rows, []string{"host", m.Host}, []string{"state", m.State}, []string{"files", strconv.Itoa(m.Files)},
			[]string{"gaps", strconv.Itoa(m.Gaps)}, []string{"events", strconv.Itoa(m.Events)},
			[]string{"manifest samples", strconv.Itoa(m.Samples)}, []string{"first sample", fmtTime(m.FirstSample)}, []string{"last sample", fmtTime(m.LastSample)})
	}
	rows = append(rows, []string{"samples read", strconv.Itoa(r.Samples.Count)}, []string{"from", fmtTime(r.Samples.From)}, []string{"to", fmtTime(r.Samples.To)})
	if err := clicore.WriteTable(w, []string{"FIELD", "VALUE"}, rows); err != nil {
		return err
	}
	for _, s := range r.Warnings {
		if _, err := fmt.Fprintln(w, "Warning: "+clicore.Printable(s)); err != nil {
			return err
		}
	}
	if len(r.Samples.First) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	srows := make([][]string, len(r.Samples.First))
	for i, s := range r.Samples.First {
		cpu := ""
		if s.CPU != nil {
			cpu = strconv.FormatFloat(*s.CPU, 'f', 1, 64)
		}
		srows[i] = []string{s.T.UTC().Format(time.RFC3339), s.Run, strconv.FormatUint(s.Seq, 10), cpu}
	}
	return clicore.WriteTable(w, []string{"TIME", "RUN", "SEQ", "CPU%"}, srows)
}
