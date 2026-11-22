<script lang="ts">
  import { help } from '../lib/copy/helpCopy';
  import { eli10 } from '../lib/eli10.svelte';

  // One plain line that is always visible, and a "?" button that opens an "In simple words" block with an everyday comparison.
  // The block stays in the page (hidden) so aria-controls always points at something. When "Explain simply" is on in the header,
  // every block starts open, and the button still works: it folds that one block away and opens it again (CASES-23). Esc folds an open block and
  // gives focus back to the button (CASES-06).
  let { id }: { id: string } = $props();
  const entry = $derived(help[id]);
  const uid = $props.id();
  const blockId = 'help-simple-' + uid;
  const labelId = 'help-label-' + uid;
  let open = $state(false);
  // While "Explain simply" is on, `folded` is the person's own choice to close this one block.
  let folded = $state(false);
  $effect(() => { if (eli10.on) folded = false; });
  const shown = $derived(eli10.on ? !folded : open);
  let toggle = $state<HTMLButtonElement>();
  function flip() { if (eli10.on) folded = !folded; else open = !open; }
  function onKey(event: KeyboardEvent) {
    if (event.key !== 'Escape' || !shown) return;
    event.stopPropagation();
    if (eli10.on) folded = true; else open = false;
    toggle?.focus();
  }
</script>

{#if entry}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="help-note" onkeydown={onKey}>
    <p class="help-plain">{entry.plain}
      <button type="button" class="help-toggle" bind:this={toggle} aria-expanded={shown} aria-controls={blockId} aria-label="Explain in simple words" onclick={flip}>?</button></p>
    <div class="help-simple" id={blockId} role="group" aria-labelledby={labelId} hidden={!shown}>
      <p class="help-simple-label" id={labelId}>In simple words</p>
      <p class="help-simple-text">{entry.simple}</p>
    </div>
  </div>
{/if}

<style>
  .help-note { min-width: 0; margin: 0; }
  .help-plain { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.45; overflow-wrap: anywhere; }
  /* The button itself is the tap target (24px, 32px on touch or a narrow screen); the 16px circle is drawn inside it. Negative block margins keep the line height. */
  .help-toggle { position: relative; display: inline-grid; place-items: center; width: 24px; height: 24px; margin: -4px 0 -4px calc(var(--space-1) - 4px); padding: 0; vertical-align: middle; color: var(--text-2); background: transparent; border: 0; border-radius: 50%; font: inherit; font-size: 12px; font-weight: 600; line-height: 1; cursor: pointer; isolation: isolate; }
  /* The ring sits behind the "?": without z-index it painted its hover and open fill over the glyph (CASES-06). */
  .help-toggle::before { content: ""; position: absolute; z-index: -1; inset: 4px; border: var(--hairline-width) solid var(--control-line); border-radius: 50%; pointer-events: none; }
  .help-toggle:hover, .help-toggle[aria-expanded='true'] { color: var(--text); }
  .help-toggle:hover::before, .help-toggle[aria-expanded='true']::before { background: var(--surface-2); border-color: var(--accent); }
  @media (pointer: coarse), (max-width: 720px) { .help-toggle { width: 32px; height: 32px; margin-block: -8px; margin-inline-start: calc(var(--space-1) - 8px); } .help-toggle::before { inset: 8px; } }
  .help-toggle:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .help-simple { box-sizing: border-box; width: 100%; max-width: none; margin-top: var(--space-2); padding: var(--space-2) var(--space-3); background: var(--surface-2); border-inline-start: var(--hairline-width) solid var(--accent); }
  .help-simple[hidden] { display: none; }
  .help-simple-label { margin: 0 0 var(--space-1); color: var(--text); font-size: var(--fs-2); font-weight: 600; }
  .help-simple-text { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.45; overflow-wrap: anywhere; }
</style>
