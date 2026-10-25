# TSK-21 web fixes (L2, L3, L4, M1 web part)

Web only. No Go, sql/, .claude/ or .mcp.json touched. Nothing committed.

## Changes
- L2: `keysMatch(pass, confirm)` in `web/src/lib/masterKey.ts` compares both entries NFC-normalised. `setupProblem` and the `mismatch` line in `MasterKeySetup.svelte` use it.
- L3: `hasEdgeSpace(pass)` plus `edgeSpaceNote()` (copy/masterKeyCopy.ts). `strengthHint` appends one gentle sentence ("The key starts or ends with a space. The space counts as part of the key, so you will need to type it again each time."). The key is never trimmed or changed; set-up is still allowed.
- L4: `normaliseCode` in `web/src/lib/caseCode.ts` runs `normalize('NFKC')` before the existing clean, lowercase and pattern check (the 256-char cap still applies first). A full-width 1 is still refused.
- M1 web part: new `web/src/components/RewrapNotice.svelte` (button calls `rewrapCases({ all: true })`, shows the existing `rewrapOffer`/`rewrapAction`/`rewrapDone` wording, a new `KEY_TEXT.rewrapFailed` line on a thrown error, can be pressed again). `StartScreen.svelte` (Home) shows it when `offersRewrap(lock, app.dpapiCases)` is true, i.e. state is unlocked and dpapiCases > 0. After a try it updates the app status with the number still failing. It uses only `state` and `dpapiCases`, which masterKeyApi.ts already reads; no new server fields.

## Dependency on the Go agent
The notice depends on the status route still reporting a correct `dpapiCases` for a case left half re-wrapped by a cut. If the server counts such a case as already master-key (manifest says master key) the count may be 0 and the notice would not show for it; that is a server-side question, not guessed at here. The web bug test (rewrapCases reachable from a screen other than the set-up panel) is satisfied.

## Tests
- `web/tests/clumsy.wizard.test.cjs`: the 4 `bug:` tests renamed to `fixed:` (bodies unchanged); 3 new `p06:` tests (offersRewrap and Home wiring, edge-space hint and keysMatch, full-width code folding including digits and dashes).
- `npm test`: 1324 tests, 1324 pass, 0 fail (was 1321 with 4 intended failures). `npm run check`: 0 errors, 0 warnings. `npm run build` not run.

## Files
web/src/lib/masterKey.ts, web/src/lib/caseCode.ts, web/src/lib/copy/masterKeyCopy.ts, web/src/components/MasterKeySetup.svelte, web/src/components/StartScreen.svelte, web/src/components/RewrapNotice.svelte (new), web/tests/clumsy.wizard.test.cjs. CRLF kept in files that had it; caseCode.ts and StartScreen.svelte were already LF and stayed LF.
