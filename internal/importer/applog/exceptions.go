package applog

import (
	"regexp"
	"strings"
)

// AppException is the stored name of every exception type outside the
// curated framework list (plan §2.2).
const AppException = "app-exception"

// maxExceptionTypes is how many types one entry keeps: outer and innermost.
const maxExceptionTypes = 2

// maxExceptionScan bounds how much exception text is read, so a huge
// continuation block costs a fixed amount.
const (
	maxExceptionScan  = 256 << 10
	maxExceptionLines = 2000
)

// exceptionRule is the §2.2 framework exception pattern. A type is kept only
// when it matches this pattern and is on curatedExceptions as well.
var exceptionRule = regexp.MustCompile(`^(System|Microsoft|MongoDB|StackExchange\.Redis|Newtonsoft|Npgsql|NHibernate|` +
	`Hangfire|Polly|Grpc|Azure|log4net)(\.[A-Za-z0-9_]+)*\.[A-Za-z0-9_]*(Exception|Error)$`)

// curatedExceptions is the export's exceptionTypes list (without
// "app-exception"), so a stored type always survives the export. A framework
// type missing here is stored as "app-exception": storing it would make the
// export drop the whole attribute. TestCuratedExceptionsSurviveExport checks
// every entry against the export.
var curatedExceptions = setOf(
	"System.Exception", "System.SystemException", "System.TimeoutException", "System.OutOfMemoryException",
	"System.InsufficientMemoryException", "System.StackOverflowException", "System.InsufficientExecutionStackException",
	"System.Threading.ThreadAbortException", "System.Threading.ThreadInterruptedException",
	"System.Threading.SynchronizationLockException", "System.Threading.Tasks.TaskCanceledException",
	"System.OperationCanceledException", "System.AggregateException", "System.InvalidOperationException",
	"System.ArgumentException", "System.ArgumentNullException", "System.ArgumentOutOfRangeException",
	"System.NullReferenceException", "System.ObjectDisposedException", "System.FormatException",
	"System.InvalidCastException", "System.IndexOutOfRangeException", "System.NotSupportedException",
	"System.NotImplementedException", "System.UnauthorizedAccessException", "System.DivideByZeroException",
	"System.OverflowException", "System.Collections.Generic.KeyNotFoundException", "System.IO.IOException",
	"System.IO.FileNotFoundException", "System.IO.DirectoryNotFoundException", "System.IO.EndOfStreamException",
	"System.Net.Sockets.SocketException", "System.Net.WebException", "System.Net.Http.HttpRequestException",
	"System.Net.HttpListenerException", "System.ComponentModel.Win32Exception",
	"System.Data.SqlClient.SqlException", "System.Data.DataException", "System.Data.DBConcurrencyException",
	"System.Transactions.TransactionAbortedException", "System.Text.Json.JsonException",
	"System.Web.HttpException", "System.Web.HttpUnhandledException", "System.Web.HttpRequestValidationException",
	"System.ServiceModel.CommunicationException", "System.ServiceModel.EndpointNotFoundException",
	"System.Security.Authentication.AuthenticationException", "System.Security.SecurityException",
	"Microsoft.Data.SqlClient.SqlException", "Microsoft.EntityFrameworkCore.DbUpdateException",
	"Microsoft.EntityFrameworkCore.DbUpdateConcurrencyException",
	"Microsoft.AspNetCore.Connections.ConnectionResetException",
	"Microsoft.AspNetCore.Connections.ConnectionAbortedException",
	"Microsoft.AspNetCore.Server.Kestrel.Core.BadHttpRequestException",
	"Microsoft.AspNetCore.Http.BadHttpRequestException",
	"StackExchange.Redis.RedisTimeoutException", "StackExchange.Redis.RedisConnectionException",
	"StackExchange.Redis.RedisServerException", "StackExchange.Redis.RedisCommandException",
	"StackExchange.Redis.RedisException",
	"MongoDB.Driver.MongoException", "MongoDB.Driver.MongoConnectionException",
	"MongoDB.Driver.MongoConnectionPoolPausedException", "MongoDB.Driver.MongoWaitQueueFullException",
	"MongoDB.Driver.MongoExecutionTimeoutException", "MongoDB.Driver.MongoCommandException",
	"MongoDB.Driver.MongoWriteException", "MongoDB.Driver.MongoNotPrimaryException",
	"MongoDB.Driver.MongoNodeIsRecoveringException", "MongoDB.Driver.MongoAuthenticationException",
	"Newtonsoft.Json.JsonReaderException", "Newtonsoft.Json.JsonSerializationException",
	"Npgsql.NpgsqlException", "Npgsql.PostgresException", "NHibernate.ADOException",
	"NHibernate.StaleObjectStateException", "Hangfire.Storage.DistributedLockTimeoutException",
	"Polly.Timeout.TimeoutRejectedException", "Polly.CircuitBreaker.BrokenCircuitException",
	"Polly.Bulkhead.BulkheadRejectedException", "Grpc.Core.RpcException", "Azure.RequestFailedException",
	"log4net.Core.LogException")

