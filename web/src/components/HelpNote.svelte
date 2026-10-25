<script lang="ts">
  import { help } from '../lib/copy/helpCopy';
  import { eli10 } from '../lib/eli10.svelte';

  // One plain line that is always visible, and a "?" button that opens an "In simple words" block with an everyday comparison.
  // The block stays in the page (hidden) so aria-controls always points at something. When "Explain simply" is on in the header,
  // every block is open and the button is disabled, because there is nothing left to toggle.
  let { id }: { id: string } = $props();
  const entry = $derived(help[id]);
  const uid = $props.id();
  const blockId = 'help-simple-' + uid;
  const labelId = 'help-label-' + uid;
  let open = $state(false);
  const shown = $derived(eli10.on || open);
</script>

{#if entry}
  <div class="help-note">
    <p class="help-plain">{entry.plain}
      <button type="button" class="help-toggle" aria-expanded={shown} aria-controls={blockId} aria-label="Explain in simple words" disabled={eli10.on} onclick={() => (open = !open)}>?</button></p>
    <div class="help-simple" id={blockId} role="group" aria-labelledby={labelId} hidden={!shown}>
      <p class="help-simple-label" id={labelId}>In simple words</p>
      <p class="help-simple-text">{entry.simple}</p>
    </div>
  </div>
{/if}

<style>
  .help-note { min-width: 0; margin: 0; }
  .help-plain { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.45; overflow-wrap: anywhere; }
  .help-toggle { position: relative; display: inline-grid; place-items: center; width: 16px; height: 16px; margin-inline-start: var(--space-1); padding: 0; vertical-align: middle; color: var(--text-2); background: transparent; border: var(--hairline-width) solid var(--control-line); border-radius: 50%; font: inherit; font-size: 10px; font-weight: 600; line-height: 1; cursor: pointer; }
  /* The visible circle stays small; the tap target reaches 24px. */
  .help-toggle::after { content: ""; position: absolute; inset: -4px; }
  .help-toggle:hover, .help-toggle[aria-expanded='true'] { color: var(--text); background: var(--surface-2); border-color: var(--accent); }
  .help-toggle:disabled { cursor: default; }
  .help-toggle:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .help-simple { margin-top: var(--space-2); padding: var(--space-2) var(--space-3); background: var(--surface-2); border-inline-start: var(--hairline-width) solid var(--accent); }
  .help-simple[hidden] { display: none; }
  .help-simple-label { margin: 0 0 var(--space-1); color: var(--text); font-size: var(--fs-2); font-weight: 600; }
  .help-simple-text { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.45; overflow-wrap: anywhere; }
</style>
