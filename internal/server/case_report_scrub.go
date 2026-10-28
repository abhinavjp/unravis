package server

import "api-audit-explorer/internal/evidence"

// reportTypedNotes is printed in every report: notes are the author's words, with
// anything that looks like a secret taken out.
const reportTypedNotes = "Typed notes are printed as written, with secrets removed."

// scrubTypedNote takes secrets out of a note a person typed. It is applied before
// the text is clipped, so a secret cannot be cut in half and slip past a pattern.
func scrubTypedNote(s string) string {
	return evidence.ScrubSecrets(s)
}