func setOf(vs ...string) map[string]bool {
	m := make(map[string]bool, len(vs))
	for _, v := range vs {
		m[v] = true
	}
	return m
}

// exceptionLabel is the stored name of one raw type: itself when curated,
// else "app-exception".
func exceptionLabel(typ string) string {
	if curatedExceptions[typ] && exceptionRule.MatchString(typ) {
		return typ
	}
	return AppException
}

// excSegment is one exception found in the text: its raw type (held in
// memory only) and the message that follows "Type: " (also memory only).
type excSegment struct {
	typ, msg string
}

// unhandledPrefix is how the .NET host starts an unhandled exception on
// stdout (ANCM stdout, plan §3 W2-d).
const unhandledPrefix = "Unhandled exception. "

// scanExceptions finds the outer exception (the first line that starts with a
// type) and every inner exception introduced by "--->", in order. Stack frames,
// "--- End of ..." lines and text that does not start with a type are skipped.
func scanExceptions(exc string) []excSegment {
	if len(exc) > maxExceptionScan {
		exc = exc[:maxExceptionScan]
	}
	var out []excSegment
	outer := false
	for n := 0; exc != "" && n < maxExceptionLines; n++ {
		var line string
		line, exc, _ = strings.Cut(exc, "\n")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "at ") {
			continue
		}
		segs := strings.Split(line, "--->")
		for i, seg := range segs {
			seg = strings.TrimSpace(seg)
			if i == 0 {
				if outer {
					continue // a later line counts only through "--->"
				}
				seg = strings.TrimPrefix(seg, unhandledPrefix)
			}
			typ, msg, ok := leadingType(seg)
			if !ok {
				continue
			}
			if i == 0 {
				outer = true
			} else if !outer {
				outer = true // an inner marker before any outer line: treat as outer
			}
			out = append(out, excSegment{typ: typ, msg: msg})
		}
	}
	return out
}

// leadingType reads a .NET type name at the start of s. The name is a dotted
// identifier (generic "`1" and nested "+" allowed) ending in "Exception" or
// "Error", followed by the end of s, ":" or " (". msg is the text after the
// colon, if any.
func leadingType(s string) (typ, msg string, ok bool) {
	i := 0
	for i < len(s) && i <= 512 {
		c := s[i]
		if c == '.' || c == '_' || c == '`' || c == '+' || (c >= '0' && c <= '9') || (c|0x20 >= 'a' && c|0x20 <= 'z') {
			i++
			continue
		}
		break
	}
	typ, rest := s[:i], s[i:]
	if typ == "" || !(strings.HasSuffix(typ, "Exception") || strings.HasSuffix(typ, "Error")) {
		return "", "", false
	}
	if c := typ[0]; c == '.' || (c >= '0' && c <= '9') {
		return "", "", false
	}
	switch {
	case rest == "":
		return typ, "", true
	case rest[0] == ':':
		return typ, strings.TrimSpace(rest[1:]), true
	case strings.HasPrefix(rest, " ("):
		// "System.Net.Sockets.SocketException (10054): message"
		if j := strings.Index(rest, "):"); j > 0 && j < 32 {
			return typ, strings.TrimSpace(rest[j+2:]), true
		}
		return typ, "", true
	}
	return "", "", false
}

// exceptionTypes is ExceptionTypes' body.
func exceptionTypes(exc string) []string {
	segs := scanExceptions(exc)
	if len(segs) == 0 {
		return nil
	}
	out := make([]string, 0, maxExceptionTypes)
	out = append(out, exceptionLabel(segs[0].typ))
	if len(segs) > 1 {
		if inner := exceptionLabel(segs[len(segs)-1].typ); inner != out[0] {
			out = append(out, inner)
		}
	}
	return out
}
