package sqlite

import (
	"crypto/rand"
	"encoding/base32"
	"regexp"
	"strings"

	"api-audit-explorer/internal/casestore"
)

var caseIDPattern = regexp.MustCompile(`^[a-z2-7]{26}$`)

// validID reports whether id has the shape of a case ID. Every path join in this
// package goes through it first (§4 rule 7).
func validID(id casestore.CaseID) bool { return caseIDPattern.MatchString(string(id)) }

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// newID returns 128 random bits as 26 lowercase base32 characters. IDs are never
// derived from a title, host or client name.
func newID() (casestore.CaseID, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return casestore.CaseID(strings.ToLower(b32.EncodeToString(raw))), nil
}
