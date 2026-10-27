package investigate

import (
	"strconv"

	"api-audit-explorer/internal/audit"
)

// FromRecords reduces audit records to engine requests. A record without a start
// time cannot be placed on a timeline; it is counted in the second result and
// left out. Nothing is read from the records but their own fields.
func FromRecords(rows []audit.RequestRecord) (reqs []Request, noStart int) {
	reqs = make([]Request, 0, len(rows))
	for _, r := range rows {
		if r.InitiatedAt == nil {
			noStart++
			continue
		}
		q := Request{
			ID:      r.ID,
			Route:   r.Controller + "." + r.Action,
			Start:   r.InitiatedAt.UTC(),
			A:       r.AuditCreateTimeMs,
			C:       r.ApiTimeMs,
			P:       r.PayloadRecordTimeMs,
			Path:    NormalizePath(r.URL),
			Outcome: r.Outcome,
		}
		if r.CompletedAt != nil {
			end := r.CompletedAt.UTC()
			q.End = &end
		}
		switch {
		case r.UserID != nil:
			q.Client = "u" + strconv.Itoa(*r.UserID)
		case r.UserIP != "":
			q.Client = "ip:" + r.UserIP
		}
		reqs = append(reqs, q)
	}
	return reqs, noStart
}
