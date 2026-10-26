# Design v2 addendum: theme, help layer, and the chosen wireframes

Applies on top of `2026-10-25-drilldown-chart.md` and `2026-10-25-more-filters-redesign.md`. Where this file disagrees with those, this file wins.

**Decisions from the wireframe review (2026-10-25):**
- Drill-down: variant B, "Navigator under chart".
- More filters: variant B, "Group rail".
- Theme: variant C "Terminal" with the IBM Plex Sans and IBM Plex Mono pair, for the whole app.
- Every group, field and control gets plain-English help, and an optional "In simple words" (ELI10) explanation.

**Regression bar (user's standing requirement: no regression in design, performance, security, function):**
- Baseline: web tests 778 pass, `npm run check` 0 errors, `go test ./...` green. None may drop. Existing tests are updated only where this file or the two plans say so, with a note in the commit body, never deleted silently.
- Security: the server CSP stays exactly as it is (`font-src 'self' data:`). Fonts are bundled files served from `'self'`. `web/dist` must contain no `http://` or `https://` URL other than `www.w3.org` namespace strings. No new network calls, no `innerHTML` of data, no new `eval`.
- Performance: the JS bundle must not grow by more than 8 KB gzip. Fonts total at most 220 KB, Latin subset, woff2, `font-display: swap`. No new animation. No per-frame work added to charts.
- Function: every pinned selector listed in the filters plan survives. Keyboard and Esc behaviour is unchanged. Chart zoom, drill-down, drawers, import, discover and export behave as before.
- Design: AA contrast (4.5:1 for text, 3:1 for control lines and focus rings) in both themes, checked by a test that computes the ratio from the tokens. No horizontal overflow at 390, 768, 1280, 1440. Both themes and `prefers-reduced-motion` verified.
- After each phase: merge, rebuild, restart, one clumsiest-user review.

---

## A. Theme: Terminal with IBM Plex

**Files:** `web/src/app.css` (tokens and base type), new `web/src/fonts/*.woff2` plus `OFL.txt`, new `web/tests/theme.test.cjs`. Other component `<style>` blocks change only where a literal radius, font or colour blocks the new look.

**Tokens.** Dark blocks (`:root` and any dark forced block) take the values below. Token names do not change.

| Token | Dark value |
|---|---|
| `--bg` | `#050807` |
| `--surface` | `#0a0f0d` |
| `--surface-2` | `#101714` |
| `--line` | `#1a2621` |
| `--line-soft` | `#131c18` |
| `--control-line` | `#6b8478` |
| `--text` | `#eef3f1` |
| `--text-2` | `#93a39b` |
| `--accent` | `#3fd6b0` |
| `--accent-ink` | `#03130e` |
| `--focus` | `#7ff0d0` |
| `--warn` | `#e3b341` |
| `--bad` | `#ff7b72` |
| `--pending` | `#8fa39a` |
| `--chart-empty` | `#0a0f0d` |

- `--text-3 #5d6e66` from the wireframe is not a token. Anything that needs dimmer text uses `--text-2`. Decorative only, never body text.
- Series, heat and status colours stay data colours. The implementer checks that `--series-1` is still distinguishable from `--accent` and adjusts only `--series-*` if it is not. Healthy still stays neutral, never green.
- **Light theme ruling:** it keeps the warm-paper neutrals and gets the same shape and type. The accent becomes `#0b6b57` (AA on white), `--focus` `#0b6b57`. Both light blocks stay identical to each other. Exactly three `--control-line: #hex` declarations must remain (an existing test pins this).

**Type and shape.**
- `--font: "IBM Plex Sans", "Segoe UI Variable Text", system-ui, sans-serif` and `--mono: "IBM Plex Mono", ui-monospace, "Cascadia Code", Consolas, monospace`. Fallbacks mean an unloaded font never breaks layout.
- `--radius: 2px`, `--radius-sm: 2px`. Square corners everywhere, including chips and buttons.
- Root `font-size` stays 14px (rem-based rules exist). The dense feel comes from a 13px base in `body` and tighter control padding, not from changing the root.
- Mono is used for numbers, table cells, ids, times and small labels. Prose, help text and the ELI10 block stay in Plex Sans so long text stays readable.
- Panel and group titles are mono, 12px, weight 500, with a decorative `//` prefix. Use `content: '// ' / ''` so assistive tech does not read it. Labels stay sentence case (no uppercase, no eyebrows).
- No glow, gradient, banner or glass. These belong to the rejected variant A.

**Fonts.**
- Files come from the official `@fontsource/ibm-plex-sans` and `@fontsource/ibm-plex-mono` npm packages (SIL OFL 1.1). Fetch them into a scratch directory with `npm pack`, copy only the Latin woff2 files for Sans 400, 500, 600 and Mono 400, 500 into `web/src/fonts/`, plus `OFL.txt` with the licence text. Do not add the packages to `web/package.json`. Do not commit `web/package-lock.json`.
- `@font-face` rules go at the top of `app.css` with relative `url()`s, `font-display: swap`, and `unicode-range` for Latin.
- Tests: every `url()` in `@font-face` points at a file that exists; no `@import` and no `http` in `app.css`; total woff2 size at most 220 KB.

**Tests (`theme.test.cjs`):** contrast ratios computed from the token values in the three blocks (text and text-2 on bg, surface and surface-2 at 4.5:1; control-line, accent and focus at 3:1; accent-ink on accent at 4.5:1); the three-block `--control-line` rule; both light blocks identical; radius tokens; font stacks start with the Plex names; no hex colour added outside the `:root` blocks.

## B. Drill-down changes (replaces parts of the drill-down plan)

- Task 6 layout becomes chart-first. The brush sits **under the chart** as the navigator (it is the overview svg plus two handles, as in the plan). An "Investigate this window" chip floats on the chart, and only shows once the view is drilled. The strip row keeps Back to …, the crumbs, Zoom in, Zoom out and Reset. Earlier and Later move to the navigator (drag the body, arrow keys, PageUp and PageDown). All six actions keep working by keyboard.
- The strip is always visible, with no `<details>`.
- Help: a one-line plain description of what the navigator is (always visible) and a "?" button that opens an ELI10 block. Copy comes from section D.
- Everything else in the plan stands, including the 120-point rule, the 1-minute floor, whole-second promotion, the 10-entry trail, and the review focus items.

## C. More filters changes (replaces parts of the filters plan)

- The five groups (When, What, Request details, Ranges, Chart) keep their components and their content. Task 5 integration renders them as a **group rail**: a vertical list of the five group names with their counts on the left, and one pane at a time on the right. Below 720px the rail becomes a horizontal tab row (`role="tablist"`, arrow keys move, Home and End jump). The first group with an active filter is selected on open, otherwise When.
- Hidden panes stay mounted (use `hidden`) so drafts and their sync effects keep working. A validation error in a hidden pane shows a dot on its rail item and lists it in the panel header, so an error can never hide.
- The top of the panel keeps the count line, the stale hint and the removable chips with Clear all.
- The 1100px two-column grid and the `grid-template-areas` from the filters plan are dropped. The scroll-inside-the-panel rule above 720px stays.
- Update Task 5 tests for the rail (tablist, tab and tabpanel roles, the error dot) and keep every pinned selector.

## D. Help layer (new)

**Files:** new `web/src/lib/copy/helpCopy.ts`, new `web/src/components/HelpNote.svelte`, new `web/tests/help.test.cjs`. The ELI10 switch lives in the app header and in app state.

- `helpCopy.ts` exports `help: Record<string, { plain: string; simple: string }>` keyed by id. Required keys: one per filter group (`when`, `what`, `details`, `ranges`, `chart`), one per field (every id in the filters plan's group table), `drill-navigator`, `drill-investigate`, `drill-back`, `pulse`, `where`, `inspector`, `resource`, `patterns`, `ledger`, `export`, `import`, `discover`.
- `plain` is always visible, one sentence. `simple` is the "In simple words" explanation, two sentences at most, with an everyday comparison.
- Source meanings from the existing `Glossary` and `Hint` wording so help never contradicts the glossary ("unknown kind", observed time versus API time, Completion). Banned words from the copy lint apply (cause, because, due to, waiting on, blocked by, led to, result in, proof). Add no glossary terms.
- `HelpNote.svelte` props: `{ id: string }`. It renders the `plain` line, and a "?" button (`aria-expanded`, `aria-controls`) that toggles the `simple` block, which is labelled "In simple words". A global `eli10` flag (header checkbox "Explain simply", stored in memory plus `localStorage` in try/catch) opens every block.
- Tests: every required key exists and has non-empty `plain` and `simple`; every `<HelpNote id>` used in a component exists in the table; every string passes the copy lint; the toggle sets `aria-expanded`; the flag survives a `localStorage` that throws.

## E. Order of work

1. **Wave 1, parallel, independent files:** A theme; drill-down Tasks 1 and 3; filters Task 1.
2. **Wave 2, parallel:** filters Tasks 2, 3 and 4; drill-down Task 2; help layer D (copy and component only).
3. **Wave 3, parallel:** drill-down Tasks 4 and 5; filters Task 5 (with C).
4. **Wave 4:** drill-down Task 6 (with B); wire `HelpNote` into the filters panel, the strip and the drawers.
5. **Final:** whole-branch review on Opus, rebuild `web/dist`, restart demo (18770) and live (18771), push `master`, then the clumsiest-user review with tests and a browser pass in both themes at 390, 768, 1280, 1440.
