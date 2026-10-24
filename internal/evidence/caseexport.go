package evidence

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
)

// CaseImport is one imported file as the caller hands it to BuildCase. The
// fields mirror casestore.ImportRecord plus the acknowledgement text, so the
// API can copy a record across field by field (evidence cannot import
// casestore: casestore imports evidence).
type CaseImport struct {
	Importer string // importer name, e.g. "iis-w3c"
	// FileName is accepted so a caller can copy a store record as is. It is
	// never read and never written to an export.
	FileName  string
	SHA256    string // 64 lower-case hex characters
	HostLabel string // hosts.json label; exported only as a keyed pseudonym
	TZRule    string // exported only when it is a clock.Rule* value
	// Status is the store's import status. Only "ok" (or "", treated as ok)
	// imports are exported; any other status is skipped.
	Status         string
	Rows, Rejected int64
	Quality        SourceQuality
	KnownGaps      []string
	AckText        string // the gating banner text the analyst acknowledged
}

// CaseInput is everything BuildCase needs.
type CaseInput struct {
	Imports []CaseImport
	// Events are the case's events as the store returns them. Their host and
	// user values are the store's (already mapped and case-keyed); BuildCase
	// re-keys them under Key, so no stored pseudonym reaches the file.
	Events []Event
	// EventsTruncated says the caller stopped reading at a limit
	// (casestore.MaxEventQuery), so the file states its counts are lower
	// bounds.
	EventsTruncated bool
	ToolVersion     string
	Now             time.Time
	// Key is a fresh per-export key (NewKey), at least 16 bytes. It is never
	// written to the file; the caller should zero it after the call.
	Key []byte
}

// Fixed text that replaces cleaned free text which still looks risky.
const (
	ackWithheld   = "[acknowledgement text withheld]"
	gapWithheld   = "[gap withheld]"
	otherImporter = "other"
	otherKind     = "other"
	maxFreeText   = 1024 // longest acknowledgement or gap exported, in bytes
	maxTokenBytes = 64
	maxQualityLen = 256
)

// attrRule says how one event attribute is exported.
type attrRule int

const (
	ruleNumber attrRule = iota + 1 // a plain decimal number, else removed
	ruleToken                      // a short plain token, else removed
	ruleHex                        // up to 16 hex characters (a plan hash), else removed
	ruleName                       // a confidential name: keyed pseudonym
	rulePerson                     // a person: keyed pseudonym, all or nothing by the thin-cell rule
)

type attrPolicy struct {
	rule   attrRule
	prefix string   // pseudonym prefix for ruleName and rulePerson
	asIs   []string // ruleName values shown unchanged (e.g. "_Total")
}

var (
	polNum = attrPolicy{rule: ruleNumber}
	polTok = attrPolicy{rule: ruleToken}
)

func polName(prefix string, asIs ...string) attrPolicy {
	return attrPolicy{rule: ruleName, prefix: prefix, asIs: asIs}
}

func polPerson(prefix string) attrPolicy { return attrPolicy{rule: rulePerson, prefix: prefix} }

var (
	counterPolicy = map[string]attrPolicy{"object": polTok, "counter": polTok, "instance": polName("instance", "_Total")}
	schedPolicy   = map[string]attrPolicy{"job": polName("job"), "queue": polName("queue"), "tz": polTok, "basis": polTok,
		"reason": polTok, "dst": polTok, "freq": polTok}
)

