<script lang="ts">
  import { help } from '../lib/copy/helpCopy';
  import { eli10 } from '../lib/eli10.svelte';

  // A small "i" button for one filter field. Closed, it adds nothing but the button to a dense row. Opened, it shows the field's plain line and
  // the "In simple words" block from the same help table HelpNote reads. With "Explain simply" on in the header every block is open and the button is disabled.
  // `part` lets a row put the button and the block in different places: the row holds the open flag and binds it to both.
  // The block stays in the page (hidden) so aria-controls always points at something. Inside a hidden pane the whole pane is hidden, so neither part adds a tab stop.
  let { id, part = 'both', open = $bindable(false) }: { id: string; part?: 'both' | 'toggle' | 'block'; open?: boolean } = $props();
  const entry = $derived(help[id]);
  const blockId = $derived('field-help-' + id);
  const shown = $derived(eli10.on || open);
</script>

{#if entry}
  {#if part !== 'block'}
    <button type="button" class="field-help-toggle" aria-expanded={shown} aria-controls={blockId} aria-label="About this filter" disabled={eli10.on} onclick={() => (open = !open)}>i</button>
  {/if}
  {#if part !== 'toggle'}
    <div class="field-help-block" id={blockId} role="group" aria-label="About this filter" hidden={!shown}>
      <p class="field-help-plain">{entry.plain}</p>
      <p class="field-help-simple"><span class="field-help-label">In simple words</span> {entry.simple}</p>
    </div>
  {/if}
{/if}

<style>
  .field-help-toggle { position: relative; display: inline-grid; place-items: center; width: 16px; height: 16px; margin-inline-start: var(--space-1); padding: 0; flex: none; vertical-align: middle; color: var(--text-2); background: transparent; border: var(--hairline-width) solid var(--control-line); border-radius: 50%; font: inherit; font-size: 10px; font-weight: 600; font-style: italic; line-height: 1; cursor: pointer; }
  /* The visible circle stays small; the tap target reaches 24px. */
  .field-help-toggle::after { content: ""; position: absolute; inset: -4px; }
  .field-help-toggle:hover, .field-help-toggle[aria-expanded='true'] { color: var(--text); background: var(--surface-2); border-color: var(--accent); }
  .field-help-toggle:disabled { cursor: default; }
  .field-help-toggle:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .field-help-block { flex: 0 0 100%; min-width: 0; margin-top: var(--space-1); padding: var(--space-2) var(--space-3); background: var(--surface-2); border-inline-start: var(--hairline-width) solid var(--accent); font-weight: 400; }
  .field-help-block[hidden] { display: none; }
  .field-help-plain, .field-help-simple { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.45; overflow-wrap: anywhere; }
  .field-help-simple { margin-top: var(--space-1); }
  .field-help-label { color: var(--text); font-weight: 600; }
</style>
