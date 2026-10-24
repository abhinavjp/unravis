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
	FileName string
	// SHA256 must have the shape of a file hash (64 lower-case hex characters)
	// but is never written to an export: a hash of the client's file would let
	// anyone holding that file prove it is the one behind the export.
	SHA256    string
	HostLabel string // hosts.json label; exported only as a keyed pseudonym
	TZRule    string // exported only when it is a clock.Rule* value
	// Status is the store's import status. Only "ok" (or "", treated as ok)
	// imports are exported; any other status is skipped.
	Status         string
	Rows, Rejected int64
	Quality        SourceQuality
	KnownGaps      []string
	// AckText is the gating banner text the analyst acknowledged. It is client
	// form text and is never written; only bannerID(AckText) is.
	AckText string
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
	// ToolVersion is written only when it looks like a version number.
	ToolVersion string
	Now         time.Time
	// Key is a fresh per-export key (NewKey), at least 16 bytes. It is used for
	// the labels of this export and nothing else, and is never written to the
	// file; the caller should zero it after the call.
	Key []byte
}

// Fixed text that replaces cleaned free text which still looks risky.
const (
	gapWithheld   = "[gap withheld]"
	otherImporter = "other"
	otherKind     = "other"
	maxFreeText   = 1024 // longest gap exported, in bytes
	// labelHex is the number of hex characters in a keyed label; eventLabelHex
	// is the same for an event id, of which a file holds many.
	labelHex      = 12
	eventLabelHex = 16
	maxStatements = 32
	maxStatement  = 2048
)

// attrRule says how one event attribute is exported.
type attrRule int

const (
	ruleNumber   attrRule = iota + 1 // a plain decimal number (or an extra literal), else removed
	ruleEnum                         // one of a fixed list, exported in the listed spelling, else removed
	ruleEnumList                     // a comma-separated list of a fixed list
	ruleWait                         // an SQL Server wait type from the fixed list or family
	ruleObject                       // a performance counter object from the fixed list
	ruleName                         // a confidential name: keyed pseudonym
	rulePerson                       // a person: keyed pseudonym, all or nothing by the thin-cell rule
)

type attrPolicy struct {
	rule   attrRule
	prefix string            // pseudonym prefix for ruleName and rulePerson
	asIs   []string          // ruleName values shown unchanged (e.g. "_Total")
	enum   map[string]string // ruleEnum, ruleEnumList: lower-case -> exported spelling
	canon  map[string]bool   // exported spellings of enum
	extra  map[string]bool   // ruleNumber: literals allowed besides a number
}

var polNum = attrPolicy{rule: ruleNumber}

func polNumOr(extra ...string) attrPolicy { return attrPolicy{rule: ruleNumber, extra: set(extra...)} }

func polEnum(m map[string]string) attrPolicy {
	return attrPolicy{rule: ruleEnum, enum: m, canon: canonicalValues(m)}
}

func polList(m map[string]string) attrPolicy {
	return attrPolicy{rule: ruleEnumList, enum: m, canon: canonicalValues(m)}
}

var (
	polWait   = attrPolicy{rule: ruleWait}
	polObject = attrPolicy{rule: ruleObject}
)

func polName(prefix string, asIs ...string) attrPolicy {
	return attrPolicy{rule: ruleName, prefix: prefix, asIs: asIs}
}

func polPerson(prefix string) attrPolicy { return attrPolicy{rule: rulePerson, prefix: prefix} }

var (
	counterPolicy = map[string]attrPolicy{"object": polObject, "counter": polEnum(counterNames),
		"instance": polName("instance", "_Total")}
	schedPolicy = map[string]attrPolicy{"job": polName("job"), "queue": polName("queue"), "tz": polEnum(schedTZ),
		"basis": polEnum(schedBasis), "reason": polEnum(schedReasons), "dst": polEnum(schedDST), "freq": polEnum(schedFreqs)}
)

