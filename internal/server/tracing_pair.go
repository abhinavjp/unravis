package server

import (
	"strings"
	"unicode/utf8"

	"api-audit-explorer/internal/casestore"
)

// tagLabelCut is the label length at which the case wizard cuts a long file name
// when it saves a source tag. A label this long may be a prefix of the file name.
const tagLabelCut = 60

// sameFileName reports whether a tag label names the import file. The compare is
// trimmed and case-insensitive. A label of tagLabelCut runes or more that is a
// prefix of the file name also counts, because long labels are cut at the tag limit.
func sameFileName(label, fileName string) bool {
	l := strings.ToLower(strings.TrimSpace(label))
	f := strings.ToLower(strings.TrimSpace(fileName))
	if l == "" || f == "" {
		return false
	}
	if l == f {
		return true
	}
	return utf8.RuneCountInString(l) >= tagLabelCut &&
		utf8.RuneCountInString(l) < utf8.RuneCountInString(f) &&
		strings.HasPrefix(f, l)
}

// pairFileTags links source tags that were saved before their file was imported.
// The case wizard posts a tag with ImportID 0 first and never links it later, so
// the tag looks like "a source with no file yet". This returns tagID -> importID
// for the import-0 tags it can pair safely. It mirrors buildSourceList in
// web/src/lib/sourcesList.ts:
//   - only imports with Status ok (or empty) are candidates, and an import that a
//     tag already names is claimed and never given to another tag;
//   - pass 1 pairs by file name, pass 2 pairs by kind (tag Kind equals the import
//     Importer), and each pairs only when the match is unique on both sides;
//   - mongo database tags are never paired.
//
// Tags are visited in the given order, so the result is deterministic.
func pairFileTags(tags []casestore.SourceTag, imports []casestore.ImportRecord) map[int64]int64 {
	paired := map[int64]int64{}
	claimed := map[int64]bool{}
	for _, t := range tags {
		if t.ImportID != 0 {
			claimed[t.ImportID] = true
		}
	}

	// open returns the import-0 file tags that are not paired yet.
	open := func() []casestore.SourceTag {
		var out []casestore.SourceTag
		for _, t := range tags {
			if t.ImportID != 0 || t.Kind == mongoDatabaseKind {
				continue
			}
			if _, done := paired[t.ID]; done {
				continue
			}
			out = append(out, t)
		}
		return out
	}
	usable := func(im casestore.ImportRecord) bool {
		return (im.Status == casestore.StatusOK || im.Status == "") && !claimed[im.ID]
	}

	// Pass 1: by file name.
	for _, t := range open() {
		var match []casestore.ImportRecord
		for _, im := range imports {
			if usable(im) && sameFileName(t.Label, im.FileName) {
				match = append(match, im)
			}
		}
		if len(match) != 1 {
			continue
		}
		twins := 0 // unpaired import-0 tags of this kind and label, this one included
		for _, o := range open() {
			if o.Kind == t.Kind && strings.EqualFold(strings.TrimSpace(o.Label), strings.TrimSpace(t.Label)) {
				twins++
			}
		}
		if twins != 1 {
			continue
		}
		paired[t.ID] = match[0].ID
		claimed[match[0].ID] = true
	}

	// Pass 2: by kind, for tags whose label is not the file name of any import.
	for _, t := range open() {
		named := false
		for _, im := range imports {
			if sameFileName(t.Label, im.FileName) {
				named = true // this tag belongs to that file, leave it alone
				break
			}
		}
		if named {
			continue
		}
		var match []casestore.ImportRecord
		for _, im := range imports {
			if usable(im) && im.Importer == t.Kind {
				match = append(match, im)
			}
		}
		if len(match) != 1 {
			continue
		}
		sameKind := 0 // unpaired import-0 tags of this kind, this one included
		for _, o := range open() {
			if o.Kind == t.Kind {
				sameKind++
			}
		}
		if sameKind != 1 {
			continue
		}
		paired[t.ID] = match[0].ID
		claimed[match[0].ID] = true
	}
	return paired
}
