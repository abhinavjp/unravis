# Authoring brief (common rules)

You are AUTHORING a work packet for another agent to implement blindly. You do NOT implement it. You may read the repository to find exact current text.

Deliver two files as `### <path>` fenced blocks, paths relative to `buildout/packets/`:
1. `<PACKET>.md` the packet.
2. `data/chk-<id>.js` a vm-based check script that prints `PASS` or `FAIL <reason>`.

Pattern to copy exactly (read them first): `S3-recurrence.md` + `data/chk-s3.js`, and `S2c-pager.md` + `data/chk-s2c.js` if present. Match their headers, "Output" section and block format.

Packet rules (the implementer has no repo access beyond the named files and does zero analysis):
- Header, then a contract: every function, global, HTML string, CSS rule and copy text spelled out exactly. No "something like". No choices left open.
- Every edit to an existing file is a SEARCH/REPLACE block whose SEARCH is copied byte-for-byte from the current file and occurs EXACTLY ONCE. Verify each with grep -c -F before you finish. Prefer new files (`parts/NN-name.js`, `parts/02-css-name.html`) over edits.
- ES5 only (var, function, for; no arrows, template literals, let, const). New globals listed by name. Code runs inside one concatenated script; parts load in sorted filename order ('-' sorts before '.'). Do not read `W` at load time in `1x-` parts.
- CSS tokens only: --ok --warn --bad --good --accent --text --text-2 --line --surface --surface-2 --r.
- Read-only product; no SQL probes; fixed-rule engine; no runtime LLM.
- The check script must exercise every contract item (render output substrings, counts, edge cases, ES5 scan, wiring greps, `new Function(src)` syntax of edited files). It must not contain no-op assertions.
- Run your check script against your OWN reasoning only by `node --check`; do not apply the packet.
- Packet Output section for the implementer: blocks, then exactly 3 lines `Status: PASS|FAIL`, `Checks: ...`, `Concerns: ...`.

Your final reply: only the two fenced file blocks, then 3 lines: `Status: PASS|FAIL`, `Checks: <greps run, counts>`, `Concerns: none` or one line.
Do not launch sub-agents. Make only these two files. Keep going until both are complete.

Addendum: embed the COMPLETE content in the packet so it can be applied mechanically: every new file as a `### parts/<name>` fenced block and every edit as a `### parts/<file>` SEARCH/REPLACE block (no fence) inside the packet file, which you wrap in a four-backtick fence. The packet file is the single source of truth.
