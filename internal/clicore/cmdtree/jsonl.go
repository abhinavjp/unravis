package cmdtree

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
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
	jsonlMaxCursor      = 1024     // the longest cursor of the owner that is passed on
	jsonlEndReserve     = 1536     // room kept inside the output cap for the complete line (it holds one cursor)
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
		for k := range m {
			if strings.EqualFold(k, key) { // the owner refuses a name that comes twice in any case
				return nil, false
			}
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
	// "Limit" and "CURSOR" mean the same to the owner: this program writes both, so it takes them under the lower-case name.
	for k, v := range base {
		if lk := strings.ToLower(k); (lk == "limit" || lk == "cursor") && k != lk {
			delete(base, k)
			base[lk] = v
		}
	}
	if string(base["limit"]) == "null" {
		delete(base, "limit") // the same as no limit
	}
	page, firstAsIs := defaultPage, false
	if raw, has := base["limit"]; has {
		var n int
		if json.Unmarshal(raw, &n) == nil && n >= 1 {
			if n > defaultPage {
				return usage("--limit must be from 1 to " + strconv.Itoa(defaultPage) + " with -o jsonl.")
			}
			page = n
		} else {
			firstAsIs = true // the owner says what is wrong with it
		}
	}
	cursor := ""
	if raw, has := base["cursor"]; has {
		_ = json.Unmarshal(raw, &cursor) // a cursor that is not text is the owner's to refuse: it is sent as it is on the first page
	}
	seen := map[string]bool{cursor: true}
	warnings := map[string]bool{}
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
		ask := min(page, sf.maxRows-total)
		if !first || !firstAsIs {
			call["limit"] = json.RawMessage(strconv.Itoa(ask))
		} else {
			ask = sf.maxRows // a limit the owner is to judge: what it sends back must still fit the row cap
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
		if len(p.Rows) > ask {
			if first && firstAsIs {
				return clicore.NewError(clicore.CodeResultTooLarge, "The page is larger than --max-rows. Lower --limit or raise --max-rows.", "")
			}
			return clicore.NewError(clicore.CodeInternal, "The app sent more rows than were asked for.", "")
		}
		if p.NextCursor != "" && (len(p.Rows) == 0 || seen[p.NextCursor] || !plainCursor(p.NextCursor)) {
			return clicore.NewError(clicore.CodeInternal, "The app sent a page that does not move on. Try again.", "")
		}
		seen[p.NextCursor] = true
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
			if len(r) == 0 || r[0] != '{' {
				return clicore.NewError(clicore.CodeInternal, "The app sent a row that this command line cannot read.", "")
			}
			if len(b) > jsonlMaxLine {
				return clicore.NewError(clicore.CodeResultTooLarge, "One row is larger than 64 KiB.", "")
			}
			buf.Write(b)
		}
		if written+int64(buf.Len())+jsonlEndReserve > sf.maxBytes { // the complete line is part of the cap
			if wrote == 0 {
				return clicore.NewError(clicore.CodeResultTooLarge, "The first page is larger than --max-bytes. Raise it or lower --limit.", "")
			}
			partial, next = true, cursor // this page is not printed: continue from the cursor that fetched it
			break
		}
		if err := writeErr(writeContext(st.ctx, st.io.Out, buf.Bytes())); err != nil {
			return err
		}
		st.warnings(res.Meta.Warnings, warnings)
		written += int64(buf.Len())
		wrote++
		total += len(p.Rows)
		if pagePartial(res, p) {
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
	return writeErr(writeContext(st.ctx, st.io.Out, b))
}

// plainCursor tells whether the owner's cursor is short, printable ASCII without quote or backslash: it is written in the complete line,
// whose size is reserved inside the cap.
func plainCursor(c string) bool {
	if len(c) > jsonlMaxCursor {
		return false
	}
	for i := 0; i < len(c); i++ {
		if b := c[i]; b < 0x21 || b > 0x7e || b == '"' || b == '\\' {
			return false
		}
	}
	return true
}

// pagePartial tells whether a page makes the whole stream partial: a source that did not answer does. A page that was cut at its size
// (truncated, with a cursor to go on) does not: the next page goes on, and only the last page, which has no cursor, can say the list
// itself was cut.
func pagePartial(res *clicore.Result, p pageOut) bool {
	var probe struct {
		Sources []struct {
			OK *bool `json:"ok"`
		} `json:"sources"`
	}
	if json.Unmarshal(res.Data, &probe) == nil {
		for _, s := range probe.Sources {
			if s.OK != nil && !*s.OK {
				return true
			}
		}
	}
	return p.NextCursor == "" && res.Meta.Coverage == "partial"
}
