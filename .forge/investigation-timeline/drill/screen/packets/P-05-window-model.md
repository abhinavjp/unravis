# P-05 Window model

model: sonnet, effort: medium. Reason: small pure logic with edge cases at the strip ends.
risk: low. Review batch: RB-1. Wave: W0. Requirement: R-02.

## Outcome
A pure module holds the one window the whole screen follows: presets, custom values, Earlier/Later, Back to this call, and the wide-window rule.

## Write scope
- `web/src/lib/drill/callWindow.ts` (new)
- `web/tests/slowcall.window.test.cjs` (new)

## Must not change
- Nothing else.

## Changes
1. Export:
```ts
export const WINDOW_PRESETS = [5, 10, 15, 30, 60] as const;      // minutes each side
export const WINDOW_MIN = 1, WINDOW_MAX = 120;                    // custom, minutes
export const OVERVIEW_MINUTES = 60;                               // strip = call start ± 60 min
export const WIDE_SECONDS = 901;                                  // beyond ±15 min the server is asked
export interface CallWindow { before: number; after: number; shiftMs: number }   // minutes, minutes, ms moved from the call
export const DEFAULT_WINDOW: CallWindow = { before: 15, after: 15, shiftMs: 0 };
export function clampMinutes(value: unknown): number | null;      // integer 1..120, else null
export function windowBounds(callStartMs: number, w: CallWindow): { start: number; end: number };
export function overviewBounds(callStartMs: number): { start: number; end: number };
export function coversOverview(callStartMs: number, w: CallWindow): boolean;    // true = hide the strip
export function presetOf(w: CallWindow): number | null;           // 15 when before === after === 15 and shiftMs === 0
export function stepWindow(callStartMs: number, w: CallWindow, dir: -1 | 1): CallWindow;   // half a window, clamped so the window stays inside the overview
export function canStep(callStartMs: number, w: CallWindow, dir: -1 | 1): boolean;
export function centreOn(callStartMs: number, w: CallWindow, centreMs: number): CallWindow; // minimap drag, clamped inside the overview
export function backToCall(w: CallWindow): CallWindow;            // shiftMs = 0
export function isWide(w: CallWindow): boolean;                   // before*60 > 901-1 or after*60 > 901-1, i.e. more than 15 min on a side, or shiftMs !== 0
export function callInside(callStartMs: number, callEndMs: number, w: CallWindow): boolean;
```
2. `windowBounds`: `start = callStartMs - before*60000 + shiftMs`, `end = callStartMs + after*60000 + shiftMs`.
3. A window longer than the overview is allowed (custom up to 120); then `coversOverview` is true and `canStep` is false both ways.
4. Tests: each preset; `clampMinutes` for `0, 1, 120, 121, 1.5, '15', NaN, null`; step right at the right end is refused; `centreOn` clamps at both ends; `isWide` false for ±15 unmoved, true for ±30, true for ±15 moved; `presetOf` null when moved; `callInside` false after two steps of a ±5 window.

## Proof
- [ ] `cd web && node --test tests/slowcall.window.test.cjs` → pass, 0 fail, at least 15 asserts.
- [ ] `cd web && npx svelte-check --threshold error` → 0 errors.

## Depends on
Nothing.
