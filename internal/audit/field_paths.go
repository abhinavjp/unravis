package audit

import "context"

// Limits of the field-path probe. It reads the newest few documents of a
// source and reports where fields are and what BSON type they have, so a
// missing field name can be found without reading any value.
const (
	// FieldPathsSample is the number of newest documents the probe inspects.
	FieldPathsSample = 5
	// MaxFieldPaths caps the paths one answer lists.
	MaxFieldPaths = 200
	// MaxFieldPathDepth caps how deep nested documents and arrays are followed
	// (a top-level field has depth 1).
	MaxFieldPathDepth = 4
	// MaxFieldPathName cuts one path segment, so data-bearing keys stay short.
	MaxFieldPathName = 100
)

// FieldPath is one field path with the BSON type names seen for it across the
// sampled documents. An array shows as `Name` (type array) plus `Name[]` for
// its elements and `Name[].Child` for the fields of document elements.
type FieldPath struct {
	Path  string   `json:"path"`
	Types []string `json:"types"`
}

// FieldPaths is the answer of the field-path probe. It carries paths and type
// names only, never a value.
type FieldPaths struct {
	Sampled   int         `json:"sampled"`
	Paths     []FieldPath `json:"paths"`
	Truncated bool        `json:"truncated"`
}

// FieldPathSampler is an optional repository capability: list the field paths
// of the newest documents of one source.
type FieldPathSampler interface {
	SampleFieldPaths(ctx context.Context, sourceID string) (FieldPaths, error)
}