// exportPolicy is the per-source attribute allowlist of the case export. It
// is narrower than the importers' own allowlists: an attribute or a source
// that is not listed here is left out of the file.
//
// Left out on purpose: the IIS URL stem ("stem"), the Application Insights
// operation name ("name", a URL path) and operation id ("operation_Id", a
// join key into the client's telemetry). Site, application pool, queue, job,
// namespace and counter instance names are client names and become keyed
// pseudonyms; "pool" is shared by HTTPERR queues and event-log app pools so
// the same pool reads the same within one export.
var exportPolicy = map[string]map[string]attrPolicy{
	"iis-w3c": {"method": polTok, "status": polNum, "substatus": polNum, "win32": polNum, "port": polNum,
		"site": polName("site"), "user": polPerson("user")},
	"iis-httperr":  {"reason": polTok, "status": polNum, "method": polTok, "queue": polName("pool"), "siteid": polNum},
	"win-counters": counterPolicy,
	"sql-pdh":      counterPolicy,
	"win-events":   {"log": polTok, "provider": polTok, "id": polNum, "app-pool": polName("pool"), "reason": polTok},
	"mongod-json": {"type": polTok, "ns": polName("ns"), "command": polTok, "planSummary": polTok, "keysExamined": polNum,
		"docsExamined": polNum, "nreturned": polNum, "numYields": polNum, "queryHash": {rule: ruleHex}, "workingMs": polNum},
	"schedule-hangfire": schedPolicy,
	"schedule-agent":    schedPolicy,
	"appinsights":       {"dependencyType": polTok, "resultCode": polTok, "success": polTok},
	"sql-xe": {"event": polTok, "wait_type": polTok, "wait_class": polTok, "component": polTok, "state": polTok,
		"error_number": polNum, "sql_severity": polNum},
	"sql-errorlog": {"error_number": polNum, "sql_severity": polNum},
}

var (
	importerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	fileHashHex  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	kindToken    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	numberValue  = regexp.MustCompile(`^-?[0-9]{1,15}(?:\.[0-9]{1,6})?$`)
	hexValue     = regexp.MustCompile(`^[0-9A-Fa-f]{1,16}$`)
	// plainToken starts with a letter, a digit, "%" or "#" (counter names such
	// as "% Processor Time"), so no token can open a spreadsheet formula, and
	// holds no quote, bracket, "@", "=" or "$".
	plainToken = regexp.MustCompile(`^[A-Za-z0-9%#][A-Za-z0-9 ._:/%()#,+\-]*$`)
	// strongCorrelation is the label importer and event text may never carry,
	// in any spelling ("strong correlation", "Strong_Correlation").
	strongCorrelation = regexp.MustCompile(`(?i)strong[\s_\-]*correlat`)
	unitToken         = regexp.MustCompile(`^[a-z][a-z-]{0,31}$`)
	dottedName        = regexp.MustCompile(`[A-Za-z0-9-]\.[A-Za-z]`)
)

// containsStrong reports "strong correlation" in any spelling, after
// normalisation.
func containsStrong(text string) bool {
	return strongCorrelation.MatchString(normalizeText(text))
}

// claimFree reports text that names no cause, no "proven" and no "strong
// correlation" (R8-B7).
func claimFree(text string) bool {
	return LintCausal(text) == nil && !containsProven(text) && !containsStrong(text)
}

// keyedLabel is prefix + "-" + the first 8 hex characters of
// HMAC-SHA256(key, prefix NUL value). The prefix is in the MAC, so the same
// value under two prefixes gives unrelated labels, and a new key gives an
// unrelated label for every value.
func keyedLabel(key []byte, prefix, value string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(prefix))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	return prefix + "-" + hex.EncodeToString(mac.Sum(nil))[:8]
}

// cleanToken returns v when it is a short plain token that scrubbing leaves
// unchanged and that carries no dotted name, no keyed number, no long digit
// run and no claim; otherwise "[removed]".
//
// DECISION: a dotted name ("web-a.example.test", "w3wp.exe") is removed
// whatever its top-level domain, since scrubText only knows common ones; a
// few harmless tokens are lost so that no host name rides along.
func cleanToken(v string) string {
	if len(v) > maxTokenBytes || !plainToken.MatchString(v) || dottedName.MatchString(v) ||
		scrubText(v) != v || removeUserIDs(v, nil) != v || residualRisk(v) || !claimFree(v) {
		return removedText
	}
	return v
}

