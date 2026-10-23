package audit

// Query kinds beyond the three derived row kinds. A row's own kind (Bucket.Kind,
// RequestRecord.Kind) is synchronous, asynchronous, hangfire or unknown, where
// unknown means the HTTP request has no recognised polling type yet (mostly an
// incomplete row written before its audit update). "http" selects every HTTP row
// together and "http_unknown" selects only the rows of unknown kind.
const (
	KindHTTP        = "http"
	KindHTTPUnknown = "http_unknown"
	KindUnknown     = "unknown"
)

// KindMembers lists the row kinds a completed (valid) row may have to belong to
// the query kind. Performance is never merged across kinds: a valid
// synchronous row is not part of an asynchronous query.
func KindMembers(kind string) []string {
	switch kind {
	case KindHTTP:
		return []string{"synchronous", "asynchronous", KindUnknown}
	case KindHTTPUnknown:
		return []string{KindUnknown}
	}
	return []string{kind}
}

// KindCompatibleMembers lists the row kinds a row that is not valid (incomplete
// or invalid) may have to be kept under the query kind. Such a row cannot be
// told apart as Synchronous or Asynchronous, so a row of unknown kind stays
// visible under both, and under http, but never under hangfire.
func KindCompatibleMembers(kind string) []string {
	switch kind {
	case "synchronous", "asynchronous":
		return []string{kind, KindUnknown}
	}
	return KindMembers(kind)
}

// KindMatches reports whether a row of rowKind belongs to the query kind. valid
// says whether the row is a completed, timed one: only valid rows carry latency,
// so they need an exact kind, while other rows follow the compatible rule.
func KindMatches(queryKind, rowKind string, valid bool) bool {
	if queryKind == "all" {
		return true
	}
	members := KindCompatibleMembers(queryKind)
	if valid {
		members = KindMembers(queryKind)
	}
	for _, member := range members {
		if member == rowKind {
			return true
		}
	}
	return false
}
