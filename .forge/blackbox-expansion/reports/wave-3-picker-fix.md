# Wave 3 picker fix — W3-06 (Medium)

Branch: `feat/blackbox-expansion`. Scope: web app only. No `.go` file, no other Svelte
component, and no `package.json` was edited. No commit/push. No network upload, no browser
automation, no machine-config change.

## The finding

`showLists` was true whenever detection succeeded and at least one entry existed, and the
manual add row lived ONLY in the `{:else}` branch. The endpoint offers running services only
and caps each list at 256 (`internal/server/blackbox.go`, `bbRunningServices` / `bbTargetList`),
so a successful response need not contain the target the owner wants. With successful-but-
incomplete discovery the owner could neither tick the desired stopped service nor type its
name. Already-selected names absent from the list rendered as `extraPicked` chips with no
state label at all.

## What changed

- `web/src/lib/blackbox.ts`: added the pure helper `pickedTargetState(kind, name, answer)`
  and the type `PickedTargetState`. It reuses the existing `targetRunState` for a listed name
  and the per-source `appPoolsState` / `servicesState` for an absent one. `answer` is the
  `TargetsAnswer` (or `null` when nothing could be read). No polling, no new fetch.
- `web/src/components/BlackboxSettings.svelte`:
  - The manual add row (`div.bb-target-add`) moved OUT of the `{:else}` branch and now renders
    always, alongside the detected lists. The `{:else}` branch keeps only `targetsManualNote`.
    `addTarget` is unchanged: same trim/non-empty guard, same `toggleTarget(picked, manualKind,
    name)`. Server-side name validation and the target cap are untouched (not weakened).
  - Reads and keeps `appPoolsState` / `servicesState` from the single `onMount` read, and sets
    `targetsRead = true` on both success and failure. Still one read on mount, no polling.
  - `extraPicked` chips (and, when no lists are shown, all `picked` chips) now carry an honest
    state note via `chipNote`, gated by `chipsLabelled = targetMode === 'pc' && targetsRead`.
    The listed-checkbox stopped/unknown labels (W2-07) are unchanged.
  - When lists are shown, a new `targetsListNote` explains the list may be incomplete and that
    a name can be typed below.
- `web/src/lib/copy/blackboxCopy.ts`: added `targetsNotInList`, `targetsListUnreadable`,
  `targetsSourceNotFound`, `targetsListNote`. Plain language; all pass the copy lint.
- `web/tests/blackbox.targets.manual.test.cjs` (new): behavioural tests on `pickedTargetState`
  plus source-text assertions on the component, in the repo's `loadTypeScript` style.

## State labelling rules

For a selected target, `pickedTargetState` returns:

| Situation | State | Words shown |
| --- | --- | --- |
| Name in the detected list, state running | `running` | no note (list convention) |
| Name in the list, state stopped | `stopped` | `not running` |
| Name in the list, state unknown / unreadable | `unknown` | `running state unknown` |
| Name absent, that source `detected` (or older server `detected=true`) | `not-in-list` | `not in the detected list; running state unknown` |
| Name absent, that source `unavailable` (or nothing read at all) | `list-unreadable` | `list could not be read; running state unknown` |
| Name absent, that source `not-found` (e.g. no IIS) | `source-not-found` | `not detected on this PC; running state unknown` |

Key rule: a name missing from a capped or unreadable list is NEVER guessed to mean stopped.
Absence always maps to an "unknown" flavour. The two sources are judged independently, so a
readable pool list and an unreadable service list label their absent names differently.
Pack/remote (`manual`) mode never fetches and its chips stay unlabelled, exactly as before.
Only `kind` + `name` is ever stored — never a path, command line or key.

## Existing assertions updated

None required updating. No existing assertion in `web/tests/blackbox.targets.test.cjs` encoded
the old else-branch-only placement of the manual row (it asserts the `onMount` guard, the single
`targets()` read, no polling, the listed-checkbox stopped/unknown label markup, and the
kind+name helpers — all still true). That file is left unchanged and all 9 of its tests pass.
The new behaviour is covered by the new file. The component assertions in
`web/tests/scn1.l8.test.cjs` (props signature, field ids, 8 `aria-invalid`, bind warning) are
also untouched and still pass.

## Commands and results (exact)

Run from `C:/Users/nachi/Documents/Qoder/2026-11-13/e31b14d4/unravis/web`:

- `npx svelte-check --output human`
  -> `svelte-check found 0 errors and 0 warnings`
- `node --test tests/blackbox.targets.test.cjs tests/blackbox.targets.manual.test.cjs`
  -> `tests 22 | pass 22 | fail 0 | cancelled 0 | skipped 0` (9 existing + 13 new)
- `node --test tests/scn1.l8.test.cjs tests/blackbox.test.cjs` (regression guard on the edited
  component and copy)
  -> `tests 42 | pass 42 | fail 0`

`gofmt` is not applicable (no Go file touched). Working-tree CRLF line endings preserved on all
edited files; the new test file was written with CRLF.

## What still needs a real browser / real server to prove

These are source-level and pure-helper tests; they transpile TypeScript in memory and assert on
component source text. They do NOT render Svelte or contact a server. Still unproven here:

1. Rendered interaction: that with a real successful-but-incomplete `GET /this-pc/targets`
   response the add row is visibly present beside the lists, typing a capped-away/absent name
   and pressing Add target actually adds a chip with the correct note, and the checkbox for a
   listed stopped service shows "not running". Needs a browser (Svelte mount) or DOM-level test.
2. That the selection survives Review -> Install/Apply and is persisted by the real server
   (`internal/blackbox/config.go` validation, `service.go` install, `pack.go`). The cross-wave
   note in the review records that Apply does not persist the new config and that live
   install/reload was not attempted; that is outside this web-only fix.
3. Real per-source states from a real Windows host (IIS present/absent, service list readable/
   refused, a genuinely capped 256-entry list) and the resulting chip wording end-to-end.
4. Live runtime "not found / not running" evidence per target: the review notes missing names
   are discarded by `host.go`, so fixing the form alone cannot establish live per-target state.

No model tier is claimed.