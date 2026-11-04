<script lang="ts">
  import type { Snippet } from 'svelte';
  import { SOURCES_TEXT, chipText, isChipOn, sourcesSummary, type SourceChip, type SourceSelection } from '../lib/tracing/sources';

  // The "Sources traced" row of the timeline (the chip row of the wireframe). Every source is a button that is on or off; with all of them
  // off nothing is traced and the screen says so. A saved audit database that is not connected is listed as such, with a way to connect it.
  // The row only reports clicks: the screen decides what they mean and keeps the choice.
  let { chips, saved, selection, disabled = false, onToggle, onAll, onNone, onConnect, actions }: {
    chips: SourceChip[];
    saved: string[];
    selection: SourceSelection;
    disabled?: boolean;
    onToggle: (id: string) => void;
    onAll: () => void;
    onNone: () => void;
    onConnect: () => void;
    /** Buttons for the right end of the heading row (Add evidence, Refresh timeline), drawn by the screen. */
    actions?: Snippet;
  } = $props();

  const summary = $derived(sourcesSummary(selection, chips));
  const allOn = $derived(chips.length > 0 && chips.every((chip) => isChipOn(selection, chip.id)));
  const noneOn = $derived(chips.every((chip) => !isChipOn(selection, chip.id)));
</script>

<section class="trace-sources" aria-label={SOURCES_TEXT.label} data-testid="trace-sources">
  <div class="ts-head">
    <h3>{SOURCES_TEXT.label}</h3>
    {#if chips.length > 0}
      <button type="button" class="chip-clear" onclick={onAll} disabled={disabled || allOn} data-testid="sources-all">{SOURCES_TEXT.selectAll}</button>
      <button type="button" class="chip-clear" onclick={onNone} disabled={disabled || noneOn} data-testid="sources-none">{SOURCES_TEXT.clearAll}</button>
    {/if}
    {#if actions}<span class="ts-actions">{@render actions()}</span>{/if}
  </div>
  {#if chips.length === 0 && saved.length === 0}
    <p class="ts-note" data-testid="sources-empty">{SOURCES_TEXT.noSources}</p>
  {:else}
    <div class="ts-chips" role="group" aria-label={SOURCES_TEXT.label}>
      {#each chips as chip (chip.id)}
        <button type="button" class="chip" data-source-id={chip.id} aria-pressed={isChipOn(selection, chip.id)} {disabled} onclick={() => onToggle(chip.id)}>{chipText(chip)}</button>
      {/each}
      {#each saved as name (name)}
        <span class="ts-saved" data-testid="source-saved">
          <span class="ts-saved-name">{SOURCES_TEXT.kindAudit} · {name}</span>
          <span class="ts-saved-state">{SOURCES_TEXT.savedNotConnected}</span>
          <button type="button" class="secondary-button ts-connect" title={SOURCES_TEXT.connectTitle} {disabled} onclick={onConnect}>{SOURCES_TEXT.connect}</button>
        </span>
      {/each}
    </div>
    {#if chips.length > 0}<p class="ts-note" role="status" data-testid="sources-summary">{summary}</p>{/if}
  {/if}
</section>

<style>
  .trace-sources { display: grid; gap: var(--space-2); min-width: 0; padding: var(--space-3) var(--space-4); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .ts-head { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .ts-head h3 { margin: 0 var(--space-2) 0 0; font-size: var(--fs-3); font-weight: 600; }
  .ts-actions { display: inline-flex; flex-wrap: wrap; gap: var(--space-2); margin-left: auto; }
  .ts-head .chip-clear:disabled { opacity: 0.45; text-decoration: none; }
  .ts-chips { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); min-width: 0; }
  .ts-chips .chip { max-width: 100%; white-space: normal; overflow-wrap: anywhere; text-align: left; }
  .ts-chips .chip[aria-pressed='false'] { opacity: 0.7; }
  .ts-chips .chip:disabled { cursor: default; }
  .ts-chips .chip:focus-visible, .chip-clear:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .ts-saved { display: inline-flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); padding: 2px var(--space-2); border: var(--hairline-width) dashed var(--line); border-radius: var(--radius-sm); color: var(--text-2); font-size: var(--fs-2); }
  .ts-saved-name { color: var(--text); overflow-wrap: anywhere; }
  .ts-saved-state { font-style: italic; }
  .ts-connect { min-height: 26px; padding: 2px var(--space-3); font-size: var(--fs-2); }
  .ts-note { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
  /* Touch targets on a phone, a tablet and any touch screen. */
  @media (max-width: 1024px), (pointer: coarse) {
    .ts-chips .chip, .ts-connect, .ts-head .chip-clear { min-height: var(--space-10); }
  }
</style>