// exportPolicy is the per-source attribute allowlist of the case export. It
// is narrower than the importers' own allowlists: an attribute or a source
// that is not listed here is left out of the file. Every kept value is a
// number, a member of a fixed list, or a keyed pseudonym; a value that is none
// of these is shown as "[removed]". No free-form token survives.
//
// Left out on purpose (exportDropped): the IIS URL stem ("stem"), the
// Application Insights operation name ("name", a URL path) and operation id
// ("operation_Id", a join key into the client's telemetry). Site, application
// pool, queue, job, namespace, query hash and counter instance names are
// client names and become keyed pseudonyms; "pool" is shared by HTTPERR queues
// and event-log app pools so the same pool reads the same within one export.
var exportPolicy = map[string]map[string]attrPolicy{
	"iis-w3c": {"method": polEnum(httpMethods), "status": polNum, "substatus": polNum, "win32": polNum, "port": polNum,
		"site": polName("site"), "user": polPerson("user")},
	"iis-httperr":  {"reason": polEnum(httpErrReasons), "status": polNum, "method": polEnum(httpMethods), "queue": polName("pool"), "siteid": polNum},
	"win-counters": counterPolicy,
	"sql-pdh":      counterPolicy,
	"win-events": {"log": polEnum(winEventLogs), "provider": polEnum(winEventProviders), "id": polNum,
		"app-pool": polName("pool"), "reason": polEnum(winEventReasons)},
	"mongod-json": {"type": polEnum(mongoTypes), "ns": polName("ns"), "command": polEnum(mongoCommands),
		"planSummary": polList(mongoStages), "keysExamined": polNum, "docsExamined": polNum, "nreturned": polNum,
		"numYields": polNum, "queryHash": polName("qh"), "workingMs": polNum},
	"schedule-hangfire": schedPolicy,
	"schedule-agent":    schedPolicy,
	"appinsights": {"dependencyType": polEnum(dependencyTypes), "resultCode": polNumOr("other"),
		"success": polEnum(successValues)},
	"sql-xe": {"event": polEnum(xeEvents), "wait_type": polWait, "wait_class": polEnum(xeWaitClasses),
		"component": polEnum(xeComponents), "state": polEnum(xeStates), "error_number": polNum, "sql_severity": polNum},
	"sql-errorlog": {"error_number": polNum, "sql_severity": polNum},
}

var (
	importerName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	fileHashHex  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	numberValue  = regexp.MustCompile(`^-?[0-9]{1,15}(?:\.[0-9]{1,6})?$`)
	importRef    = regexp.MustCompile(`^import-[0-9]{1,6}$`)
)

// claimFree reports text that names no cause, no "proven" and no "strong
// correlation" (R8-B7), after the fold of claimNorm.
func claimFree(text string) bool {
	return LintCausal(text) == nil && !containsProven(text) && !containsStrong(text)
}

// keyedLabel is prefix + "-" + the first labelHex hex characters of
// HMAC-SHA256(key, prefix NUL value). The prefix is in the MAC, so the same
// value under two prefixes gives unrelated labels, and a new key gives an
// unrelated label for every value.
func keyedLabel(key []byte, prefix, value string) string {
	return keyedLabelN(key, prefix, value, labelHex)
}

func keyedLabelN(key []byte, prefix, value string, n int) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(prefix))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	return prefix + "-" + hex.EncodeToString(mac.Sum(nil))[:n]
}

