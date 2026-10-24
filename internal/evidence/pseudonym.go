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

// buildPseudonyms counts stalls per distinct user and returns one masked entry
// per user, sorted by label. Stalls without a user are not counted. The raw id
// and the key never leave this function.
func buildPseudonyms(key []byte, stalls []audit.Stall) []Pseudonym {
	perUser := map[int]int{}
	for _, s := range stalls {
		if s.UserID != nil {
			perUser[*s.UserID]++
		}
	}
	out := make([]Pseudonym, 0, len(perUser))
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
	return out
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
