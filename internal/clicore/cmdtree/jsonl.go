package cmdtree

import (
	"bytes"
	"encoding/json"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"api-audit-explorer/internal/clicore"
)

// JSON Lines streaming (spec 05 3.3): `audit requests` and `audit slowest` with -o jsonl. The command line pages through the owner's
// cursor itself, one page in flight, so the owner needs no new protocol and a slow reader holds back the next page (backpressure).
const (
	jsonlAnnotation     = "unravis.jsonl" // the command has a JSON Lines form
	jsonlMaxLine        = 64 << 10        // one line, in bytes
	jsonlDefaultMaxRows = 1000
	jsonlMaxRows        = 10000
	jsonlMaxBytes       = 64 << 20 // the output cap; --max-bytes only lowers it
)

type streamLine struct {
	SchemaVersion string          `json:"schemaVersion"`
	Operation     string          `json:"operation"`
	RequestID     string          `json:"requestId"`
	Type          string          `json:"type"`
	Data          json.RawMessage `json:"data,omitempty"`
}

type streamEnd struct {
	SchemaVersion string `json:"schemaVersion"`
	Operation     string `json:"operation"`
	RequestID     string `json:"requestId"`
	Type          string `json:"type"`
	Coverage      string `json:"coverage"`
	NextCursor    any    `json:"nextCursor"` // null, or the cursor to continue from
	Rows          int    `json:"rows"`
}

// streamFlags are the two limits of a stream, read from the flags of the command.
type streamFlags struct {
	maxRows  int
	maxBytes int64
}

func (sf *streamFlags) register(c *cobra.Command) {
	c.Flags().IntVar(&sf.maxRows, "max-rows", jsonlDefaultMaxRows, "with -o jsonl: most rows to print, 1 to 10000")
	c.Flags().Int64Var(&sf.maxBytes, "max-bytes", jsonlMaxBytes, "with -o jsonl: most bytes to print, at most 64 MiB")
}

// check refuses a limit that does not fit, and a limit in a mode that has no stream.
func (sf *streamFlags) check(c *cobra.Command, jsonl bool) error {
	set := c.Flags().Changed("max-rows") || c.Flags().Changed("max-bytes")
	if set && !jsonl {
		return usage("--max-rows and --max-bytes go with -o jsonl.")
	}
	if sf.maxRows < 1 || sf.maxRows > jsonlMaxRows {
		return usage("--max-rows must be from 1 to 10000.")
	}
	if sf.maxBytes < 1 || sf.maxBytes > jsonlMaxBytes {
		return usage("--max-bytes must be from 1 to 67108864.")
	}
	return nil
}

// objectFields splits one JSON object into its fields. It refuses text that is not an object and a name that comes twice (the owner
// refuses those too, and this program writes the fields again, so a repeated name must not vanish here).
func objectFields(b json.RawMessage) (map[string]json.RawMessage, bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false
	}
	m := map[string]json.RawMessage{}
	for dec.More() {
		kt, err := dec.Token()
		key, ok := kt.(string)
		if err != nil || !ok {
			return nil, false
		}
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			return nil, false
		}
		if _, dup := m[key]; dup {
			return nil, false
		}
		m[key] = v
	}
	return m, true
}

type pageOut struct {
	Rows       []json.RawMessage `json:"rows"`
	NextCursor string            `json:"nextCursor"`
}

