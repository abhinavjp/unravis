# Unravis UI direction — quiet first, depth on demand

Mode: **Operate** (an engineer investigating a slowdown, often late, under pressure, in a dim room). Scanability and speed outrank ornament; the brand lives in precision.

## Job
Answer "is something wrong, where, and why" in the order the user needs it. Never show a control or number before the user has asked a question that needs it.

## Direction
- **Ground:** deep graphite-blue, dark by default (`prefers-color-scheme` honoured, with a full light theme). Neutrals tinted toward blue; no pure black or white.
- **One accent:** a single cool luminous hue for focus, selection and the active path. Status colours are separate and data-driven only: 4xx amber, 5xx rose, incomplete/pending slate (hatched, never green), healthy stays neutral. Colour never carries meaning alone.
- **Type:** system stack (offline, no hosted fonts). Thin large tabular numerals for headline figures, small tracked caps for labels, generous leading. Numbers always `tabular-nums`.
- **Form:** hairlines instead of boxes, space instead of cards, no nested cards, no side-stripe borders, no glow/neon edges, no glass blur, no gradient text. Radius small and consistent. Charts are the visual centrepiece; chrome recedes.
- **Motion:** 150–220 ms ease-out; one orchestrated reveal for depth changes (content slides/fades from the element that opened it); honour `prefers-reduced-motion`.

## Progressive depth (the structural thesis)
- **D0 Pulse (landing):** one calm screen. Quick time-range chips, source selector, kind control (Sync / Async / Hangfire / HTTP), five headline figures (Requests, Completion, Observed P95, 4xx, 5xx) and one hero timeline. No filter panel, no tables.
- **D1 Where:** tap a figure or "Where is it?" → ranked operations (controller + action + kind) each with a sparkline and the one number that matters; "Compare to earlier" reveals deltas. Breakdown dimensions appear as a quiet "Split by…" menu.
- **D2 Evidence:** selecting an operation opens an inspector (right drawer on wide screens, full view on narrow). Tabs reveal only what exists: Timeline (P50–P99), Phases, Failures, Incomplete, Slowest, Breakdown.
- **D3 Record:** a request opens the full record detail and payload.
- **Filters:** active filters are removable chips under the header. "More filters" is a collapsed, grouped panel (time, identity, status, size/timing ranges, text); it opens only on request and shows a count when anything inside is active.
- **Wayfinding:** a breadcrumb (Pulse › Operation › Request) is always present beyond D0; Esc/back steps up one level; state survives going back.
- **Connections** live behind a source popover, not a permanent sidebar; first run shows only the connect step.

## Must not change
All data, calculations, drill-down integrity (every figure still opens its underlying records), labels that carry semantics (e.g. "Observed API time (AuditCreateTime + ApiTime)", "HTTP (polling type unknown)"), existing tests, MCP and server code. This is markup/CSS/layout/interaction work in `web/` only.

## Quality bars
WCAG AA contrast in both themes; keyboard-reachable depth changes with visible focus; usable from 390 px to 1600 px; no layout shift when depth changes; empty/loading/error/partial states designed (never a blank panel); verify desktop and mobile once, fix in one batch, confirm once.

## Sequencing
Runs after Tasks 9–12 land (all panels exist), as Task 15, before the final review. Rename (Task 14) is independent.

## CRAP principles (binding on every screen)
- **Contrast:** one focal element per view, set apart by size, weight or the single accent; headline figures large and thin against small tracked labels; status colours only where status exists. Never two things shouting.
- **Repetition:** one token set (colour, spacing scale, radius, type scale, motion timings) and one component vocabulary: the same chip, tab, figure tile, table row, drawer and empty state everywhere. No per-panel one-offs.
- **Alignment:** a single grid and baseline; left-align text and labels, right-align numerals on the decimal; every element sits on a shared edge with its neighbours; no centred-by-habit blocks.
- **Proximity:** related items grouped tightly (a figure with its label and delta; a filter group with its chips), unrelated groups separated by clearly larger space; whitespace does the dividing instead of boxes and rules.
