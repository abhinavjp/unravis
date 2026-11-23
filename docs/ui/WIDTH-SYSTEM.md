# Width system (23 Nov 2026)

Owner note: screens felt congested and centered; the case page was an 880 px column in a 1920 px window. Desktop wins over phone.
Findings, research and before/after pictures: project folder `ui-audit/WIDTH-FINDINGS.md`.

## Rules

| Kind of screen | Container | Token / where |
|---|---|---|
| Data pages: lists, charts, tables, timeline, case page, create guide | Fill the window up to 2000 px, then stay centered | `--page-max` in `web/src/app.css` (`.stage-inner`, `.scope-main`) |
| Create guide, add-files cards | Up to 1560 px; card grids `auto-fill` | `.wizard`, `.card-grid`, `.tiles` |
| Forms and one-question pages (key unlock, erase, untrusted, case key) | Narrow column, 880 px | `.case-home` base rule, `--form-max` |
| Prose | About 75 characters a line, by `max-width` in `ch` on the paragraph, never by narrowing the page | `--read-max` (78ch) |
| Installer (setup window) | One 1040 px column, content and footer buttons aligned | `web/setup/App.svelte` |
| Drawers, dialogs | Fixed widths by design | unchanged |

## Case page

At 1100 px and wider: a side column (title, facts, jump links, actions; sticky when the window is 760 px or more tall) and a wide column of blocks. From 1360 px the short blocks (Analysis, Timeline, Theories, Files, Case key, Erase) sit two across; long blocks (retries, story, health checks, sources) span the column. Below 1100 px it is one column, as before.

## Adding a screen

Put it inside `.stage-inner` and it gets the data-page width. Do not set a `max-width` in pixels on a page container; use `--page-max`, or `--form-max` for a form page, and put the readable measure on the paragraphs. Check it with the width survey (`tools/uiaudit/README.md`).
