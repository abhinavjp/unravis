package evidence

import (
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/ledger"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrInvalid is returned for an Input that cannot be exported.
var ErrInvalid = errors.New("invalid export input")

// Input is everything Build needs. Key is the per-case pseudonym key; it is used
// to derive pseudonyms and is never written to any output.
type Input struct {
	Report       audit.PatternReport
	SourceLabels map[string]string // source id -> human label; ids never appear in an export
	Ledger       []ledger.Hypothesis
	Options      Options
	ToolVersion  string
	Now          time.Time
	Key          []byte
}

const keyLen = 32

// NewKey returns a fresh random 32-byte per-case key.
func NewKey() ([]byte, error) {
	k := make([]byte, keyLen)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, nil
}

// Build assembles the preview/export. It reads only the finished report, the
// source labels and (when chosen) the ledger. Every count is masked, every
// headline is rebuilt from structured data, and nothing carries a payload, raw
// URL, query text, IP, user agent, raw user ID, source id or the key.
func Build(in Input) (Export, error) {
	if in.Now.IsZero() {
		return Export{}, fmt.Errorf("%w: no creation time", ErrInvalid)
	}
	start, end, err := reportWindow(in.Report)
	if err != nil {
		return Export{}, err
	}
	if in.Options.IncludePseudonyms && len(in.Key) < minKeyLen {
		return Export{}, fmt.Errorf("%w: pseudonyms need a per-case key", ErrInvalid)
	}
	source := sourceLabel(in.Report, in.SourceLabels)

	records := make([]Record, 0, len(in.Report.Findings))
	for _, f := range in.Report.Findings {
		rec, err := buildRecord(f, in.Report, source, start, end)
		if err != nil {
			return Export{}, err
		}
		records = append(records, rec)
	}

	body := Body{Records: records}
	if in.Options.IncludePseudonyms {
		body.Pseudonyms = buildPseudonyms(in.Key, in.Report.Stalls)
	}
	if in.Options.IncludeLedger {
		entries, err := buildLedger(in.Ledger, records, reportUserIDs(in.Report))
		if err != nil {
			return Export{}, err
		}
		body.Ledger = entries
	}

	sum, err := bodyHash(body)
	if err != nil {
		return Export{}, err
	}
	e := Export{
		Manifest: Manifest{
			SchemaVersion: SchemaVersion,
			ToolVersion:   in.ToolVersion,
			CreatedAt:     in.Now.UTC(),
			BodySHA256:    sum,
			Masking:       Masking{Statements: maskingStatements(in.Options)},
		},
		Body: body,
	}
	// Last line of defence: no form of "proven" may reach a file.
	raw, err := Canonical(e)
	if err != nil {
		return Export{}, err
	}
	if containsProven(string(raw)) {
		return Export{}, fmt.Errorf("%w: export text contains \"proven\"", ErrInvalid)
	}
	return e, nil
}

// buildLedger exports text, state and evidence references only. Notes, facts,
// guards and history are dropped. Text is scrubbed of identifiers and raw user
// ids the report holds, then masked.
// References to records outside this export collapse into one
// "not in this export" entry.
func buildLedger(hs []ledger.Hypothesis, records []Record, userIDs map[int]bool) ([]LedgerEntry, error) {
	exported := map[string]bool{}
	for _, r := range records {
		exported[r.EvidenceID] = true
	}
	out := make([]LedgerEntry, 0, len(hs))
	for _, h := range hs {
		switch h.State {
		case ledger.StateOpen, ledger.StateSupported, ledger.StateStrong, ledger.StateContradicted, ledger.StateNotTestable:
		default:
			return nil, fmt.Errorf("%w: ledger state %q", ErrInvalid, h.State)
		}
		ids := make([]string, 0, len(h.EvidenceIDs))
		missing := false
		for _, id := range h.EvidenceIDs {
			switch {
			case exported[id]:
				ids = append(ids, id)
			case !missing:
				missing = true
				ids = append(ids, notInExport)
			}
		}
		out = append(out, LedgerEntry{Text: cleanFreeText(h.Text, userIDs), State: h.State, EvidenceIDs: ids})
	}
	return out, nil
}

func maskingStatements(o Options) []string {
	s := []string{
		"Left out by default: request and response payloads, raw URLs, query text, IP addresses, user agents, raw user IDs and source ids. Sources are named by a human label only.",
		fmt.Sprintf("Every count below %d is shown as \"<%d\", including counts inside headlines and known gaps.", ThinCell, ThinCell),
	}
	if o.IncludePseudonyms {
		s = append(s, "Users are shown as keyed pseudonyms (user- followed by 8 hex characters). They are stable within this case and cannot be linked to other cases.")
	} else {
		s = append(s, "No user pseudonyms are included.")
	}
	if o.IncludeLedger {
		s = append(s, fmt.Sprintf("Hypothesis text, state and evidence references are included; notes and guard facts are not. In hypothesis text, identifiers, email addresses, IP addresses and links are replaced by \"[removed]\" and every number below %d is shown as \"<%d\".", ThinCell, ThinCell))
	} else {
		s = append(s, "The hypothesis ledger is not included.")
	}
	return append(s, "The key that makes pseudonyms is not in this file.")
}

// bodyHash is the hex SHA-256 of json.Marshal(Body): compact, struct field order.
func bodyHash(b Body) (string, error) {
	raw, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Canonical returns the exact bytes of the export file (indented JSON, stable key order).
func Canonical(e Export) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // keep "<20" readable instead of "<20"
	enc.SetIndent("", "  ")
	if err := enc.Encode(e); err != nil { // Encode appends the trailing newline
		return nil, err
	}
	return buf.Bytes(), nil
}

// Verify re-parses a file's bytes and recomputes the body hash against the
// manifest. Unknown fields are refused, so nothing can ride along unhashed
// inside the body.
func Verify(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var e Export
	if err := dec.Decode(&e); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if dec.More() {
		return fmt.Errorf("%w: trailing data after the export", ErrInvalid)
	}
	if e.Manifest.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: schema version %d, want %d", ErrInvalid, e.Manifest.SchemaVersion, SchemaVersion)
	}
	sum, err := bodyHash(e.Body)
	if err != nil {
		return err
	}
	if sum != e.Manifest.BodySHA256 {
		return fmt.Errorf("%w: body hash does not match the manifest", ErrInvalid)
	}
	return nil
}
