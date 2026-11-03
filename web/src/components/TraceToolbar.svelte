<script lang="ts">
 import type { TracingLaneKey } from '../lib/tracing/model';
 import { LANE_ORDER, scrubPillText, TOOLBAR_TEXT, type LaneVisibility } from '../lib/tracing/toolbar';

 let { visible, onToggle, scrub }: { visible: LaneVisibility; onToggle: (key: TracingLaneKey) => void; scrub: boolean | null } = $props();
 let hintOpen = $state(false);
 // The pill always states the real export setting; the live region repeats it so a change is announced.
 const pillText = $derived(scrubPillText(scrub));
 // Same lane colours as the chart rows: the shared --lane-* variables, never a literal colour.
 const SWATCH: Record<TracingLaneKey, string> = {
  audit: 'background: var(--lane-audit)',
  sql: 'background: var(--lane-sql)',
  iis: 'background: var(--lane-iis)'
 };
</script>

<div class="trace-toolbar" role="toolbar" aria-label={TOOLBAR_TEXT.ariaLabel}>
 <div class="tt-lanes" role="group" aria-label={TOOLBAR_TEXT.lanesLabel}>
  {#each LANE_ORDER as key (key)}
   <button type="button" class="tt-lane" aria-pressed={visible[key]} onclick={() => onToggle(key)}>
    <span class="tt-swatch" style={SWATCH[key]} aria-hidden="true"></span>{TOOLBAR_TEXT[key]}
   </button>
  {/each}
 </div>
 <button type="button" class="pill" class:pill-off={scrub === false} aria-expanded={hintOpen} aria-label={pillText} onclick={() => { hintOpen = !hintOpen; }}>
  {pillText}
 </button>
 <span class="sr-only" role="status" aria-live="polite">{pillText}</span>
 {#if hintOpen}<p class="tt-hint">{TOOLBAR_TEXT.scrubHint}</p>{/if}
</div>

<style>
 .trace-toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); min-width: 0; }
 .tt-lanes { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
 .tt-lane {
  display: inline-flex; align-items: center; gap: var(--space-2); min-height: var(--space-6); padding: var(--space-1) var(--space-3);
  color: var(--text); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm);
  font: inherit; cursor: pointer;
 }
 .tt-lane[aria-pressed='false'] { opacity: 0.55; }
 .tt-swatch { flex: none; width: 10px; height: 10px; border-radius: 50%; }
 .tt-lane:focus-visible, .pill:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
 .pill {
  min-height: var(--space-6); padding: var(--space-1) var(--space-3); color: var(--text); background: var(--surface-2);
  border: var(--hairline-width) solid var(--line); border-radius: var(--space-8); font: inherit; cursor: pointer;
 }
 /* Scrub off is still neutral: a plain outline, never an alarm colour. */
 .pill-off { outline: var(--hairline-width) solid var(--line); outline-offset: 2px; }
 .tt-hint { flex-basis: 100%; margin: var(--space-1) 0 0; color: var(--text-2); }
 /* Touch targets on a phone, a tablet and any touch screen: 40 px high, never under 32. */
 @media (max-width: 768px), (pointer: coarse) {
  .tt-lane, .pill { min-height: var(--space-10); }
 }
 .sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; border: 0; }
</style>