// streamPages runs op page after page and prints one line per row. defaultPage is the page size when the arguments name none.
// A row or byte cap ends the stream with coverage "partial" and the cursor to continue from; a failure ends it with an error and no
// "complete" line (the lines before it are provisional).
func (st *state) streamPages(op string, args json.RawMessage, defaultPage int, sf streamFlags) error {
	base, ok := objectFields(args)
	if !ok {
		return usage("The request must be one JSON object with no repeated names.")
	}
	page, firstAsIs := defaultPage, false
	if raw, has := base["limit"]; has {
		var n int
		if json.Unmarshal(raw, &n) == nil && n >= 1 {
			page = n
		} else {
			firstAsIs = true // the owner says what is wrong with it
		}
	}
	cursor := ""
	if raw, has := base["cursor"]; has {
		_ = json.Unmarshal(raw, &cursor) // a cursor that is not text is the owner's to refuse: it is sent as it is on the first page
	}
	sess, e := st.attach(st.ctx)
	if e != nil {
		return e
	}
	var (
		total, wrote int
		written      int64
		partial      bool
		next         string
	)
	for first := true; ; first = false {
		call := make(map[string]json.RawMessage, len(base)+2)
		for k, v := range base {
			call[k] = v
		}
		if !(first && firstAsIs) {
			call["limit"] = json.RawMessage(strconv.Itoa(min(page, sf.maxRows-total)))
		}
		if !first {
			call["cursor"], _ = json.Marshal(cursor)
		}
		ab, err := json.Marshal(call)
		if err != nil {
			return clicore.NewError(clicore.CodeInternal, "The request could not be built.", "")
		}
		callID, err := newCallID()
		if err != nil {
			return clicore.NewError(clicore.CodeInternal, "No random source for the call id.", "")
		}
		dl := st.opts.timeout
		if d, has := st.ctx.Deadline(); has {
			dl = min(dl, max(time.Until(d), time.Millisecond))
		}
		res, e := sess.Do(st.ctx, clicore.Call{Op: op, Args: ab, RequestID: st.reqID, CallID: callID, Deadline: dl})
		if e != nil {
			return e
		}
		var p pageOut
		if json.Unmarshal(res.Data, &p) != nil || p.Rows == nil {
			return clicore.NewError(clicore.CodeInternal, "The app sent a page that this command line cannot read.", "")
		}
		if p.NextCursor != "" && (len(p.Rows) == 0 || p.NextCursor == cursor) {
			return clicore.NewError(clicore.CodeInternal, "The app sent a page that does not move on. Try again.", "")
		}
		var buf bytes.Buffer
		if first {
			b, err := clicore.ASCIIJSON(streamLine{clicore.SchemaVersion, op, st.reqID, "meta", nil})
			if err != nil {
				return clicore.NewError(clicore.CodeInternal, "The result could not be written.", "")
			}
			buf.Write(b)
		}
		for _, r := range p.Rows {
			b, err := clicore.ASCIIJSON(streamLine{clicore.SchemaVersion, op, st.reqID, "item", r})
			if err != nil {
				return clicore.NewError(clicore.CodeInternal, "The result could not be written.", "")
			}
			if len(b) > jsonlMaxLine {
				return clicore.NewError(clicore.CodeResultTooLarge, "One row is larger than 64 KiB.", "")
			}
			buf.Write(b)
		}
		if written+int64(buf.Len()) > sf.maxBytes {
			if wrote == 0 {
				return clicore.NewError(clicore.CodeResultTooLarge, "The first page is larger than --max-bytes. Raise it or lower --limit.", "")
			}
			partial, next = true, cursor // this page is not printed: continue from the cursor that fetched it
			break
		}
		if _, err := st.io.Out.Write(buf.Bytes()); err != nil {
			return writeErr(err)
		}
		written += int64(buf.Len())
		wrote++
		total += len(p.Rows)
		if res.Meta.Coverage == "partial" {
			partial = true
		}
		next = p.NextCursor
		if next == "" || total >= sf.maxRows {
			break
		}
		cursor = next
	}
	end := streamEnd{clicore.SchemaVersion, op, st.reqID, "complete", "complete", nil, total}
	if partial || next != "" {
		end.Coverage = "partial"
	}
	if next != "" {
		end.NextCursor = next
	}
	b, err := clicore.ASCIIJSON(end)
	if err != nil {
		return clicore.NewError(clicore.CodeInternal, "The result could not be written.", "")
	}
	_, err = st.io.Out.Write(b)
	return writeErr(err)
}
