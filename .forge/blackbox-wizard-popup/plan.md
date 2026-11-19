# Plan: blackbox-wizard-popup (detailed mode)

Read first: `context.md` (evidence) and `decisions.md` (settled, blockers B-01..B-11, open OD-1..OD-9).
Graph: `GRAPH.md`. Phases: `phases/NN-slug/phase.md`. Tasks: `phases/NN-slug/TSK-NN-slug.md`.

```yaml
mode: detailed
commit_granularity: phase
history_style: separate
branch: feat/blackbox-wizard-popup   # see OD-3 for the base
packets: 22
```

Rules for every task:
- Change files only inside the task's Write scope. Any change outside it is a deviation and is written down in `progress.md` with the reason.
- Never send a field the server does not know (`DisallowUnknownFields`). `probe.enabled` is always `false`.
- No demo-only UI. Every control either changes the real `Settings` payload, or is plainly read-only and says why.
- Copy is beginner-friendly and passes `lintScreenStrings`.
- Do not build anything in decisions.md S-16.

## Phases

| Phase | Slug | Tasks | Goal |
|---|---|---|---|
| 01 | contracts | TSK-01..05 | Rules, state, catalogue and copy, drives endpoint and client. All logic in `.ts`, unit-tested. |
| 02 | shell | TSK-06..08 | Dialog shell, progress rail, shared controls. |
| 03 | steps | TSK-09..17 | The 8 steps, the extracted target picker, and the wizard that wires them to the real API. |
| 04 | integration | TSK-18..19 | Mount in the Blackbox page, delete the drawer wizard, rebuild `web/dist`. |
| 05 | verify | TSK-20..22 | Reviews, clumsy-user pass, owner live-data run, BACKLOG ticks. |

## Shared contracts

### C-1 Wizard state module: `web/src/lib/blackboxWizard.ts` (TSK-01)
Pure TypeScript, no Svelte imports, so `loadTypeScript` can test it.

```ts
export type StepId = 'name' | 'where' | 'record' | 'sql' | 'watch' | 'keep' | 'folder' | 'review';
export type Where = 'pc' | 'pack';
export type PresetId = 'recommended' | 'light' | 'custom';
export type WatchMode = 'none' | 'some';

export interface WizardState {
  step: StepId;
  maxSeen: number;            // index of the furthest step reached; rail unlocks 0..maxSeen
  returnTo: 'review' | null;  // set by a Review "Change" link
  where: Where | null;
  settings: Settings;         // the one payload; starts from defaultSettings()
  logPathsText: string;       // textarea mirror of settings.logPaths
  watchMode: WatchMode;
  folderTouched: boolean;     // the owner typed or picked; stop auto-default
  confirmSystemDrive: boolean;// the C: tick
  sqlSeen: boolean;           // SQL Server seen in the this-pc services list (B-01)
}

export function steps(s: Pick<WizardState, 'where' | 'sqlSeen'>): StepId[];  // 8 with sql, else 7
export function stepIndex(s: WizardState): number;       // 1-based for "Step X of N"
export function canVisit(s: WizardState, id: StepId): boolean;   // index <= maxSeen
export function stepProblem(s: WizardState, id: StepId): string | null; // uses nameProblem, targetsProblem, settingsProblem, folder rules
export function next(s: WizardState): WizardState;      // refuses when stepProblem; honours returnTo
export function back(s: WizardState): WizardState;
export function goTo(s: WizardState, id: StepId): WizardState;   // only when canVisit
export function presetOf(record: Settings['record']): PresetId;  // B-03
export function applyPreset(s: WizardState, p: Exclude<PresetId, 'custom'>): WizardState;
export function useDefaultsToReview(s: WizardState): WizardState; // fills defaults for record/sql/watch, then step = 'review'
export function defaultFolder(drives: DriveInfo[] | null, fallback: string): { folder: string; needsSystemTick: boolean };
export function driveOfPath(path: string): string | null;  // 'D' for D:\..., null for UNC
export function buildSettings(s: WizardState): Settings;   // the payload for validate/install/pack
```

