# BBX-1-12 Install-wizard target picker (web)
Backlog: BL-117 | Group: 1 | Model: performance | Reason: Svelte 5 runes UI + client + copy + tests; one concern but several frontend files.
Depends on: BBX-1-10 | Blocks: none (group 1 complete after this)

## Goal
Let the owner pick app pools and services in the Blackbox install wizard's Settings step: offer detected running targets (from the new endpoint) as checkboxes, and allow manual name entry for the pack/remote path. Selections are saved into `settings.targets`.

## Context
- Files: `web/src/components/BlackboxSettings.svelte` (Settings step; `record.processes` checkbox at line 26, note 27), `web/src/lib/blackbox.ts` (`Settings` type 96-108, `blackboxApi` 210-237, `defaultSettings()` 269-275), `web/src/lib/copy/blackboxCopy.ts` (settings copy ~165-203). Test: `web/tests/blackbox.test.cjs` (count guard 135-139 + lint) — add a new test file `web/tests/blackbox.targets.test.cjs` if cleaner.
- Stack: Svelte 5 runes, Vite, tests are `node --test tests/*.test.cjs` (node:test + assert; TS transpiled in-memory). Run `cd web && npm test`.
- The endpoint `GET /api/blackbox/this-pc/targets` (BBX-1-10) returns `{appPools:[{name,running}], services:[{name,running}], detected}`.
- Rules that apply: **owner-selected targets only**; **detected, not typed, where possible**; **no text** beyond names; UI text must be understandable to a first-time investigator (handoff §7).
- Copy count rule (survey): adding plain `blackboxCopy` strings is auto-counted; keep `blackboxCopyEntries().length > 80` and the lint passing. Do NOT add banned wording (because|due to|caus|… — see copylint.ts).

## Do
1. `blackbox.ts`:
   - Add to `Settings`: `targets: { kind: 'appPool' | 'service'; name: string }[]`.
   - `defaultSettings()`: `targets: []`.
   - Add `blackboxApi.targets(): Promise<{appPools:{name:string;running:boolean}[]; services:{name:string;running:boolean}[]; detected:boolean}>` calling `GET /api/blackbox/this-pc/targets` (same auth header pattern as other methods).
2. `BlackboxSettings.svelte`:
   - Add a "Targets (app pools and services)" section. On mount (this-PC mode), call `blackboxApi.targets()`; render two checkbox lists (app pools, services) of detected names. Checking a name adds `{kind,name}` to `settings.targets`; unchecking removes it.
   - When `detected=false` or in pack/remote mode, show a manual entry row: a kind select (appPool/service) + a name text input + Add button; added names appear as removable chips.
   - Show a short helper line: "Pick the app pools and services to watch in detail. Other counters stay machine-wide." and, for a selected target that is not running, the words "not running" (never 0).
   - Keep the existing `record.processes` checkbox and note unchanged.
3. `blackboxCopy.ts`: add plain string keys for the section heading, helper line, "not running", "Add target", kind labels. First-time-investigator wording, no banned terms.
4. Tests (`web/tests/blackbox.targets.test.cjs`): 
   - settings round-trip keeps targets; defaultSettings has empty targets.
   - the targets() client hits the right path (mock fetch).
   - copy entries lint clean and count guard holds.
   - a pure helper `toggleTarget(targets, kind, name)` add/remove logic (factor it into blackbox.ts so it tests without DOM).

## Do not
- Do not fetch targets for remote/pack mode automatically (this-PC only); manual entry there.
- Do not store anything but kind+name (no paths, command lines, keys).
- Do not break the existing wizard parts (Name/Where/Settings/Review) or the count/lint guards.
- Do not use banned copy wording; do not add a DRILL_HELP-style fixed-count key here.
- Do not introduce a new UI library.

## Verification checklist
- [ ] cd web && npm test passes (new tests included)
- [ ] cd web && npm run check passes (svelte-check)
- [ ] go build ./... still passes (no Go change expected)
- [ ] Footprint: targets fetched once on mount, not polled
- [ ] Missing reading is omitted, never 0 (not-running target shows "not running")
- [ ] No text captured beyond kind+name
- [ ] Copy lint + count guards pass; wording readable for a first-time investigator
- [ ] Existing blackbox web tests still pass

## Live-check needed?
YES — owner confirms in the real wizard on a server with IIS: detected pools/services appear and can be selected; a pack install allows manual entry. Fake tests verify logic/shape only. (Also run the dev server and click through the wizard before reporting done — handoff UI rule.)

## Report back
Files changed, each checklist item ticked or failed with output, anything unconfirmed. Full report to `.forge/blackbox-expansion/reports/BBX-1-12.md`.