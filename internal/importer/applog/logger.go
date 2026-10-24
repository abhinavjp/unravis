package applog

import (
	"errors"
	"regexp"
)

// ErrPseudonym refuses a file when a logger name outside the framework rule
// cannot be pseudonymised (plan §2.2: fail-closed).
var ErrPseudonym = errors.New("applog: logger pseudonym unavailable")

// frameworkLogger is the §2.2 logger rule, character for character the
// export's rule (internal/evidence/caseexport.go frameworkLogger, pinned by
// TestW2FExportValueRules in internal/evidence/w2f_vocab_test.go):
//
//   - System, Microsoft, MongoDB, Serilog, log4net, Hangfire, Polly, Grpc,
//     Azure, Newtonsoft or NHibernate, plus at most two more parts (three
//     parts in all);
//   - StackExchange.Redis plus at most one more part;
//   - ioredis or redis, alone.
//
// Each part is 1 to 64 letters, digits or "_". A name that matches is kept as
// it is; any other name (a deeper framework name included) becomes a keyed
// logger- pseudonym. TestLoggerRuleMatchesExport checks the two rules agree.
var frameworkLogger = regexp.MustCompile(`^(?:(?:System|Microsoft|MongoDB|Serilog|log4net|Hangfire|Polly|Grpc|Azure|` +
	`Newtonsoft|NHibernate)(?:\.[A-Za-z0-9_]{1,64}){0,2}|StackExchange\.Redis(?:\.[A-Za-z0-9_]{1,64})?|ioredis|redis)$`)

// loggerPseudonym is the shape a "logger" pseudonym must have before it is
// stored: the kind prefix and a short token of letters and digits.
var loggerPseudonym = regexp.MustCompile(`^logger-[0-9A-Za-z]{1,64}$`)

// loggerLabel is LoggerLabel's body.
func loggerLabel(cat string, ps func(kind, v string) string) (string, error) {
	if cat == "" {
		return "", nil
	}
	if frameworkLogger.MatchString(cat) {
		return cat, nil
	}
	if ps == nil {
		return "", ErrPseudonym
	}
	l := ps("logger", cat)
	if !loggerPseudonym.MatchString(l) {
		return "", ErrPseudonym
	}
	return l, nil
}