### C-2 Catalogue and copy (TSK-02)
- `web/src/lib/blackboxCatalogue.ts`: `RecordGroup { id, title, always: boolean, toggle?: 'processes' | 'logsCatalogue' | 'copyLogsOnSnapshot', sqlHost?: true, figures: Figure[] }` and `Figure { id, label, details: { what, why, watch, stored } }`.
  - Every group and figure points to a type in `groups_types.go` or `sqlhost.go` (a `source` string in a comment, checked in review).
  - No VM group. No `bbsql` deeper checks as tickable items.
- `web/src/lib/copy/blackboxWizardCopy.ts`: `BBW_TEXT`, a flat object. Key prefixes: `step.<id>.title`, `step.<id>.lead`, `step.<id>.help`, `rail.*`, `footer.*`, `leave.*`, `record.*`, `sql.*`, `watch.*`, `keep.*`, `folder.*`, `review.*`, `result.*`, `notice.*`. Plus `blackboxWizardCopyEntries()` for the copy-lint test, in the same style as `blackboxCopyEntries` in `blackboxCopy.ts`.
- Existing `BB_TEXT` and `BB_REFUSAL` stay the source for refusal and server-error text. Do not duplicate them.

### C-3 API client shape
- Existing (unchanged): `blackboxApi.thisPc()`, `.targets()`, `.validate(settings)`, `.install(settings)`, `.pack(settings)`, plus `saveBlob`.
- New (TSK-05), `web/src/lib/blackboxDrives.ts`:

```ts
export type DriveType = 'fixed' | 'removable' | 'network' | 'other';
export interface DriveInfo { letter: string; label: string; type: DriveType; system: boolean; freeBytes: number | null; totalBytes: number | null; }
export interface DrivesAnswer { drives: DriveInfo[]; }
export function fetchDrives(): Promise<DrivesAnswer>; // GET /api/blackbox/this-pc/drives
export function eligibleDrives(d: DriveInfo[]): DriveInfo[]; // fixed, not system; system C: is kept but flagged
```

- New route (TSK-04): `GET /api/blackbox/this-pc/drives` → `{"drives":[{"letter":"D","label":"Data","type":"fixed","system":false,"freeBytes":123,"totalBytes":456}]}`. Read-only, this PC only, no paths beyond the letter, no error text from the OS. It uses the existing loopback and auth guards of the other `this-pc` routes.

### C-4 Dialog shell and rail interface (TSK-06, TSK-07)

```svelte
<!-- WizardDialog.svelte -->
props: { open: boolean; titleId: string; busy: boolean; finished: boolean;
         onLeave: () => void;                // after the owner confirms Leave, or directly when finished
         onPrimary: () => void;              // Enter and the primary button
         header: Snippet; rail: Snippet; body: Snippet; footer: Snippet }
behaviour: showModal() on open; the 'cancel' event is prevented and opens the leave layer
           (role="alertdialog", "Keep going" focused first); Esc while busy does nothing and says why;
           Enter calls onPrimary unless focus is on a button, summary, textarea or an input that has its own Enter;
           exposes focusHeading() which moves focus to the h2 (tabindex -1) on step change;
           full screen at max-width 640px; prefers-reduced-motion drops transitions.

<!-- ProgressRail.svelte -->
props: { steps: { id: string; label: string }[]; current: number; maxSeen: number; disabled: boolean;
         onGo: (index: number) => void }
renders <ol aria-label="Progress, step X of N">; each dot is a <button>, disabled beyond maxSeen,
aria-current="step" on the current one; at ≤640px only the current label shows.
```

Shared controls (TSK-08): `SegmentedControl.svelte` (radio group, 2-5 options, arrow keys), `RadioCards.svelte` (either/or, title + one line each), `FigureDetails.svelte` (`<details>` with the four parts). All at least 40 px tall.

### C-5 Settings payload to install, apply and pack
- `buildSettings(state)` returns a full `Settings`. It starts from `defaultSettings()` and only overwrites fields the steps own:
  - Name → `name`
  - Record → `record.*`, `intervalSeconds`, `logPaths`
  - Watch → `targets` (empty when `watchMode = 'none'`)
  - Keep → `keepDays`, `maxGB`, `minFreePercent`
  - Folder → `folder`, `port`, `bind`
- `probe` is always `{ enabled: false }`. No new keys.
- The same object goes to `blackboxApi.validate` before Review, then to `install` (Where = pc) or `pack` (Where = pack).
- Apply settings keeps using `BlackboxSettings.svelte`; its payload does not change (TSK-12 is a pure extraction).

