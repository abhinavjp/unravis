<script lang="ts">
  import { help } from '../lib/copy/helpCopy';
  import { eli10 } from '../lib/eli10.svelte';

  // A small "i" button for one filter field. Closed, it adds nothing but the button to a dense row. Opened, it shows the field's plain line and
  // the "In simple words" block from the same help table HelpNote reads. With "Explain simply" on in the header every block is open and the button is disabled.
  // `part` lets a row put the button and the block in different places: the row holds the open flag and binds it to both.
  // The block stays in the page (hidden) so aria-controls always points at something. Inside a hidden pane the whole pane is hidden, so neither part adds a tab stop.
  let { id, part = 'both', open = $bindable(false), label = '' }: { id: string; part?: 'both' | 'toggle' | 'block'; open?: boolean; label?: string } = $props();
  const entry = $derived(help[id]);
  const blockId = $derived('field-help-' + id);
  const shown = $derived(eli10.on || open);
  // The button is named after the field it explains ("About Status"), so the six "i" buttons of one tab are not all called the same (ANALYSIS-06).
  // The row can pass `label`; without it the name is read from the field's own <label> (or its "-label" element) once the row is on screen.
  let found = $state('');
  let toggleButton = $state<HTMLButtonElement>();
  $effect(() => {
    if (label || !toggleButton) return;
    const root = toggleButton.getRootNode() as Document;
    const source = root.querySelector?.('label[for="' + id + '"]') ?? root.getElementById?.(id + '-label') ?? toggleButton.closest('fieldset')?.querySelector('legend') ?? root.getElementById?.(id)?.closest('label') ?? null;
    if (!source) return;
    // Only the label's own words: a muted aside such as "exact match" is a child element and is left out.
    const own = [...source.childNodes].filter((node) => node.nodeType === Node.TEXT_NODE).map((node) => node.textContent ?? '').join('').replace(/\s+/g, ' ').trim();
    found = own || (source.textContent ?? '').replace(/\s+/g, ' ').trim();
  });
  const name = $derived((label || found).replace(/\s*\(.*\)\s*$/, '').trim());
  const about = $derived(name ? 'About ' + name : 'About this filter');
</script>

{#if entry}
  {#if part !== 'block'}
    <button type="button" class="field-help-toggle" aria-expanded={shown} aria-controls={blockId} bind:this={toggleButton} aria-label={eli10.on ? about + ' (open: Explain simply is on)' : about} disabled={eli10.on} onclick={() => (open = !open)}>i</button>
  {/if}
  {#if part !== 'toggle'}
    <div class="field-help-block" id={blockId} role="group" aria-label={about} hidden={!shown}>
      <p class="field-help-plain">{entry.plain}</p>
      <p class="field-help-simple"><span class="field-help-label">In simple words</span> {entry.simple}</p>
    </div>
  {/if}
{/if}

<style>
  /* The button itself is the tap target (24px, 32px on touch or a narrow screen); the 16px circle is drawn inside it. Negative margins keep the row height. */
  .field-help-toggle { position: relative; display: inline-grid; place-items: center; width: 24px; height: 24px; margin: -4px 0 -4px calc(var(--space-1) - 4px); padding: 0; flex: none; vertical-align: middle; color: var(--text-2); background: transparent; border: 0; border-radius: 50%; font: inherit; font-size: 12px; font-weight: 600; font-style: italic; line-height: 1; cursor: pointer; isolation: isolate; }
  /* The ring sits behind the "i": without z-index it painted its hover and open fill over the glyph (ANALYSIS-05, CASES-06). */
  .field-help-toggle::before { content: ""; position: absolute; z-index: -1; inset: 4px; border: var(--hairline-width) solid var(--control-line); border-radius: 50%; pointer-events: none; }
  .field-help-toggle:hover, .field-help-toggle[aria-expanded='true'] { color: var(--text); }
  .field-help-toggle:hover::before, .field-help-toggle[aria-expanded='true']::before { background: var(--surface-2); border-color: var(--accent); }
  @media (pointer: coarse), (max-width: 720px) { .field-help-toggle { width: 32px; height: 32px; margin-block: -8px; margin-inline-start: calc(var(--space-1) - 8px); } .field-help-toggle::before { inset: 8px; } }
  /* After a "?" hint the two hit areas must not overlap (BL-474): the hint button is 24px and sits 4px into this one's space, so start 4px later. Wins over the touch rule above. */
  :global(.hint-wrap ~ .field-help-toggle) { margin-inline-start: var(--space-1); }
  /* Disabled while "Explain simply" holds every block open: still readable, the glyph stays (ANALYSIS-19). */
  .field-help-toggle:disabled { cursor: default; opacity: .8; }
  .field-help-toggle:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .field-help-block { flex: 0 0 100%; min-width: 0; margin-top: var(--space-1); padding: var(--space-2) var(--space-3); background: var(--surface-2); border-inline-start: var(--hairline-width) solid var(--accent); font-weight: 400; }
  .field-help-block[hidden] { display: none; }
  .field-help-plain, .field-help-simple { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.45; overflow-wrap: anywhere; }
  .field-help-simple { margin-top: var(--space-1); }
  .field-help-label { color: var(--text); font-weight: 600; }
</style>
