// Package privacy is the pure part of code mode privacy (BL-365): what the broker does to text that leaves a host call or the
// sandbox, and the clock and random source the script sees. It has no dependency on the sandbox worker, the broker, the case
// store or the MCP server, so it runs and is tested on any OS. The broker (BL-363) calls it; nothing here spawns, reads files or
// opens a network.
//
// Where it sits (docs/desktop/spec/04-code-mode.md, D11 and D12):
//
//   - Hiding of user ids and user agents stays where it already is: the ten tools return "hidden-<rank>" when the operator has not
//     allowed identifiers (internal/mcp/breakdown.go). This package does NOT replace that. It is the second net: the Guard looks
//     through the answers, the logs and the errors for what must not be there, and gives every distinct value a token that is the
//     same for the whole run ([IP_1], [USER_2], [UA_1]), so a script that groups or counts by the token still can. A token comes
//     from the order in which values are first seen in the texts of one run, never from the value, its length, or the order the
//     identifiers were given in, so it says nothing across runs about the value. Caveat: with four host calls in flight the order of
//     first sight depends on which call reaches the Guard first, so two live runs of one script can number differently; the broker
//     stores the swept answer, and a replay serves the stored (swept) blobs, so replay does not depend on it.
//   - Scrub (ScrubText, ScrubError) removes what must never be in an error or a log line, in any mode: secrets (internal/secretscrub
//     does the secret forms), Authorization and cookie headers (also as JSON or a Go map), Bearer tokens, JWTs and common key
//     formats, connection string parts (ADO and libpq style), database and URL forms (any scheme keeps only its host), file paths
//     (Windows, UNC, Unix, ~ and ./), every single-quoted run (SQL strings in driver errors), and the values in the usual driver
//     details (Postgres "Failing row contains", "Key (..)=(..)", enum and syntax values, MySQL and SQL Server duplicate key, Mongo
//     "dup key"); internal/querytext does the shape of a whole statement. The text is cut to a size before any rule runs, at a
//     place that cannot leave the front of an address.
//   - ScriptClock and Rand are the D12 clock and Math.random: 10 ms steps from a recorded base time, a frozen mode for replay, and
//     xorshift128+ with a recorded seed. They take their time from a function the caller gives, so tests never wait.
//
// The Guard is the only way in. It has two sides, and the difference is the rule for text that looks like a token ("[IP_1]"):
//
//   - In: HostResult and HostError take raw data from a tool or a driver together with the identifiers found in it (IDs). The
//     identifiers are learned first, so nothing leaves before its values were learned. Text that looks like a token is escaped once,
//     one more "_" after the "[" ("[IP_1]" -> "[_IP_1]", "[_IP_1]" -> "[__IP_1]", any letter case; injective), so data cannot pass
//     for a token of this run.
//   - Out: Output, Log and ScriptError take what the script made. They sweep and scrub but do not escape, because everything the
//     script received was escaped on the way in, so a token in its output is a real one. Stored swept answers (a replay) go to the
//     script as they are and are never passed through HostResult again; Output of the same bytes in a new Guard is the same text.
//
// Order for text inside the Guard: cut, escape (In only), learned values (a user agent holds a URL, and a scrubber that cut the
// URL would break the match), scrubbers, then the address sweep as a last net. Mode allow turns the sweeper off (the operator allowed
// identifiers) and nothing else: secrets are scrubbed in every mode.
//
// Rules the code keeps:
//
//   - Fail closed. A result that is not valid JSON or not valid UTF-8 is refused, never passed on raw. A JSON text nested deeper than
//     maxDepth is refused. A scrubber that cannot tell where a value ends masks to the end (secretscrub does the same). An
//     identifier that cannot be taken in (over 16 KiB, or past 8192 values or 512 KiB in all) makes the call that carried it return
//     an error and the Guard Incomplete: every later call returns Withheld or ErrIncomplete, and the broker should end the run with
//     a code of its own for it. A text that costs more than the address scan budget (16 steps per byte) is withheld whole.
//   - Linear time and bounded memory. Every input is cut or refused above a constant (MaxJSONBytes, maxScrubInput, the scan
//     budget); no regular expression here backtracks (Go regexp is RE2); the hand-written scanners move forward only; learned values
//     are matched by an Aho-Corasick automaton built on a geometric schedule (a few hundred milliseconds for the largest allowed
//     set). A run holds at most maxTokens tokens per kind; the next distinct value gets one shared overflow token.
//   - Errors from this package never carry the offending value.
//
// Learned values: matched without regard to ASCII case. At least 6 bytes: anywhere in a text. 3 to 5 bytes: as a whole word (not
// touching a letter, digit or "_"). Shorter: only as a whole string, a member name or a whole JSON number. A learned number is
// replaced wherever it is a whole JSON number, however short, so a count equal to a learned id is hidden too (and "1.234567e6"
// counts as 1234567). Invisible format characters (Unicode Cf: zero width space, joiners, soft hyphen) are removed from swept text
// because they split an address or a name for a scanner and not for a reader.
//
// Not covered (listed so nobody assumes it): an identifier the Guard was not told about and that is not an IP address (a plain user
// id inside free text); an IP written in a form that net/netip and the dotted reader do not read (decimal "2130706433", octal,
// hex, "1.2.3" short form, fullwidth digits or full stops, "1%2E2%2E3%2E4", a dot written as a JSON escape, an address and a mask glued with no
// separator); a learned value written percent-encoded, JSON-escaped ("Mozilla\/5.0") or with non-ASCII letters in another case; an
// IPv6 address in the middle of a long run of hex digits and colons; a relative path without "./" ("home/alice/x"); anything the
// tools return in allow mode. Over-redaction, on purpose: a four-part number that reads as an address ("1.2.3.4.5", "1.0.0.0" as a
// version) and every number between the first and the last window of a dotted run; a "::" pair that parses as an address when it
// touches a digit; a learned id equal to an ordinary JSON number or a short word; every single-quoted run, every "key (...)", and
// every absolute Unix path of two parts in an error text; the words after an unquoted Windows path up to the next ":" or the end of
// the line; duplicate member names that end as one token get "#2", "#3" after the name.
package privacy