## Requirement traceability

| Requirement | Packets |
|---|---|
| BL-130 pop-up, rail, honest progress, no stray clicks, segmented controls, preset → Custom, drive-aware default folder | TSK-01, 03, 04, 05, 06, 07, 08, 10, 15, 17, 18 |
| BL-131 Remove and Status | Not in this plan (OD-2). Shell reusable from TSK-06. |
| BL-132 what is recorded, Details per figure, from the real catalogue | TSK-02, 08, 10, 11, 16 |
| BL-133 SQL step | Front half only: info step (TSK-11) per B-01. Backend ticks are deferred. |
| BL-134 deeper checks, saved connection | Not built (B-02, S-16). |
| BL-135 Watch step | TSK-12, 13, 16, 22 |
| S-01, S-04, S-05 | TSK-06, 07, 17, 20 |
| S-02 step list, S-03 Place first | TSK-01, 17 |
| S-06 controls | TSK-08, 20 |
| S-07 presets and defaults shortcut | TSK-01, 10, 17 |
| S-08, S-09, S-14, S-15 | TSK-02, 10, 11, 13 |
| S-10 target rules | TSK-12, 13 (reuse `targetsProblem`) |
| S-11 folder | TSK-03, 04, 05, 15, 16 |
| S-12 keep | TSK-14 |
| B-06 name rule | TSK-02, 09 |
| B-07 log folders | TSK-10 |
| B-08 keep ranges | TSK-14 |
| B-10 Apply must not regress | TSK-12, 20, 22 |
| B-11 pack and this-PC gating | TSK-09, 17 |

## Checkpoints

1. **End of phase 01:** `cd web && npm test && npm run check`, plus `go test ./internal/server/... ./internal/blackbox/...`. Merge-sentinel review of TSK-04 (new route: loopback guard, no OS text leaked).
2. **End of phase 02:** `npm test && npm run check`. A11y check of the shell: focus starts on the heading, Esc opens the leave layer, Tab stays inside the dialog, `aria-current`, `aria-labelledby`.
3. **End of phase 03:** `npm test && npm run check && npm run build`. Merge-sentinel review of TSK-12 (Apply regression) and TSK-17 (payload and submit).
4. **End of phase 04:** `go test ./...`, `cd web && npm test && npm run check && npm run build`. `web/dist` is committed and embedded in the exe, so it must be rebuilt and committed in the same phase commit (CI does not check it; BL-031).
5. **Phase 05:** merge-sentinel over the whole branch diff; a11y review; wireframe conformance (variant A only, with the blockers' rulings); clumsy-user review with one fixer pass; then the owner live-data run.

## What can only be checked live (TSK-22, owner-run)
- A real install on a real server through "Place on this PC": service created, `blackbox.json` written to the chosen folder, the read key sealed, the entry in the list.
- The Watch list on a real IIS box: app pools from applicationHost.config, running services, the "state unknown" and "not running" notes, a typed name for a stopped service.
- A pack built, copied to another server and installed there with the chosen settings.
- The drives list and default folder on a machine with D:, without D:, with a USB stick and with a mapped network drive.
- A UNC folder: whether the service account can actually write there.
- SQL info step shows on a box with SQL Server and is hidden on one without.
- A perfmon comparison is not needed.

## Reuse and gaps
- Reuse: `blackbox.ts` rules and API (`defaultSettings`, `nameProblem`, `targetsProblem`, `settingsProblem`, `validBind`, `listensOnAll`, `targetNamesByKind`, `saveBlob`); `BB_TEXT` and `BB_REFUSAL`; the target picker logic inside `BlackboxSettings.svelte` (extracted by TSK-12); `copylint.ts`; patterns from `importPopup/` (focus trap, stepper that only goes back, state class); Windows drive helpers in `archive_path_windows.go`.
- Gaps (built here): native `<dialog>` shell, progress rail, segmented control, radio cards, Details disclosure, figure catalogue, drives route and client, UNC on the client.
- Gaps (not built): per-figure and SQL counter switches (BL-133 backend), deeper checks (BL-134), VM (BL-113), Remove/Status pop-up (BL-131).

## Not in this plan
BL-131 (Remove/Status), BL-133 backend, BL-134, BL-113, the certificate path, per-check DMV choice, any new collector or agent.
