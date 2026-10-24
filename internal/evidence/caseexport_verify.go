package evidence

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"api-audit-explorer/internal/clock"
)

// verifyCase applies the case export policy to a parsed file: the checks
// BuildCase's output satisfies by construction, run again on whatever Verify
// is given. A hash only proves the file was not changed after it was written;
// it says nothing about what was written, so a file whose hash was recomputed
// after planting an IP, an email, a causal event kind or an acknowledgement
// text must still fail here.
//
// raw is the file's bytes. Errors name the field and the rule, never the
// value.
func verifyCase(e Export, raw []byte) error {
	if e.Manifest.SchemaVersion != CaseSchemaVersion {
		return caseInvalid("schema version %d is not the case export version", e.Manifest.SchemaVersion)
	}
	if len(e.Body.Records) != 0 || len(e.Body.Pseudonyms) != 0 || len(e.Body.Ledger) != 0 {
		return caseInvalid("a case export holds no records, pseudonym table or ledger")
	}
	if v := e.Manifest.ToolVersion; v != "" && exportVersion(v) != v {
		return caseInvalid("tool version is not a version number")
	}
	if err := verifyStatements(e.Manifest.Masking.Statements); err != nil {
		return err
	}
	if err := verifyImports(e); err != nil {
		return err
	}
	if err := verifyEvents(e.Body.Events); err != nil {
		return err
	}
	// Whole-file checks: nothing may name a cause, say "proven" or "strong
	// correlation", or carry an address or link, wherever it sits.
	text := string(raw)
	if !claimFree(text) {
		return caseInvalid("the file carries a causal or proven claim")
	}
	if strings.Contains(text, "@") || strings.Contains(text, "://") || ipv4.MatchString(text) {
		return caseInvalid("the file carries an address or link")
	}
	return nil
}

func caseInvalid(format string, a ...any) error {
	return fmt.Errorf("%w: case export: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

func verifyStatements(ss []string) error {
	if len(ss) == 0 || len(ss) > maxStatements {
		return caseInvalid("the number of masking statements is out of range")
	}
	for _, s := range ss {
		if s == "" || len(s) > maxStatement || scrubText(s) != s || residualRisk(s) || !claimFree(s) {
			return caseInvalid("a masking statement carries text the case export does not allow")
		}
	}
	return nil
}

func verifyImports(e Export) error {
	man, body := e.Manifest.Imports, e.Body.Imports
	if len(man) != len(body) {
		return caseInvalid("manifest and body list a different number of imports")
	}
	for i, m := range man {
		b := body[i]
		if !importRef.MatchString(m.Ref) || m.Ref != b.Ref || m.Ref != "import-"+strconv.Itoa(i+1) {
			return caseInvalid("import ref has the wrong shape or order")
		}
		if m.Importer != b.Importer || (exportPolicy[m.Importer] == nil && m.Importer != otherImporter) {
			return caseInvalid("import importer is not a known importer")
		}
		if !knownBannerIDs[m.BannerID] {
			return caseInvalid("import banner id is not a known banner id")
		}
		if m.KnownGaps == nil || len(m.KnownGaps) > 64 {
			return caseInvalid("import known gaps are missing or too many")
		}
		for _, g := range m.KnownGaps {
			if cleanCaseText(g, gapWithheld) != g {
				return caseInvalid("a known gap carries text the case export does not allow")
			}
		}
		if b.Host != "" && !labelOK("host", b.Host, labelHex) {
			return caseInvalid("import host is not a keyed label")
		}
		if b.TZRule != exportTZRule(b.TZRule) {
			return caseInvalid("import time-zone rule is not a clock rule")
		}
		if err := verifyCount(b.Rows, true); err != nil {
			return err
		}
		if err := verifyCount(b.Rejected, false); err != nil {
			return err
		}
		if b.Rows.Text == withheldText && !b.Rejected.Masked {
			return caseInvalid("a row count is withheld although the rejected count is not masked")
		}
		q := b.Quality
		if q.SourceVersion != exportVersion(q.SourceVersion) || q.TimezoneRule != exportTZRule(q.TimezoneRule) ||
			q.Threshold != "" || q.Sampling != "" {
			return caseInvalid("import source quality carries text the case export does not allow")
		}
	}
	return nil
}

// verifyCount accepts the counts maskCount64 produces, and "withheld" for a
// row count.
func verifyCount(c Count, isRows bool) error {
	switch {
	case c.Masked && c.Value == nil && c.Text == "<"+strconv.Itoa(ThinCell):
	case c.Masked && c.Value == nil && c.Text == withheldText && isRows:
	case !c.Masked && c.Value != nil && *c.Value >= ThinCell && c.Text == strconv.Itoa(*c.Value):
	default:
		return caseInvalid("an import count is not a masked or full count")
	}
	return nil
}

func verifyEvents(events []ExportEvent) error {
	seen := make(map[string]bool, len(events))
	for i, ev := range events {
		pol := exportPolicy[ev.Source]
		if pol == nil {
			return caseInvalid("event source has no export policy")
		}
		if !labelOK("ev", ev.EvidenceID, eventLabelHex) || seen[ev.EvidenceID] {
			return caseInvalid("event id is not a unique keyed label")
		}
		seen[ev.EvidenceID] = true
		if i > 0 {
			prev := events[i-1]
			if ev.Start.Before(prev.Start) || (ev.Start.Equal(prev.Start) && ev.EvidenceID < prev.EvidenceID) {
				return caseInvalid("events are not in time order")
			}
		}
		if ev.Kind != otherKind && !exportKinds[ev.Source][ev.Kind] {
			return caseInvalid("event kind is not on the list of its source")
		}
		if !exportUnits[ev.Unit] && ev.Unit != "" {
			return caseInvalid("event unit is not on the list of units")
		}
		if !exportSeverities[ev.Severity] {
			return caseInvalid("event severity is not in the fixed set")
		}
		if ev.TZRule == "" || ev.TZRule != exportTZRule(ev.TZRule) {
			return caseInvalid("event time-zone rule is not a clock rule")
		}
		if !isUTC(ev.Start) || !isUTC(ev.End) || ev.End.Before(ev.Start) {
			return caseInvalid("event times are not UTC or end before start")
		}
		for _, p := range []*float64{ev.DurationMs, ev.Value} {
			if p != nil && (math.IsNaN(*p) || math.IsInf(*p, 0)) {
				return caseInvalid("event number is not finite")
			}
		}
		if ev.DurationMs != nil && *ev.DurationMs < 0 {
			return caseInvalid("event duration is negative")
		}
		if ev.Host != "" && !labelOK("host", ev.Host, labelHex) {
			return caseInvalid("event host is not a keyed label")
		}
		if len(ev.Attrs) > MaxEventAttrs {
			return caseInvalid("event has too many attributes")
		}
		for k, v := range ev.Attrs {
			p, ok := pol[k]
			if !ok || v == "" || len(v) > MaxEventAttrValue || !attrValueOK(p, v) {
				return caseInvalid("event attribute is not allowed or its value breaks its rule")
			}
		}
	}
	return nil
}

// Compile-time check that the clock rules exportTZRule lists stay in step
// with Event.Validate: a rule is accepted by one only if the other accepts it.
var _ = [...]string{clock.RuleUTCField, clock.RuleOffsetInField, clock.RuleHeaderBias, clock.RuleHostLocal}
