package importer

// Reason is a fixed enum naming why a row or a file was refused. It never
// holds row content.
type Reason string

const (
	ReasonFieldCount        Reason = "field-count"
	ReasonBadTime           Reason = "bad-time"
	ReasonTZUnlabelled      Reason = "tz-unlabelled"
	ReasonDSTAmbiguous      Reason = "dst-ambiguous"
	ReasonDSTNonexistent    Reason = "dst-nonexistent"
	ReasonBadNumber         Reason = "bad-number"
	ReasonLineTooLong       Reason = "line-too-long"
	ReasonEncoding          Reason = "encoding"
	ReasonUnknownHost       Reason = "unknown-host"
	ReasonUnscrubbedColumn  Reason = "unscrubbed-column"
	ReasonUnsupportedFormat Reason = "unsupported-format"
	ReasonArchive           Reason = "archive-extract-first"
	ReasonTZMismatch        Reason = "tz-mismatch"
	ReasonRejectFraction    Reason = "reject-fraction"

	// Binary families (sniff.go): exports that cannot be read in pure Go. Each is
	// refused from the head alone, and the web copy holds the conversion step.
	ReasonBinaryBLG      Reason = "binary-blg"
	ReasonBinaryETL      Reason = "binary-etl"
	ReasonBinaryNettrace Reason = "binary-nettrace"
	ReasonBinaryEVTX     Reason = "binary-evtx"
	ReasonBinaryBSON     Reason = "binary-bson"
	ReasonBinaryXEL      Reason = "binary-xel"
	ReasonBinaryRDB      Reason = "binary-rdb"
)

var allReasons = []Reason{
	ReasonFieldCount, ReasonBadTime, ReasonTZUnlabelled, ReasonDSTAmbiguous, ReasonDSTNonexistent,
	ReasonBadNumber, ReasonLineTooLong, ReasonEncoding, ReasonUnknownHost, ReasonUnscrubbedColumn,
	ReasonUnsupportedFormat, ReasonArchive, ReasonTZMismatch, ReasonRejectFraction,
	ReasonBinaryBLG, ReasonBinaryETL, ReasonBinaryNettrace, ReasonBinaryEVTX, ReasonBinaryBSON, ReasonBinaryXEL, ReasonBinaryRDB,
}

// AllReasons lists every Reason, in declaration order.
func AllReasons() []Reason { return append([]Reason(nil), allReasons...) }

// Valid reports whether r is one of the fixed values.
func (r Reason) Valid() bool {
	for _, v := range allReasons {
		if v == r {
			return true
		}
	}
	return false
}

// Text is a short fixed sentence fragment for people. It never holds content.
func (r Reason) Text() string {
	switch r {
	case ReasonFieldCount:
		return "wrong number of fields"
	case ReasonBadTime:
		return "unreadable time"
	case ReasonTZUnlabelled:
		return "time zone not stated"
	case ReasonDSTAmbiguous:
		return "local time falls in a clock change and is ambiguous"
	case ReasonDSTNonexistent:
		return "local time falls in a clock change and does not exist"
	case ReasonBadNumber:
		return "unreadable number"
	case ReasonLineTooLong:
		return "line too long"
	case ReasonEncoding:
		return "text encoding not valid"
	case ReasonUnknownHost:
		return "host not in the host map"
	case ReasonUnscrubbedColumn:
		return "file has a column that must be removed before import"
	case ReasonUnsupportedFormat:
		return "format not supported"
	case ReasonArchive:
		return "archive, extract first"
	case ReasonTZMismatch:
		return "time zone does not match the file"
	case ReasonRejectFraction:
		return "too many rows rejected"
	case ReasonBinaryBLG:
		return "binary performance log, convert to CSV first"
	case ReasonBinaryETL:
		return "binary trace log, convert to CSV or text first"
	case ReasonBinaryNettrace:
		return "binary dotnet-trace file, not read"
	case ReasonBinaryEVTX:
		return "raw event log, export to CSV first"
	case ReasonBinaryBSON:
		return "binary MongoDB dump, export to JSON first"
	case ReasonBinaryXEL:
		return "binary extended events file, export to CSV or XML first"
	case ReasonBinaryRDB:
		return "binary Redis snapshot, not read"
	}
	return "refused"
}
