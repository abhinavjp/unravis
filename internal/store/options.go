package store

// Option configures a Store at construction.
type Option func(*Store)

// WithBroadScans permits census (broad) scans against live sources. The default
// is false.
func WithBroadScans(allowed bool) Option {
	return func(s *Store) { s.allowBroadScans = allowed }
}

// WithDocsExaminedBudget caps docsExamined per query. Zero leaves it unset.
func WithDocsExaminedBudget(n int64) Option {
	return func(s *Store) { s.docsExaminedBudget = n }
}
