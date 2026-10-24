package evidence

import (
	"api-audit-explorer/internal/audit"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
)

// minKeyLen is the shortest per-case key Build accepts when pseudonyms are on.
const minKeyLen = 16

// pseudonymLabel is "user-" plus the first 8 hex characters of
// HMAC-SHA256(key, decimal user id). Without the key the label cannot be mapped
// back to the id, and a new key (a new case) gives an unrelated label.
func pseudonymLabel(key []byte, userID int) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(strconv.Itoa(userID)))
	return "user-" + hex.EncodeToString(mac.Sum(nil))[:8]
}

// buildPseudonyms counts stalls per distinct user and returns one entry per
// user, sorted by label. Stalls without a user are not counted. The raw id and
// the key never leave this function.
//
// DECISION (complementary suppression): the per-user counts are parts of the
// B1 total, which is printed. Masking one small cell on its own is not enough,
// as the total minus the shown cells gives it back. So the whole table is
// withheld (ok is false) when any per-user count is below ThinCell, when the
// stalls without a user number 1 to ThinCell-1, or when fewer than ThinCell
// users are present (the list length is itself a count of users). No "others"
// remainder row is ever printed. Every count in a table that is printed is
// therefore at least ThinCell.
func buildPseudonyms(key []byte, stalls []audit.Stall) (out []Pseudonym, ok bool) {
	perUser := map[int]int{}
	noUser := 0
	for _, s := range stalls {
		if s.UserID != nil {
			perUser[*s.UserID]++
		} else {
			noUser++
		}
	}
	if len(perUser) < ThinCell || (noUser > 0 && noUser < ThinCell) {
		return nil, false
	}
	for _, n := range perUser {
		if n < ThinCell {
			return nil, false
		}
	}
	out = make([]Pseudonym, 0, len(perUser))
	for id, n := range perUser {
		out = append(out, Pseudonym{Label: pseudonymLabel(key, id), Requests: MaskCount(n)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Label != out[j].Label {
			return out[i].Label < out[j].Label
		}
		// Two users sharing a label is possible (32 bits); keep order stable.
		return out[i].Requests.Text < out[j].Requests.Text
	})
	return out, true
}

// reportUserIDs is every raw user id the report holds, so free text can be
// cleaned of them.
func reportUserIDs(r audit.PatternReport) map[int]bool {
	ids := map[int]bool{}
	for _, s := range r.Stalls {
		if s.UserID != nil {
			ids[*s.UserID] = true
		}
	}
	for _, v := range r.Volleys {
		ids[v.UserID] = true
	}
	return ids
}
