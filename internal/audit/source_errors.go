package audit

// SourceQueryErrorMessage returns only curated messages; driver error text is
// never used as a browser-facing message.
func SourceQueryErrorMessage(code string) string {
	switch code {
	case "capacity_timeout":
		return "The source could not start before the comparison deadline because query slots were occupied. Retry with fewer sources or after other queries finish."
	case "query_cancelled":
		return "The audit query was cancelled. Run the comparison again."
	case "source_unavailable":
		return "Source was removed before the query started."
	case "query_timeout":
		return "The audit query timed out. Narrow the time range or ask DevOps to check query performance and timing indexes."
	case "permission_denied":
		return "MongoDB denied this query (code 13). Check read permissions for this database and collection."
	case "authentication_failed":
		return "MongoDB rejected authentication (code 18). Check the connection credentials and authentication database."
	case "query_memory_limit":
		return "MongoDB exceeded its query memory limit. Narrow the time range or ask DevOps to review query indexes."
	case "connection_failed":
		return "The connection was interrupted during the audit query. Check VPN and server connectivity."
	default:
		return "Source query failed; check availability and read permissions."
	}
}
