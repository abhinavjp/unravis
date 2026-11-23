// Package privacy is the pure part of code mode privacy (BL-365): what the broker does to text that leaves a host call or the
// sandbox, and the clock and random source the script sees. It has no dependency on the sandbox worker, the broker, the case
// store or the MCP server, so it runs and is tested on any OS. The broker (BL-363) calls it; nothing here spawns, reads files or
// opens a network.
//
// Where it sits (docs/desktop/spec/04-code-mode.md, D11 and D12):
//
//   - Hiding of user ids and user agents stays where it already is: the ten tools return "hidden-<rank>" when the operator has not
//     allowed identifiers (internal/mcp/breakdown.go). This package does NOT replace that. It is the second net: a Sweeper looks
//     through the final result, the logs and the errors for what must not be there, and gives every distinct value a token that is
//     the same for the whole run ([IP_1], [USER_2], [UA_1]), so a script that groups or counts by the token still can. A token
//     comes from the order in which values are first seen in the texts of one run, never from the value, its length, or the order
//     Learn was called in, so it says nothing across runs about the value. Caveat: with four host calls in flight the order of first
//     sight depends on which call reaches the Sweeper first, so two live runs of one script can number differently; the broker
//     stores the swept result, and a replay serves the stored (swept) blobs, so replay does not depend on it. Text that already
//     looks like a token ("[USER_1]") in raw input is escaped to "[_USER_1]" so data cannot pass for a token.
//   - Scrub (ScrubError) removes what must never be in an error or a log line, in any mode: secrets (internal/secretscrub does
//     the secret forms), Authorization and cookie headers, Bearer tokens, JWTs and common key formats, connection string parts,
//     database and URL forms, file paths (Windows, UNC, Unix, ~ and ./), every single-quoted run (SQL strings in driver errors) and
//     the unquoted values of the usual duplicate key messages (internal/querytext does the shape of a whole statement), and cuts
//     the text to a size.
//   - ScriptClock and Rand are the D12 clock and Math.random: 10 ms steps from a recorded base time, a frozen mode for replay, and
//     xorshift128+ with a recorded seed. They take their time from a function the caller gives, so tests never wait.
//
// Guard puts the three together for one run, in this order for text: learned values first (a user agent holds a URL, and a scrubber
// that cut the URL would break the match), then the scrubbers, then the address sweep as a last net. Mode Allow turns the sweeper off (operator allowed identifiers) and nothing else:
// secrets are scrubbed in every mode.
//
// Rules the code keeps:
//
//   - Fail closed. A result that is not valid JSON is refused, never passed on raw. A JSON text nested deeper than maxDepth is
//     refused. A scrubber that cannot tell where a value ends masks to the end (secretscrub does the same).
//   - Linear time and bounded memory. Every input is cut or refused above a constant (MaxJSONBytes, maxScrubInput); no regular
//     expression here backtracks (Go regexp is RE2); the hand-written scanners move forward only. A run holds at most maxTokens
//     tokens per kind; the next distinct value gets one shared overflow token, so memory per run is bounded and nothing leaks.
//   - Errors from this package never carry the offending value.
//
// Fail closed on Learn: a value that cannot be taken in (over 16 KiB, or past 16384 values or 1 MiB in all) makes Learn return an
// error and the Guard Incomplete; Log, Error and Result then release nothing. A learned number is replaced wherever it is a whole
// JSON number, however short, so a count equal to a learned id is hidden too.
//
// Not covered (listed so nobody assumes it): an identifier the sweeper was not told about and that is not an IP address (a plain
// user id inside free text); an IP written in a form that net/netip does not read (decimal "2130706433", octal, "1.2.3" short
// form) or percent-encoded ("2001%3Adb8%3A%3A1"); a learned value written percent-encoded, JSON-escaped ("Mozilla\/5.0") or with
// non-ASCII letters in another case; an IPv6 address in the middle of a long run of hex digits and colons; anything the tools
// return in allow mode. Over-redaction, on purpose: a four-part number that reads as an address ("1.2.3.4.5", "1.0.0.0" as a
// version), a "::" pair that parses as an address when it touches a digit, a learned id equal to an ordinary JSON number, every
// single-quoted run and every absolute Unix path of two parts in an error text, and the words after an unquoted Windows path up to
// the next ":" or the end of the line.
package privacy
