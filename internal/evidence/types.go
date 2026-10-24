// Package evidence is the P13 evidence record and redacted case export
// (roadmap §8.9, rulings C8, R15). It reads findings the engine already
// produced; it adds no database access. An export is shown in full as a
// preview before anything is written, and the per-case pseudonym key is never
// part of an export.
package evidence

import "time"

const (
	// SchemaVersion is the version Build (the finding export) writes. The case
	// export fields are omitted when empty, so a version 1 or 2 file decodes
	// and re-encodes to the same bytes.
	SchemaVersion = 2
	// CaseSchemaVersion is the version BuildCase writes. Version 3 is the first
	// version that may hold Manifest.Imports, Body.Imports and Body.Events (the
	// case export); a file of an older version that holds any of them is
	// refused, so a case export cannot be passed off as an older file to skip
	// the case export checks. It carries no source line numbers, no
	// per-file hash and no acknowledgement text.
	CaseSchemaVersion = 3
	// MaxSchemaVersion is the newest version Verify accepts.
	MaxSchemaVersion = CaseSchemaVersion
	// MinSchemaVersion is the oldest version Verify accepts.
	MinSchemaVersion = 1
	// ThinCell is the masking threshold: any count below it prints as "<20".
	ThinCell = 20
)

// Provenance names where a record came from and pins it with a hash.
type Provenance struct {
	Kind string `json:"kind"` // "query" for a live or demo read, "file" for an import
	Ref  string `json:"ref"`  // source label or file name, never a path or URL
	Hash string `json:"hash"` // SHA-256 hex of the canonical record fields
}

// SourceQuality states what the source can and cannot show (§6.2, §6.6, §8.8).
type SourceQuality struct {
	SourceVersion string     `json:"sourceVersion,omitempty"`
	Threshold     string     `json:"threshold,omitempty"` // recording threshold such as slowms; empty when none applies
	CoverageFirst *time.Time `json:"coverageFirst,omitempty"`
	CoverageLast  *time.Time `json:"coverageLast,omitempty"`
	Sampling      string     `json:"sampling,omitempty"`
	TimezoneRule  string     `json:"timezoneRule,omitempty"`
}

// Count is a masked number. Value is nil when Masked is true.
type Count struct {
	Value  *int   `json:"value,omitempty"`
	Masked bool   `json:"masked"`
	Text   string `json:"text"` // "7" or "<20"
}

// Record is one versioned evidence record.
type Record struct {
	Version     int           `json:"version"`
	EvidenceID  string        `json:"evidenceId"`
	Source      string        `json:"source"`
	WindowStart time.Time     `json:"windowStart"`
	WindowEnd   time.Time     `json:"windowEnd"`
	JoinClass   string        `json:"joinClass"` // "none" for single-source records
	Tolerance   string        `json:"tolerance"` // e.g. "1 s _id resolution"
	Provenance  Provenance    `json:"provenance"`
	Label       string        `json:"label"` // Symptom, Correlation or Strong correlation
	Rule        string        `json:"rule"`
	Headline    string        `json:"headline"`
	N           Count         `json:"n"`
	LowN        bool          `json:"lowN"`
	KnownGaps   []string      `json:"knownGaps"`
	Quality     SourceQuality `json:"sourceQuality"`
}

// LedgerEntry is the export form of a hypothesis: text, state and evidence
// references only. Notes the analyst marked case-specific are dropped.
type LedgerEntry struct {
	Text        string   `json:"text"`
	State       string   `json:"state"`
	EvidenceIDs []string `json:"evidenceIds"`
}

// Pseudonym is a keyed stand-in for a user, e.g. "user-3fa91c2e". Stable within
// one case, unlinkable across cases.
type Pseudonym struct {
	Label    string `json:"label"`
	Requests Count  `json:"requests"`
}

// Options is what the analyst chose in the preview. Defaults are all false: no
// pseudonyms and no ledger.
type Options struct {
	IncludePseudonyms bool `json:"includePseudonyms"`
	IncludeLedger     bool `json:"includeLedger"`
}

// Masking states what was left out or hidden, in plain words.
type Masking struct {
	Statements []string `json:"statements"`
}

// Manifest pins the export. Hash is the SHA-256 of Export.Body (see Export).
type Manifest struct {
	SchemaVersion int       `json:"schemaVersion"`
	ToolVersion   string    `json:"toolVersion"`
	CreatedAt     time.Time `json:"createdAt"`
	BodySHA256    string    `json:"bodySha256"`
	Masking       Masking   `json:"masking"`
	// Imports lists the imported files of a case export (version 3). A file is
	// named by its importer only, never by its file name or hash.
	Imports []ManifestImport `json:"imports,omitempty"`
}

// ManifestImport is one imported file as the manifest states it: which
// importer read it, an identifier of the gating banner the analyst
// acknowledged, and the importer's known gaps. There is no file name field,
// no file hash and no free text from the client form.
type ManifestImport struct {
	Ref      string `json:"ref"` // "import-1", ...; the same ref as in Body.Imports
	Importer string `json:"importer"`
	// BannerID is "banner-" and 16 hex characters of the SHA-256 of a known
	// banner text, or "custom-not-exported" for any other acknowledgement.
	BannerID  string   `json:"bannerId"`
	KnownGaps []string `json:"knownGaps"`
}

// ExportImport is the body's summary of one imported file. Host is a keyed
// pseudonym for this export; counts are masked.
type ExportImport struct {
	Ref      string        `json:"ref"`
	Importer string        `json:"importer"`
	Host     string        `json:"host,omitempty"`
	TZRule   string        `json:"tzRule,omitempty"`
	Rows     Count         `json:"rows"`
	Rejected Count         `json:"rejected"`
	Quality  SourceQuality `json:"sourceQuality"`
}

// ExportEvent is one imported event after the per-source attribute policy:
// the event id, host and named attributes are keyed pseudonyms for this
// export, and only allowlisted attributes of fixed or numeric value remain.
// There is no source line number. Attrs marshals with sorted keys, so the
// encoding is canonical.
type ExportEvent struct {
	EvidenceID string            `json:"evidenceId"`
	Source     string            `json:"source"`
	Kind       string            `json:"kind"`
	Host       string            `json:"host,omitempty"`
	Start      time.Time         `json:"start"`
	End        time.Time         `json:"end"`
	DurationMs *float64          `json:"durationMs,omitempty"`
	Value      *float64          `json:"value,omitempty"`
	Unit       string            `json:"unit,omitempty"`
	Severity   string            `json:"severity,omitempty"`
	TZRule     string            `json:"tzRule"`
	Attrs      map[string]string `json:"attrs,omitempty"`
}

// Body is what the file contains between the manifest and the signature of the
// hash. Nothing in it carries a payload, raw URL, query text, IP, user agent or
// raw user ID.
type Body struct {
	Records    []Record       `json:"records"`
	Pseudonyms []Pseudonym    `json:"pseudonyms,omitempty"`
	Ledger     []LedgerEntry  `json:"ledger,omitempty"`
	Imports    []ExportImport `json:"imports,omitempty"` // version 3 (case export)
	Events     []ExportEvent  `json:"events,omitempty"`  // version 3 (case export)
}

// Export is the whole file: manifest plus body.
type Export struct {
	Manifest Manifest `json:"manifest"`
	Body     Body     `json:"body"`
}
