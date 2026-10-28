# Lane L4 report

Branch: `fix/scn1-L4-analysis-screens-chrome` (from master). 11 packet commits plus this report. Web-only lane, no Go gate.

## Packet status

| Packet | Commit | Status |
|---|---|---|
| L4-01 copy small | 3d7ab2b | done |
| L4-02 audit ID gate | 9c192a9 | done |
| L4-03 target line and notes | bb713a5 | done |
| L4-04 pulse headline tiles | d323b66 | done |
| L4-05 phases tab | 80c0473 | done |
| L4-06 zoom notes | 8daef22 | done |
| L4-07 drawer table fit | dd4f730 | done |
| L4-08 header wrap | 66ca93b | done |
| L4-09 tap targets | 30b343f | done |
| L4-10 update check | e1ee1c8 | done |
| L4-11 help security tests | see git log | done, tests only |

## Gate (in `web/`)

- `npm test`: 1861 tests, 1861 pass, 0 fail (1848 after L4-09; L4-10 added 6, L4-11 added 7).
- `npm run check`: 0 errors, 0 warnings.
- `npm run build`: ok. `web/dist` and `cmd/unravis-setup/ui` restored and cleaned; `git status` clean.

## Rulings

- Ruling: created `web/node_modules` in the worktree as a junction (`mklink /J`) to the main checkout's `web/node_modules` — the worktree had none and `npm ci` is forbidden — cost if wrong: none, the junction is untracked and removable.
- Ruling: L4-06 edited the existing `web/tests/zoomStrip.test.cjs` (outside the packet write scope) — it failed on the new `screenCopy` import, the live-region regex and the import-list pin that the packet's own change invalidates — cost if wrong: a pin in that file was loosened; revert and re-pin if the owner disagrees.
- Ruling: L4-09 test strips the media marker before the `width: \d` check — the packet's regex matched `(max-width: 720px)` in the marker itself — cost if wrong: none, the check still bans any other width or height value in the block.
- L4-05 updated two assertions in `tests/pure.test.cjs` that pinned the old Phases tab rule; L4-01 updated one expected string there too (scopeHint wording). Both follow directly from the packet's required text.

## Honest limits

- L4-03: the cap on the target line is applied through `appliedTarget` in `Charts.svelte`; the hero chart path is not separately covered by a test, only the pure helpers and source greps.
- L4-06: the tiny-drag note is pinned in `ZoomStrip.svelte`; a direct drag on the chart itself (not the strip) does not set it.
- L4-08: the header now wraps from 721px; overlap with the open drawer at those widths is not tested.
- All lane tests are source greps and pure-function tests; there is no DOM.

## Not done

Nothing left undone.

## Browser checks needed

- L4-07: open the request drawer, confirm the table fits with no horizontal scroll and the last column buttons stack.
- L4-08: at widths 721 to 1100 px, confirm the header wraps to a second row without clipping, and that the drawer does not overlap it.
- L4-09: emulate a touch device or a width of 720 px or less, confirm the listed controls are 44 px tall and the topbar height is unchanged.
- L4-10: load with `#update=available`, then edit the address to `#update=zzz`, confirm the Help panel preview disappears. Navigating to an address without `update=` must keep it.
- L4-03, L4-06: confirm the inline target and interval errors, and the tiny-drag and empty-zoom notes, read well on screen.
