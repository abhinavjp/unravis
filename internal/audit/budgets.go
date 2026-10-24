package audit

import "time"

// Query budgets and client timeouts. OperationTimeout (models.go) stays the
// HTTP and request ceiling; the values here bound the database work beneath it.
const (
	InteractiveQueryBudget = 10 * time.Second // maxTimeMS 10000
	CensusQueryBudget      = 60 * time.Second // maxTimeMS 60000
	ServerSelectionTimeout = 30 * time.Second // driver default
	ConnectTimeout         = 10 * time.Second
	PerSourceConcurrency   = 1
)
