# Unravis brand and design tokens

Reusable reference for every Unravis screen: the web app, the setup installer, docs and any future surface.
Source of truth in code: `web/src/brand/tokens.css` (all tokens and the IBM Plex `@font-face` blocks; `web/src/app.css` imports it, components follow). If this file and the CSS differ, the CSS wins; update this file.

## Character

An instrument for engineers investigating production incidents. Dark by default, near-black with a single teal-mint accent. Dense, calm, exact. Colour carries data, not decoration. Solid accent means one thing: the primary action.

## Fonts

Bundled locally (works offline), SIL OFL licence, files in `web/src/fonts/` with `OFL.txt`.

| Role | Stack |
|---|---|
| Text | `"IBM Plex Sans", "Segoe UI Variable Text", system-ui, sans-serif` at 400 / 500 / 600 |
| Numbers, paths, code | `"IBM Plex Mono", ui-monospace, "Cascadia Code", Consolas, monospace` at 400 / 500 |

Body 13 to 14 px, line height 1.45, tabular numbers on (`font-feature-settings: "tnum" 1`). Type scale: 11 / 12 / 13 / 14 / 16 / 20 px. Headings weight 600, letter spacing -0.01em. No uppercase letter-spaced micro-labels.

## Colour: dark (default)

| Token | Value | Use |
|---|---|---|
| `--bg` | `#050807` | page |
| `--surface` | `#0a0f0d` | panels |
| `--surface-2` | `#101714` | raised, hover, brand mark |
| `--line` | `#1a2621` | borders |
| `--line-soft` | `#131c18` | quiet dividers |
| `--control-line` | `#6b8478` | borders of controls (meets contrast) |
| `--text` | `#eef3f1` | main text |
| `--text-2` | `#93a39b` | secondary text |
| `--accent` | `#3fd6b0` | primary action, selection, links |
| `--accent-ink` | `#03130e` | text on accent |
| `--warn` | `#e3b341` | amber: warnings, 4xx |
| `--bad` | `#ff7b72` | rose: errors, 5xx |
| `--pending` | `#8fa39a` | slate: not finished, shown hatched |
| `--focus` | `#7ff0d0` | focus ring |
| `--scrim` | `rgb(2 5 9 / 62%)` | dialog backdrop |

Heat ramp: `#1d3a52`, `#2f7f86`, `#c9962f`, `#d0604f`.
Series (charts, in order): `#6ea8ff`, `#c58bd6`, `#e3a23c`, `#f07a88`, `#b9bd52`, `#54c2d9`, `#c79a7a`, `#9fb0c6`.

## Colour: light

| Token | Value |
|---|---|
| `--bg` | `#f5f5f3` |
| `--surface` | `#ffffff` |
| `--surface-2` | `#eeeeeb` |
| `--line` | `#d9d9d4` |
| `--control-line` | `#74808f` |
| `--text` | `#16191d` |
| `--text-2` | `#4a525c` |
| `--accent` | `#0b6b57` |
| `--accent-ink` | `#ffffff` |
| `--warn` | `#96600a` |
| `--bad` | `#b02a3d` |
| `--pending` | `#5b6878` |
| `--focus` | `#0b6b57` |

Theming: follows `prefers-color-scheme`; forced with `data-theme="light"` or `data-theme="dark"` on `<html>`. Every screen must work in both.

## Shape, space, motion

- Radius 2 px (`--radius-sm`) on controls and containers. Nothing pill-shaped, no soft blobs.
- Space scale: 4 / 8 / 12 / 16 / 20 / 24 / 32 / 40 px.
- Header height 56 px. Minimum page width 320 px.
- Motion: 180 ms (`--dur`) with `cubic-bezier(.2, .7, .2, 1)` (`--ease`). Colour and background changes only; honour `prefers-reduced-motion`.
- Focus: 2 px solid `--focus`, 2 px offset, always visible.
- Pending or incomplete is drawn with the diagonal `--hatch` pattern, never green.

## Logo (brand mark)

A 26 px square with a 1 px `--line` border, `--surface-2` fill and 5 px padding, holding three bottom-aligned bars in `--accent`, 2 px apart: heights 7 px at 55 % opacity, 14 px at full, 10 px at 80 %. Wordmark "Unravis" beside it at 16 px, weight 600, 10 px gap.

```html
<span class="brand-mark"><span></span><span></span><span></span></span>
```

## Buttons and labels (one rule everywhere)

**Outline means press me. Sticker means information.**

| Kind | Look |
|---|---|
| Primary | `--accent` fill, `--accent-ink` text, 1 px accent border, 13 px weight 600, padding 6 x 12. One per screen at most. |
| Secondary | transparent, 1 px `--line` border, `--text`; hover raises border to `--text-2` and fills `--surface-2`. |
| Quiet | text only, `--text-2`, hover fills `--surface-2`. |
| Disabled | `--surface-2` fill, `--control-line` border, `--text-2` text, not-allowed cursor. |
| Sticker (status label, chip) | soft fill, no outline, not clickable, never styled like a button. |

Selected states are not saturated filled pills. A selected tab or nav item gets a 2 px accent underline; a selected toggle gets an accent border.

## Status words (fixed vocabulary)

- Readiness: Ready, Partly ready, Cannot test yet, Failed.
- Connection tests only: Works, Failed, Not tested.
- Running state (recorder) only: Running, Running, late.
- Suspect scale: red most likely, orange likely, green ruled out, grey not sure or could not be tested (grey is never a pass).
- Colour on data: 4xx amber, 5xx rose, pending slate hatched, healthy neutral.

## Voice

Plain sentences. Say what happened, then what to do. No jargon without a word of explanation. "In a nutshell" is the label for the short plain-language box. Unravis only reads: anything that changes a server is shown as a script for the owner to run.

## Installer notes (derived from the above)

- Same tokens, same fonts (embedded in the executable, no network needed), dark by default, follows the system theme.
- Layout: fixed-width window, 56 px header with the brand mark and wordmark, a left rail of steps with a 2 px accent marker on the current one, one primary button at the bottom right.
- A step list shows each action with a status sticker (Waiting, Running, Done, Failed) and a mono line of detail; a failed step shows the fix in plain words and a Retry outline button.
- Progress is a thin accent bar on `--line`; pending work is hatched.
- Admin approval is explained before the Windows prompt appears, and the screen says what will and will not be changed.
- Never green for "waiting"; never an accent fill for anything except the one primary action.
