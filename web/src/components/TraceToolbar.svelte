<script lang="ts">
 import type { TracingLaneKey } from '../lib/tracing/model';
 import { scrubPillText, TOOLBAR_TEXT, type LaneVisibility } from '../lib/tracing/toolbar';

 let { visible, onToggle, scrub }: { visible: LaneVisibility; onToggle: (key: TracingLaneKey) => void; scrub: boolean | null } = $props();
 let hintOpen = $state(false);
 // The pill always states the real export setting; the live region repeats it so a change is announced.
 const pillText = $derived(scrubPillText(scrub));
</script>

<div class="trace-toolbar" role="toolbar" aria-label={TOOLBAR_TEXT.ariaLabel}>
 <div class="tt-lanes" role="group" aria-label={TOOLBAR_TEXT.lanesLabel}>
  <button type="button" class="tt-lane" aria-pressed={visible.audit} onclick={() => onToggle('audit' as TracingLaneKey)}>
   <span class="tt-swatch" style="background: var(--lane-audit)" aria-hidden="true"></span>{TOOLBAR_TEXT.audit}
  </button>
  <button type="button" class="tt-lane" aria-pressed={visible.sql} onclick={() => onToggle('sql' as TracingLaneKey)}>
   <span class="tt-swatch" style="background: var(--lane-sql)" aria-hidden="true"></span>{TOOLBAR_TEXT.sql}
  </button>
  <button type="button" class="tt-lane" aria-pressed={visible.iis} onclick={() => onToggle('iis' as TracingLaneKey)}>
   <span class="tt-swatch" style="background: var(--lane-iis)" aria-hidden="true"></span>{TOOLBAR_TEXT.iis}
  </button>
 </div>
 <button type="button" class="pill" class:pill-off={scrub === false} aria-expanded={hintOpen} aria-label={pillText} onclick={() => { hintOpen = !hintOpen; }}>
  {pillText}
 </button>
 <span class="sr-only" role="status" aria-live="polite">{pillText}</span>
 {#if hintOpen}<p class="tt-hint">{TOOLBAR_TEXT.scrubHint}</p>{/if}
</div>

<style>
 .trace-toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; min-width: 0; }
 .tt-lanes { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
 .tt-lane {
  display: inline-flex; align-items: center; gap: 6px; min-height: 24px; padding: 2px 10px;
  color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm);
  font: inherit; cursor: pointer;
 }
 .tt-lane[aria-pressed='false'] { opacity: 0.55; }
 .tt-swatch { flex: none; width: 10px; height: 10px; border-radius: 50%; }
 .tt-lane:focus-visible, .pill:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
 .pill {
  min-height: 24px; padding: 2px 12px; color: var(--text); background: var(--surface-2);
  border: 1px solid var(--line); border-radius: 999px; font: inherit; cursor: pointer;
 }
 /* Scrub off is still neutral: a plain outline, never an alarm colour. */
 .pill-off { outline: 1px solid var(--line); outline-offset: 2px; }
 .tt-hint { flex-basis: 100%; margin: 2px 0 0; color: var(--text-2); }
 .sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; border: 0; }
</style>