// labelOK reports whether v is a keyed label of the given prefix with n hex
// characters.
func labelOK(prefix, v string, n int) bool {
	rest, ok := strings.CutPrefix(v, prefix+"-")
	if !ok || len(rest) != n {
		return false
	}
	for i := 0; i < len(rest); i++ {
		if c := rest[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// exportAttr applies one rule. Person values are returned as is here and
// re-keyed (or dropped) by applyPersonRule once every event is known.
func exportAttr(key []byte, p attrPolicy, v string) string {
	switch p.rule {
	case ruleNumber:
		if numberValue.MatchString(v) {
			return v
		}
		if lo := strings.ToLower(v); p.extra[lo] {
			return lo
		}
	case ruleEnum:
		if c, ok := p.enum[strings.ToLower(v)]; ok {
			return c
		}
	case ruleEnumList:
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			c, ok := p.enum[strings.ToLower(strings.TrimSpace(part))]
			if !ok {
				return removedText
			}
			out = append(out, c)
		}
		return strings.Join(out, ",")
	case ruleWait:
		if waitTypeOK(v) {
			return v
		}
	case ruleObject:
		if c, ok := canonCounterObject(v); ok {
			return c
		}
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

// attrValueOK is the check Verify applies to an exported attribute value: it
// accepts exactly what exportAttr (and applyPersonRule) can produce.
func attrValueOK(p attrPolicy, v string) bool {
	switch p.rule {
	case ruleNumber:
		return v == removedText || numberValue.MatchString(v) || p.extra[v]
	case ruleEnum:
		return v == removedText || p.canon[v]
	case ruleEnumList:
		if v == removedText {
			return true
		}
		for _, part := range strings.Split(v, ",") {
			if !p.canon[part] {
				return false
			}
		}
		return true
	case ruleWait:
		return v == removedText || waitTypeOK(v)
	case ruleObject:
		return v == removedText || counterObjectCanon[v]
	case ruleName:
		for _, s := range p.asIs {
			if v == s {
				return true
			}
		}
		return labelOK(p.prefix, v, labelHex)
	case rulePerson:
		return labelOK(p.prefix, v, labelHex)
	}
	return false
}

// caseWithholding records what BuildCase hid, for the masking statements.
type caseWithholding struct {
	rows        bool // an import row count is "withheld"
	personShown map[string]bool
	personHeld  map[string]bool
	unlisted    bool // events from a source without a policy were left out
	truncated   bool
}

// BuildCase assembles a redacted case export (schema version 3): one
// ExportImport and one ManifestImport per ok import, and one ExportEvent per
// event whose source has an export policy. Records is empty. The result is
// shown in full as a preview and written with Canonical; Verify accepts it.
//
// Rules:
//   - no file name, file hash, source line number, raw host name, raw user
//     value, stored case pseudonym, stored evidence id, IP, URL, path, message
//     or client form text reaches the file;
//   - event ids, hosts and named attributes are keyed pseudonyms under in.Key,
//     and the key is used for nothing else, so an export under another key
//     shares no label with this one;
//   - attributes follow exportPolicy: unlisted ones are left out, and values
//     that are not a number, a fixed term or a pseudonym are "[removed]";
//   - event kinds and units come from fixed lists; version strings are kept
//     only when they look like a version number;
//   - user pseudonyms are all or nothing (applyPersonRule);
//   - import counts below ThinCell print as "<20", and a row count is
//     withheld when its rejected count is masked (complementary suppression);
//   - the acknowledgement is reduced to a banner id;
//   - the file never says "proven", "strong correlation" or names a cause.
//
// Event times stay exact, so a holder of the original logs can match events
// to log lines; the export is for a trusted counterparty (see the masking
// statements).
//
// Before it returns, the result passes the same policy checks Verify applies,
// so what BuildCase writes and what Verify accepts cannot drift apart.
//
// It returns an error wrapping ErrInvalid for input it cannot export safely:
// a short key, no creation time, an importer name or file hash of the wrong
// shape, a negative count, a repeated evidence id, or an event that fails
// Event.Validate or whose evidence id does not name its source.
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
			SchemaVersion: CaseSchemaVersion,
			ToolVersion:   exportVersion(in.ToolVersion),
			CreatedAt:     in.Now.UTC(),
			Masking:       Masking{Statements: caseStatements(w)},
			Imports:       manifestImports,
		},
		Body: Body{Records: []Record{}, Imports: bodyImports, Events: events},
	}
	if e.Manifest.BodySHA256, err = exportHash(e); err != nil {
		return Export{}, err
	}
	// Last line of defence: the file must pass every check Verify makes on a
	// case export (shapes, allowlists, no cause, no "proven", no "strong
	// correlation", however they are spelt).
	raw, err := Canonical(e)
	if err != nil {
		return Export{}, err
	}
	if err := verifyCase(e, raw); err != nil {
		return Export{}, err
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
		man = append(man, ManifestImport{Ref: ref, Importer: importer, BannerID: bannerID(im.AckText), KnownGaps: gaps})
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

// cleanQuality keeps only what cannot carry client text: a version-like
// source version, a clock rule and the coverage times. The importers' free
// strings (threshold and sampling descriptions) are left out; the importer's
// known gaps say the same in fixed sentences.
func cleanQuality(q SourceQuality) SourceQuality {
	out := SourceQuality{SourceVersion: exportVersion(q.SourceVersion), TimezoneRule: exportTZRule(q.TimezoneRule)}
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
	seen := make(map[string]bool, len(events))
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
		id := keyedLabelN(key, "ev", ev.EvidenceID, eventLabelHex)
		if seen[id] {
			return nil, fmt.Errorf("%w: event %d: evidence id repeated", ErrInvalid, i)
		}
		seen[id] = true
		x := ExportEvent{EvidenceID: id, Source: ev.Source, Kind: otherKind,
			Start: ev.Start, End: ev.End, Severity: ev.Severity, TZRule: ev.TZRule}
		if exportKinds[ev.Source][ev.Kind] {
			x.Kind = ev.Kind
		}
		if exportUnits[ev.Unit] {
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
		"This file holds the imported events and import summaries of one case. File names, file hashes, raw host names, IP addresses, URLs and paths, query and SQL text, user agents, cookies, referers, messages, raw user names and source line numbers are not in it. Each imported file is named by its importer only.",
		fmt.Sprintf("An event attribute is kept only when its source's export policy lists it and its value is a number, a term from a fixed list (an HTTP method, a status, a counter or wait name and the like) or a keyed pseudonym; any other value is shown as \"%s\". Every other attribute, and every event from a source without a policy, is left out. An event kind comes from a fixed list per source; any other kind is shown as \"%s\".", removedText, otherKind),
		fmt.Sprintf("Event ids, hosts, sites, application pools, queues, jobs, namespaces, query hashes and counter instances are shown as keyed pseudonyms (a prefix such as host- followed by %d hex characters, %d for event ids). The key is made for this export only and is used for nothing else, so the same name gets a different label in any other export. A label hides the name, not the fact that two events share it.", labelHex, eventLabelHex),
	}
	shown, held := sortedKeys(w.personShown), sortedKeys(w.personHeld)
	for _, p := range shown {
		s = append(s, fmt.Sprintf("Each %[1]s is shown as a keyed pseudonym (%[1]s- followed by %[3]d hex characters) made with this export's key. The pseudonyms stored with the case do not appear. Every %[1]s shown has %[2]d or more events.", p, ThinCell, labelHex))
	}
	for _, p := range held {
		s = append(s, fmt.Sprintf("The %[1]s attribute is left out of every event: fewer than %[2]d of them were present, one had fewer than %[2]d events, or 1 to %[3]d events had none. Showing it would let a reader count a group below %[2]d.", p, ThinCell, ThinCell-1))
	}
	if len(shown) == 0 && len(held) == 0 {
		s = append(s, "No user pseudonyms are included.")
	}
	s = append(s, fmt.Sprintf("Every import row count and rejected-row count below %d is shown as \"<%d\", including counts inside known gaps.", ThinCell, ThinCell))
	if w.rows {
		s = append(s, fmt.Sprintf("A row count shown as \"%s\" is %d or more. It is hidden since the events of an import can be counted in this file, and a row count minus that number could come close to a rejected-row count below %d. This lowers the risk; it does not remove it, since rows that made no event also change the difference.", withheldText, ThinCell, ThinCell))
	}
	if w.unlisted {
		s = append(s, "Some events came from a source without an export policy and are left out.")
	}
	if w.truncated {
		s = append(s, "Not every event of the case is in this file: the event list stops at the read limit, so counts taken from it are lower bounds.")
	}
	return append(s,
		"Event times and durations are exact, and the number of events is the number in this file. Anyone who holds the original logs can match events to log lines by time and value. This export is for a trusted counterparty, not for publication.",
		fmt.Sprintf("The acknowledgement the analyst ticked is not copied: each import carries a banner id (the SHA-256 of a standard banner text, or \"%s\" for any other text). A version string is kept only when it looks like a version number. Free-text recording thresholds and sampling notes are not copied.", customBannerID),
		fmt.Sprintf("Known gaps are cleaned like hypothesis text: identifiers, email addresses, IP addresses, host names, paths and links become \"%s\", and a gap that still looks like it holds an identifier is replaced by \"%s\".", removedText, gapWithheld),
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

// csvCell is Cell, except that a number is left as it is: a cell that is wholly
// a number ("-5", "12.5") cannot start a formula, and escaping it would turn a
// negative value into text.
func csvCell(s string) string {
	if numberValue.MatchString(s) {
		return s
	}
	return Cell(s)
}

// EventsCSV writes the events of a case export as CSV: a header row, then one
// row per event with attributes as "key=value" pairs joined by "; " in key
// order. Every cell goes through csvCell, so no cell can start a formula.
func EventsCSV(e Export) ([]byte, error) {
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	row := func(cells ...string) error {
		for i, c := range cells {
			cells[i] = csvCell(c)
		}
		return cw.Write(cells)
	}
	if err := row("evidenceId", "source", "kind", "host", "start", "end", "durationMs", "value",
		"unit", "severity", "tzRule", "attrs"); err != nil {
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
			ev.Severity, ev.TZRule, strings.Join(pairs, "; ")); err != nil {
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