// exportAttr applies one rule. Person values are returned as is here and
// re-keyed (or dropped) by applyPersonRule once every event is known.
func exportAttr(key []byte, p attrPolicy, v string) string {
	switch p.rule {
	case ruleNumber:
		if numberValue.MatchString(v) {
			return v
		}
	case ruleHex:
		if hexValue.MatchString(v) {
			return v
		}
	case ruleToken:
		return cleanToken(v)
	case ruleName:
		for _, s := range p.asIs {
			if v == s {
				return v
			}
		}
		return keyedLabel(key, p.prefix, v)
	case rulePerson:
		return v
	}
	return removedText
}

// caseWithholding records what BuildCase hid, for the masking statements.
type caseWithholding struct {
	rows        bool // an import row count is "withheld"
	personShown map[string]bool
	personHeld  map[string]bool
	unlisted    bool // events from a source without a policy were left out
	truncated   bool
}

// BuildCase assembles a redacted case export (schema version 2): one
// ExportImport and one ManifestImport per ok import, and one ExportEvent per
// event whose source has an export policy. Records is empty. The result is
// shown in full as a preview and written with Canonical; Verify accepts it.
//
// Rules:
//   - no file name, raw host name, raw user value, stored case pseudonym,
//     IP, URL, path or message reaches the file;
//   - hosts and named attributes are keyed pseudonyms under in.Key, so an
//     export under another key shares no pseudonym with this one;
//   - attributes follow exportPolicy; unlisted ones are left out, and values
//     that fail their rule are "[removed]";
//   - user pseudonyms are all or nothing (applyPersonRule);
//   - import counts below ThinCell print as "<20", and a row count is
//     withheld when its rejected count is masked (complementary suppression);
//   - the file never says "proven", "strong correlation" or names a cause.
//
// It returns an error wrapping ErrInvalid for input it cannot export safely:
// a short key, no creation time, an importer name or file hash of the wrong
// shape, a negative count, or an event that fails Event.Validate or whose
// evidence id does not name its source.
func BuildCase(in CaseInput) (Export, error) {
	if in.Now.IsZero() {
		return Export{}, fmt.Errorf("%w: no creation time", ErrInvalid)
	}
	if len(in.Key) < minKeyLen {
		return Export{}, fmt.Errorf("%w: the case export needs a per-export key", ErrInvalid)
	}
	w := caseWithholding{personShown: map[string]bool{}, personHeld: map[string]bool{}, truncated: in.EventsTruncated}

	manifestImports, bodyImports, err := exportImports(in.Key, in.Imports, &w)
	if err != nil {
		return Export{}, err
	}
	events, err := exportEvents(in.Key, in.Events, &w)
	if err != nil {
		return Export{}, err
	}

	e := Export{
		Manifest: Manifest{
			SchemaVersion: SchemaVersion,
			ToolVersion:   in.ToolVersion,
			CreatedAt:     in.Now.UTC(),
			Masking:       Masking{Statements: caseStatements(w)},
			Imports:       manifestImports,
		},
		Body: Body{Records: []Record{}, Imports: bodyImports, Events: events},
	}
	if e.Manifest.BodySHA256, err = exportHash(e); err != nil {
		return Export{}, err
	}
	// Last line of defence (R8-B7): nothing in the file may name a cause, say
	// "proven" or "strong correlation", however it is spelt.
	raw, err := Canonical(e)
	if err != nil {
		return Export{}, err
	}
	if !claimFree(string(raw)) {
		return Export{}, fmt.Errorf("%w: case export text carries a causal or proven claim", ErrInvalid)
	}
	return e, nil
}

