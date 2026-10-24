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
	"io"
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
	var w withholding
	if w.counts, err = suppressComplements(records); err != nil {
		return Export{}, err
	}

	body := Body{Records: records}
	if in.Options.IncludePseudonyms {
		var shown bool
		body.Pseudonyms, shown = buildPseudonyms(in.Key, in.Report.Stalls)
		w.pseudonyms = !shown
	}
	if in.Options.IncludeLedger {
		entries, err := buildLedger(in.Ledger, records, reportUserIDs(in.Report))
		if err != nil {
			return Export{}, err
		}
		body.Ledger = entries
	}

	e := Export{
		Manifest: Manifest{
			SchemaVersion: SchemaVersion,
			ToolVersion:   in.ToolVersion,
			CreatedAt:     in.Now.UTC(),
			Masking:       Masking{Statements: maskingStatements(in.Options, w)},
		},
		Body: body,
	}
	if e.Manifest.BodySHA256, err = exportHash(e); err != nil {
		return Export{}, err
	}
	// Last line of defence: no form of "proven" may reach a file, however it is
	// spelt (containsProven normalises before it looks).
	raw, err := Canonical(e)
	if err != nil {
		return Export{}, err
	}
	if containsProven(string(raw)) {
		return Export{}, fmt.Errorf("%w: export text contains \"proven\"", ErrInvalid)
	}
	return e, nil
}

// withholding records what Build hid as a whole, so the masking statements can
// say so.
type withholding struct {
	pseudonyms bool // the pseudonym table was asked for and withheld
	counts     bool // at least one record count is "withheld"
}

// buildLedger exports text, state and evidence references only. Notes, facts,
// guards and history are dropped. Only hypotheses that reference at least one
// record in this export are kept, so unrelated cases never ride along. Text is
// scrubbed of identifiers and raw user ids the report holds, then masked, or
// withheld when it still looks risky. Other references to records outside
// this export collapse into one "not in this export" entry.
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
		missing, related := false, false
		for _, id := range h.EvidenceIDs {
			switch {
			case exported[id]:
				related = true
				ids = append(ids, id)
			case !missing:
				missing = true
				ids = append(ids, notInExport)
			}
		}
		if !related {
			continue
		}
		out = append(out, LedgerEntry{Text: cleanFreeText(h.Text, userIDs, ledgerWithheld), State: h.State, EvidenceIDs: ids})
	}
	return out, nil
}

func maskingStatements(o Options, w withholding) []string {
	s := []string{
		"Left out by default: request and response payloads, raw URLs, query text, IP addresses, user agents, raw user IDs and source ids. Sources are named by a human label only.",
		fmt.Sprintf("Every count below %d is shown as \"<%d\", including counts inside headlines and known gaps. A finding with a count below %d is marked low n.", ThinCell, ThinCell, ThinCell),
	}
	if w.counts {
		s = append(s, fmt.Sprintf("A count shown as \"%s\" is %d or more. It is hidden so that subtracting it from another count shown cannot reveal a count below %d.", withheldText, ThinCell, ThinCell))
	}
	switch {
	case !o.IncludePseudonyms:
		s = append(s, "No user pseudonyms are included.")
	case w.pseudonyms:
		s = append(s, fmt.Sprintf("Pseudonym table withheld: a cell was below %d.", ThinCell))
	default:
		s = append(s, "Users are shown as keyed pseudonyms (user- followed by 8 hex characters). They are stable within this export only: each preview uses a new key, so the same user gets a different label in any other export.")
	}
	if o.IncludeLedger {
		s = append(s, fmt.Sprintf("Only hypotheses that reference a finding in this export are included, with their text, state and evidence references; notes and guard facts are not. In hypothesis text, identifiers, email addresses, IP addresses, host names, paths and links are replaced by \"[removed]\", every number below %d is shown as \"<%d\", and text that still looks like it holds an identifier is replaced by \"%s\".", ThinCell, ThinCell, ledgerWithheld))
	} else {
		s = append(s, "The hypothesis ledger is not included.")
	}
	return append(s, keyStatement, hashStatement)
}

// Closing statements shared by Build and BuildCase.
const (
	keyStatement  = "The key that makes pseudonyms is not in this file."
	hashStatement = "bodySha256 is the SHA-256 of this file's bytes with the bodySha256 value set to an empty string, so it covers the manifest and the body."
)

// exportHash is the hex SHA-256 of Canonical(e) with Manifest.BodySHA256 set to
// "". It therefore covers everything in the file except the hash itself:
// schemaVersion, toolVersion, createdAt, the masking statements and the body.
// To check a file by hand, replace the bodySha256 value with "" (keeping every
// other byte) and hash the result.
func exportHash(e Export) (string, error) {
	e.Manifest.BodySHA256 = ""
	raw, err := Canonical(e)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Canonical returns the exact bytes of the export file, which are also the
// bytes the manifest hash is computed over (with the hash value empty):
//   - UTF-8 JSON from encoding/json with struct field order as declared in
//     types.go, so key order is fixed;
//   - two-space indent, no prefix, "\n" line ends and one trailing "\n";
//   - HTML escaping off, so "<20" is written as is, never as "\u003c20";
//   - times in RFC 3339 UTC, as time.Time marshals them.
func Canonical(e Export) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(e); err != nil { // Encode appends the trailing newline
		return nil, err
	}
	return buf.Bytes(), nil
}

// Verify checks a file's bytes: exactly one JSON value with nothing after it,
// no unknown fields, a schema version from MinSchemaVersion to
// MaxSchemaVersion, a hash that matches the manifest and body, and the canonical encoding (so
// key spelling, order and spacing are the ones Build wrote and nothing can
// ride along unhashed).
//
// A file older than the case export (version 3) must not hold the case fields
// (manifest imports, body imports or events): that would be a downgrade that
// slips a case export past the case policy. A version 3 file is re-checked
// against the whole case export policy (verifyCase), so a recomputed hash does
// not make a planted address, causal kind or free text valid. Every record's
// version must equal the file's.
func Verify(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var e Export
	if err := dec.Decode(&e); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := dec.Decode(&json.RawMessage{}); err != io.EOF {
		return fmt.Errorf("%w: trailing data after the export", ErrInvalid)
	}
	v := e.Manifest.SchemaVersion
	if v < MinSchemaVersion || v > MaxSchemaVersion {
		return fmt.Errorf("%w: schema version %d, want %d to %d", ErrInvalid, v, MinSchemaVersion, MaxSchemaVersion)
	}
	if v < CaseSchemaVersion && (e.Manifest.Imports != nil || e.Body.Imports != nil || e.Body.Events != nil) {
		return fmt.Errorf("%w: a version %d file holds case export fields", ErrInvalid, v)
	}
	for _, r := range e.Body.Records {
		if r.Version != v {
			return fmt.Errorf("%w: record version %d in a version %d file", ErrInvalid, r.Version, v)
		}
	}
	sum, err := exportHash(e)
	if err != nil {
		return err
	}
	if sum != e.Manifest.BodySHA256 {
		return fmt.Errorf("%w: hash does not match the manifest and body", ErrInvalid)
	}
	canon, err := Canonical(e)
	if err != nil {
		return err
	}
	if !bytes.Equal(canon, raw) {
		return fmt.Errorf("%w: the file is not in canonical form", ErrInvalid)
	}
	if v == CaseSchemaVersion {
		return verifyCase(e, raw)
	}
	return nil
}
