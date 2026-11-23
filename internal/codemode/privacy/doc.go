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
//   - Out: Output, Log and ScriptError take what the script made. They sweep (Log and ScriptError also scrub) but do not escape,
//     because everything the script received was escaped on the way in, so a token in its output is a real one. They mint no
//     numbered token: an address the script wrote itself becomes "[IP_OUT]", a long learned value "[USER_OUT]" or "[UA_OUT]", the
//     same for every value. So the script cannot ask "which number is this guess?" (it cannot learn what [IP_1] stands for by
//     writing 65,536 guesses), the numbering of the in side is never touched by it, and Output of the same bytes in a new Guard
//     (a replay) is the same text. Tokens that came in pass through unchanged. A learned value is matched on the out side only from
//     12 bytes (user agents, e-mail addresses): a short id (4711) would turn every guess into a yes/no answer, so it is not matched
//     there at all, and an id the broker forgot to pass to HostResult is not caught on the out side if it is short. Stored swept
//     answers (a replay) go to the script as they are and are never passed through HostResult again.
//
// Order for text inside the Guard: cut, one normalisation (invisible characters and invalid bytes, see below), escape (In only,
// after the normalisation so an invisible character cannot hide a look-alike), learned values (a user agent holds a URL, and a
// scrubber that cut the URL would break the match), addresses (before the scrubbers, so a rule that stops at a ":" such as a UNC
// host cannot cut the front off an address), scrubbers, then the addresses again as a last net. Mode allow turns the sweeper off
// (the operator allowed identifiers) and nothing else: Log, ScriptError and HostError are scrubbed in every mode. Results (HostResult,
// Output) are JSON data: they are swept and validated but NOT scrubbed, in either mode (a string value that holds "Password=x" or
// a path passes); only error and log text is scrubbed.
//
// Rules the code keeps:
//
//   - Fail closed. A result that is not valid JSON or not valid UTF-8 is refused, never passed on raw. A JSON text nested deeper than
//     maxDepth is refused. A scrubber that cannot tell where a value ends masks to the end (secretscrub does the same). An
//     identifier that cannot be taken in (over 16 KiB, or past 8192 values or 512 KiB in all) makes the call that carried it return
//     an error and the Guard Incomplete: every later call returns Withheld or ErrIncomplete, and the broker should end the run with
//     a code of its own for it. A text that costs more than the address scan budget (16 steps per byte) is withheld whole as
//     "[IP_WITHHELD]"; that does not make the Guard Incomplete (the rest of the run is fine).
//   - Linear time and bounded memory. Every input is cut or refused above a constant (MaxJSONBytes, maxScrubInput, the scan
//     budget); no regular expression here backtracks (Go regexp is RE2); the hand-written scanners move forward only; learned values
//     are matched by an Aho-Corasick automaton built on a geometric schedule (a few hundred milliseconds for the largest allowed
//     set). A run holds at most maxTokens tokens per kind; the next distinct value gets one shared overflow token.
//   - Errors from this package never carry the offending value.
//
// Invisible characters and invalid bytes: one normalisation, the same for text and for every learned value, so they meet in one
// form. Bytes that are not valid UTF-8 become "?"; Unicode Cf characters (zero width space, joiners, soft hyphen, BOM) are removed.
// It runs once per text in linear time. Removing a character can glue a digit to the end of an address ("1.2.3.4" + BOM + "5"); the
// address reader takes an over-long first or last part of a four-part number whole ("9218.250.173.149", "1.2.3.4" + "5"), so that
// is still one token.
//
// Learned values: matched without regard to ASCII case. At least 6 bytes: anywhere in a text. 3 to 5 bytes: as a whole word (not
// touching a letter, digit or "_"). Shorter: only as a whole string, a member name or a whole JSON number. A learned number is
// replaced wherever it is a whole JSON number, however short, so a count equal to a learned id is hidden too (and "1.234567e6"
// counts as 1234567). Invisible format characters (Unicode Cf: zero width space, joiners, soft hyphen) are removed from swept text
// because they split an address or a name for a scanner and not for a reader.
//
// Known limits (accepted for the first release; the broker PR gets its own review):
//   - Numbers: a JSON number equal to a learned id is hidden, a negative one ("-1234567") is not. A learned value is matched in ASCII
//     case only ("\u212aelvin" with the Kelvin sign is not "kelvin").
//   - IPv6: an address that starts with a letter that is part of a word ("xdead:beef::cafe" gives "xdead:[IP_1]"), a zone glued to
//     the next address, and two addresses glued together leave a part; see the "long run of hex digits and colons" case below.
//   - Cosmetic or over-redaction: "map[password:hunter2]" loses its closing "]"; "open /home/alice/x: permission denied" masks
//     more than the path (secretscrub rule); `column "x"` and every "key (...)" are masked whatever they hold; a four-part
//     dotted number whose middle parts are 0 to 255 is an address even with a long first or last part ("2023.10.1.5").
//   - Short ids on the out side, long learned values on the out side: see Out above (a bare yes/no for a guessed value of 12 bytes
//     or more remains possible and is not practical to use).
//   - Numbering: with several host calls in flight the in-side tokens are numbered by the order in which the answers reach the
//     Guard; the broker should call it from one goroutine at a time or accept that two live runs can number differently (a replay
//     does not depend on it).
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