// exportImports builds the manifest and body entries of every ok import, in
// the order given. Refs are "import-1", "import-2", ...
func exportImports(key []byte, imports []CaseImport, w *caseWithholding) ([]ManifestImport, []ExportImport, error) {
	var man []ManifestImport
	var body []ExportImport
	for _, im := range imports {
		if im.Status != "" && im.Status != "ok" {
			continue
		}
		if !importerName.MatchString(im.Importer) {
			return nil, nil, fmt.Errorf("%w: importer name has the wrong shape", ErrInvalid)
		}
		if !fileHashHex.MatchString(im.SHA256) {
			return nil, nil, fmt.Errorf("%w: file hash is not 64 lower-case hex characters", ErrInvalid)
		}
		if im.Rows < 0 || im.Rejected < 0 {
			return nil, nil, fmt.Errorf("%w: negative row count", ErrInvalid)
		}
		importer := im.Importer
		if exportPolicy[importer] == nil {
			importer = otherImporter
		}
		ref := "import-" + strconv.Itoa(len(body)+1)

		rejected := maskCount64(im.Rejected)
		rows := maskCount64(im.Rows)
		// The events of one import can be counted in the body, so a shown row
		// count minus those events would give back a masked rejected count.
		if rejected.Masked && !rows.Masked {
			rows = Count{Masked: true, Text: withheldText}
			w.rows = true
		}
		ei := ExportImport{Ref: ref, Importer: importer, TZRule: exportTZRule(im.TZRule),
			Rows: rows, Rejected: rejected, Quality: cleanQuality(im.Quality)}
		if im.HostLabel != "" {
			ei.Host = keyedLabel(key, "host", im.HostLabel)
		}
		body = append(body, ei)

		gaps := make([]string, 0, len(im.KnownGaps))
		for _, g := range im.KnownGaps {
			gaps = append(gaps, cleanCaseText(g, gapWithheld))
		}
		man = append(man, ManifestImport{Ref: ref, Importer: importer, SHA256: im.SHA256,
			Acknowledgement: cleanCaseText(im.AckText, ackWithheld), KnownGaps: gaps})
	}
	return man, body, nil
}

func exportTZRule(r string) string {
	switch r {
	case clock.RuleUTCField, clock.RuleOffsetInField, clock.RuleHeaderBias, clock.RuleHostLocal:
		return r
	}
	return ""
}

// cleanCaseText is the ledger treatment (scrub identifiers, mask small
// numbers, withhold residual risk) plus a length cap and the claim check.
// Text that carried a claim before cleaning is withheld whole rather than
// shown with the word removed, since what is left would read as a statement.
func cleanCaseText(text, withheld string) string {
	if text == "" {
		return ""
	}
	if len(text) > maxFreeText || !claimFree(text) {
		return withheld
	}
	out := cleanFreeText(text, nil, withheld)
	if !claimFree(out) {
		return withheld
	}
	return out
}

// cleanQuality keeps a quality string only when scrubbing leaves it unchanged
// and it carries no claim; times are copied.
func cleanQuality(q SourceQuality) SourceQuality {
	keep := func(s string) string {
		if len(s) > maxQualityLen || scrubText(s) != s || residualRisk(s) || !claimFree(s) {
			return ""
		}
		return s
	}
	out := SourceQuality{SourceVersion: keep(q.SourceVersion), Threshold: keep(q.Threshold),
		Sampling: keep(q.Sampling), TimezoneRule: keep(q.TimezoneRule)}
	if q.CoverageFirst != nil {
		t := q.CoverageFirst.UTC()
		out.CoverageFirst = &t
	}
	if q.CoverageLast != nil {
		t := q.CoverageLast.UTC()
		out.CoverageLast = &t
	}
	return out
}

// exportEvents converts, filters and sorts the events, then applies the
// person rule.
func exportEvents(key []byte, events []Event, w *caseWithholding) ([]ExportEvent, error) {
	out := make([]ExportEvent, 0, len(events))
	for i, ev := range events {
		pol := exportPolicy[ev.Source]
		if pol == nil {
			w.unlisted = true
			continue
		}
		own := make(map[string]bool, len(ev.Attrs))
		for k := range ev.Attrs {
			own[k] = true
		}
		if err := ev.Validate(own); err != nil {
			return nil, fmt.Errorf("%w: event %d: %v", ErrInvalid, i, err)
		}
		if err := checkCaseEventID(ev.EvidenceID, ev.Source); err != nil {
			return nil, fmt.Errorf("%w: event %d: %v", ErrInvalid, i, err)
		}
		x := ExportEvent{EvidenceID: ev.EvidenceID, Source: ev.Source, Kind: otherKind,
			Start: ev.Start, End: ev.End, Severity: ev.Severity, Line: ev.Line, TZRule: ev.TZRule}
		if kindToken.MatchString(ev.Kind) && claimFree(ev.Kind) {
			x.Kind = ev.Kind
		}
		if unitToken.MatchString(ev.Unit) {
			x.Unit = ev.Unit
		}
		if ev.Host != "" {
			x.Host = keyedLabel(key, "host", ev.Host)
		}
		if ev.DurationMs != nil {
			d := *ev.DurationMs
			x.DurationMs = &d
		}
		if ev.Value != nil {
			v := *ev.Value
			x.Value = &v
		}
		for k, v := range ev.Attrs {
			p, ok := pol[k]
			if !ok || v == "" {
				continue
			}
			if x.Attrs == nil {
				x.Attrs = map[string]string{}
			}
			x.Attrs[k] = exportAttr(key, p, v)
		}
		out = append(out, x)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].EvidenceID < out[j].EvidenceID
	})
	applyPersonRule(key, out, w)
	return out, nil
}

