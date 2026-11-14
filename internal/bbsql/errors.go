// errors.go is the C10 classifier: every driver failure becomes one fixed
// code with a fixed sentence. The Error type lives in conn.go (Code + fixed
// Message); driver text, instance names and login names are never copied into
// a code or message. The number/phrase approach follows dbhealth's
// ClassifyConnect pattern but is written fresh for the Blackbox.
package bbsql

import (
	"context"
	"errors"
	"strings"

	mssql "github.com/microsoft/go-mssqldb"
)

// Fixed C10 codes. CodeConnectTimeout ("timeout") is the driver/connect
// timeout; CodeTimeout ("query-timeout", conn.go) is the C4 query budget.
const (
	CodeLoginFailed    = "login-failed"
	CodeDatabase       = "database"
	CodePermission     = "permission"
	CodeConnectTimeout = "timeout"
	CodeNetwork        = "network"
	CodeTLS            = "tls"
	CodeTooManyRights  = "too-many-rights"
	CodeFailed         = "failed"
)

// classified returns the fixed sentence for a code; nothing is built from
// driver text.
func classified(code string) *Error {
	switch code {
	case CodeLoginFailed:
		return &Error{code, "The server refused this login."}
	case CodeDatabase:
		return &Error{code, "The login cannot open the database."}
	case CodePermission:
		return &Error{code, "The login is missing a permission it needs."}
	case CodeConnectTimeout:
		return &Error{code, "The server did not answer in time."}
	case CodeNetwork:
		return &Error{code, "The local SQL Server could not be reached."}
	case CodeTLS:
		return &Error{code, "The secure connection could not be set up."}
	case CodeTooManyRights:
		return ErrTooManyRights
	default:
		return &Error{CodeFailed, "The SQL operation failed."}
	}
}

// Classify maps any error to a fixed-code *Error. Already-fixed errors pass
// through unchanged; driver errors match on SQL Server numbers first and
// stable phrases second. The raw text is read for matching only — it is never
// stored, wrapped or surfaced.
func Classify(err error) *Error {
	if err == nil {
		return nil
	}
	var fixed *Error
	if errors.As(err, &fixed) {
		return fixed
	}
	var se mssql.Error
	if errors.As(err, &se) {
		switch se.Number {
		case 18456, 18452, 18451, 18488, 18487:
			return classified(CodeLoginFailed)
		case 4060, 911:
			return classified(CodeDatabase)
		case 229, 230, 262, 297, 916, 15151, 15247:
			return classified(CodePermission)
		case -2:
			return classified(CodeConnectTimeout)
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return classified(CodeConnectTimeout)
	}
	m := strings.ToLower(err.Error()) // matching only; never surfaced
	switch {
	case strings.Contains(m, "login failed"):
		return classified(CodeLoginFailed)
	case strings.Contains(m, "cannot open database"):
		return classified(CodeDatabase)
	case strings.Contains(m, "permission"):
		return classified(CodePermission)
	case strings.Contains(m, "certificate") || strings.Contains(m, "x509") ||
		strings.Contains(m, "tls") || strings.Contains(m, "handshake") || strings.Contains(m, "encryption"):
		return classified(CodeTLS)
	case strings.Contains(m, "timeout") || strings.Contains(m, "deadline"):
		return classified(CodeConnectTimeout)
	case strings.Contains(m, "no such host") || strings.Contains(m, "lookup") ||
		strings.Contains(m, "connection refused") || strings.Contains(m, "unreachable") ||
		strings.Contains(m, "dial") || strings.Contains(m, "unable to open tcp") || strings.Contains(m, "network"):
		return classified(CodeNetwork)
	}
	return classified(CodeFailed)
}
