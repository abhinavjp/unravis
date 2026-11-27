<script lang="ts">
  import { tick } from 'svelte';
  import { REMOVE_TEXT } from '../lib/copy/removeConnectionCopy';
  import { runRemoval } from '../lib/removeFlow';

  // The Remove button of a saved connection. One press asks first (inline, not a browser pop-up); only the answer Remove calls `remove`.
  // `remove` resolves true once the row is gone. On false or an error the row stays and the reason shows next to it.
  // The parts sit in the row's own flex line: the question and the error take the full width.
  let { label, locked = false, remove }: { label: string; locked?: boolean; remove: () => Promise<boolean> } = $props();
  let phase = $state<'idle' | 'ask' | 'busy' | 'failed'>('idle');
  let keepButton = $state<HTMLButtonElement>();
  let askButton = $state<HTMLButtonElement>();

  // The question takes the focus on the safe answer, and Keep or Esc hands it back to the Remove button.
  async function ask() { phase = 'ask'; await tick(); keepButton?.focus(); }
  async function keep() { phase = 'idle'; await tick(); askButton?.focus(); }
  async function confirm() {
    if (phase !== 'ask') return;
    phase = 'busy';
    const result = await runRemoval(remove);
    if (result === 'removed') return; // the row goes away with the list, and this button with it
    phase = 'failed';
    await tick();
    askButton?.focus();
  }
  function onKey(event: KeyboardEvent) {
    if (event.key !== 'Escape' || phase !== 'ask') return;
    event.preventDefault(); event.stopPropagation(); void keep();
  }
</script>

{#if phase === 'ask' || phase === 'busy'}
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div class="remove-confirm" role="group" aria-label="{REMOVE_TEXT.button}: {label}" onkeydown={onKey}>
    {#if phase === 'busy'}<p role="status">{REMOVE_TEXT.busy}</p>{:else}<p>{REMOVE_TEXT.ask}</p>{/if}
    <div class="remove-row">
      <button type="button" class="secondary-button" disabled={phase === 'busy'} onclick={() => void confirm()}>{REMOVE_TEXT.yes}</button>
      <button type="button" class="secondary-button" bind:this={keepButton} disabled={phase === 'busy'} onclick={() => void keep()}>{REMOVE_TEXT.keep}</button>
    </div>
  </div>
{:else}
  <button type="button" class="quiet-button" bind:this={askButton} disabled={locked} aria-label="{REMOVE_TEXT.button}: {label}" onclick={() => void ask()}>{REMOVE_TEXT.button}</button>
  {#if phase === 'failed'}<div class="inline-error remove-error" role="alert">{REMOVE_TEXT.failed}</div>{/if}
{/if}

<style>
  .remove-confirm { display: grid; flex: 1 1 100%; gap: 8px; min-width: 0; margin: 2px 0 0; padding: 10px 12px; border: 1px solid var(--control-line); border-radius: var(--radius-sm); background: var(--surface-2); }
  .remove-confirm p { margin: 0; font-size: 13px; }
  .remove-row { display: flex; flex-wrap: wrap; gap: 8px; }
  .remove-error { flex: 1 1 100%; margin: 0; }
</style>