// checkCaseEventID accepts ev:<case tag>:<source>:<16 lower-case hex>, the
// shape EventID builds, without knowing the case id.
func checkCaseEventID(id, source string) error {
	rest, ok := strings.CutPrefix(id, "ev:")
	if !ok {
		return fmt.Errorf("evidence id is not an event id")
	}
	tag, rest, ok := strings.Cut(rest, ":")
	if !ok || tag == "" || len(tag) > 8 || !isLowerAlnum(tag) {
		return fmt.Errorf("evidence id case tag has the wrong shape")
	}
	hex16, ok := strings.CutPrefix(rest, source+":")
	if !ok || len(hex16) != 16 {
		return fmt.Errorf("evidence id does not name its source")
	}
	for i := 0; i < len(hex16); i++ {
		if c := hex16[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("evidence id hash has the wrong shape")
		}
	}
	return nil
}

func isLowerAlnum(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// personGroup is the tally of one person prefix ("user") over every event
// whose source's policy has a person attribute with that prefix.
type personGroup struct {
	perValue map[string]int
	none     int
}

// applyPersonRule re-keys person attributes or drops them, per prefix, all or
// nothing.
//
// DECISION (complementary suppression, mirroring buildPseudonyms): a reader
// can count the events of each pseudonym in the body, so each pseudonym is a
// cell. The attribute is kept on every event only when there are at least
// ThinCell distinct values, every value has at least ThinCell events, and the
// events without a value number 0 or at least ThinCell. Otherwise it is
// removed from every event of that prefix: masking only the small groups
// would leave them recoverable as the remainder.
func applyPersonRule(key []byte, events []ExportEvent, w *caseWithholding) {
	groups := map[string]*personGroup{}
	for _, ev := range events {
		for k, p := range exportPolicy[ev.Source] {
			if p.rule != rulePerson {
				continue
			}
			g := groups[p.prefix]
			if g == nil {
				g = &personGroup{perValue: map[string]int{}}
				groups[p.prefix] = g
			}
			if v := ev.Attrs[k]; v != "" && v != "-" {
				g.perValue[v]++
			} else {
				g.none++
			}
		}
	}
	keep := map[string]bool{}
	for prefix, g := range groups {
		ok := len(g.perValue) >= ThinCell && (g.none == 0 || g.none >= ThinCell)
		for _, n := range g.perValue {
			if n < ThinCell {
				ok = false
				break
			}
		}
		keep[prefix] = ok
		switch {
		case ok:
			w.personShown[prefix] = true
		case len(g.perValue) > 0:
			w.personHeld[prefix] = true
		}
	}
	for i := range events {
		ev := &events[i]
		for k, p := range exportPolicy[ev.Source] {
			if p.rule != rulePerson {
				continue
			}
			v, present := ev.Attrs[k]
			if !present {
				continue
			}
			if keep[p.prefix] && v != "-" {
				ev.Attrs[k] = keyedLabel(key, p.prefix, v)
			} else {
				delete(ev.Attrs, k)
			}
		}
		if len(ev.Attrs) == 0 {
			ev.Attrs = nil
		}
	}
}

func caseStatements(w caseWithholding) []string {
	s := []string{
		"This file holds the imported events and import summaries of one case. File names, raw host names, IP addresses, URLs and paths, query and SQL text, user agents, cookies, referers, messages and raw user names are not in it. Each imported file is named by its importer and the SHA-256 of its bytes only.",
		fmt.Sprintf("An event attribute is kept only when its source's export policy lists it; every other attribute, and every event from a source without a policy, is left out. A kept text value that is not a short plain token (at most %d bytes, no dotted name, no long digit run, no identifier) is shown as \"%s\".", maxTokenBytes, removedText),
		"Hosts, sites, application pools, queues, jobs, namespaces and counter instances are shown as keyed pseudonyms (a prefix such as host- followed by 8 hex characters). The key is made for this export only, so the same name gets a different label in any other export.",
	}
	shown, held := sortedKeys(w.personShown), sortedKeys(w.personHeld)
	for _, p := range shown {
		s = append(s, fmt.Sprintf("Each %[1]s is shown as a keyed pseudonym (%[1]s- followed by 8 hex characters) made with this export's key. The pseudonyms stored with the case do not appear. Every %[1]s shown has %[2]d or more events.", p, ThinCell))
	}
	for _, p := range held {
		s = append(s, fmt.Sprintf("The %[1]s attribute is left out of every event: fewer than %[2]d of them were present, one had fewer than %[2]d events, or 1 to %[3]d events had none. Showing it would let a reader count a group below %[2]d.", p, ThinCell, ThinCell-1))
	}
	if len(shown) == 0 && len(held) == 0 {
		s = append(s, "No user pseudonyms are included.")
	}
	s = append(s, fmt.Sprintf("Every import row count and rejected-row count below %d is shown as \"<%d\", including counts inside known gaps.", ThinCell, ThinCell))
	if w.rows {
		s = append(s, fmt.Sprintf("A row count shown as \"%s\" is %d or more. It is hidden so that subtracting the events of that import cannot reveal a rejected-row count below %d.", withheldText, ThinCell, ThinCell))
	}
	if w.unlisted {
		s = append(s, "Some events came from a source without an export policy and are left out.")
	}
	if w.truncated {
		s = append(s, "Not every event of the case is in this file: the event list stops at the read limit, so counts taken from it are lower bounds.")
	}
	return append(s,
		fmt.Sprintf("Acknowledgements and known gaps are cleaned like hypothesis text: identifiers, email addresses, IP addresses, host names, paths and links become \"%s\", and text that still looks like it holds an identifier is replaced by \"%s\" or \"%s\".", removedText, ackWithheld, gapWithheld),
		"Evidence ids, times and source line numbers are as stored, so the same event can be matched across two exports of one case even though its pseudonyms differ.",
		keyStatement,
		hashStatement,
	)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// EventsCSV writes the events of a case export as CSV: a header row, then one
// row per event with attributes as "key=value" pairs joined by "; " in key
// order. Every cell goes through Cell, so no cell can start a formula.
func EventsCSV(e Export) ([]byte, error) {
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	row := func(cells ...string) error {
		for i, c := range cells {
			cells[i] = Cell(c)
		}
		return cw.Write(cells)
	}
	if err := row("evidenceId", "source", "kind", "host", "start", "end", "durationMs", "value",
		"unit", "severity", "line", "tzRule", "attrs"); err != nil {
		return nil, err
	}
	for _, ev := range e.Body.Events {
		keys := make([]string, 0, len(ev.Attrs))
		for k := range ev.Attrs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		pairs := make([]string, len(keys))
		for i, k := range keys {
			pairs[i] = k + "=" + ev.Attrs[k]
		}
		if err := row(ev.EvidenceID, ev.Source, ev.Kind, ev.Host, ev.Start.Format(time.RFC3339Nano),
			ev.End.Format(time.RFC3339Nano), fmtFloat(ev.DurationMs), fmtFloat(ev.Value), ev.Unit,
			ev.Severity, strconv.FormatInt(ev.Line, 10), ev.TZRule, strings.Join(pairs, "; ")); err != nil {
			return nil, err
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func fmtFloat(p *float64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}
