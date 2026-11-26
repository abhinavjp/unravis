<script lang="ts">
  import { onMount } from 'svelte';
  import { codeModeStore } from '../lib/codeModeStore.svelte';
  import { openCodeMode } from '../lib/codeModeUi.svelte';
  import CodeIcon from './CodeIcon.svelte';

  // The header badge: shown while code mode is on (or its settings file is damaged), hidden when it is off. It starts the shared
  // settings reading (focus, every 30 seconds, after every save) and draws the store's answer. A click opens the popup on its Result step.
  // The countdown is only a display; the server's gate is what lets the command line run.
  onMount(() => {
    codeModeStore.start();
    return () => codeModeStore.stop();
  });

  const badge = $derived(codeModeStore.badge);
</script>

{#if badge}
  <button type="button" class="code-badge" class:warn={badge.tone === 'warn'} class:danger={badge.tone === 'danger'} data-testid="code-badge" aria-haspopup="dialog" aria-label={badge.aria} onclick={(event) => openCodeMode(event.currentTarget)}>
    <CodeIcon name={badge.icon} />
    <span class="long" aria-hidden="true">{badge.full} · {#if badge.isTime}<span class="time">{badge.detail}</span>{:else if badge.tone === 'danger'}<strong>{badge.detail}</strong>{:else}{badge.detail}{/if}</span>
    <span class="short" class:time={badge.isTime} aria-hidden="true">{badge.short}</span>
  </button>
{/if}

<style>
  .code-badge { display: inline-flex; align-items: center; gap: 8px; min-height: 32px; padding: 0 12px; color: var(--text); background: color-mix(in srgb, var(--accent) 10%, var(--surface)); border: 1px solid var(--accent); border-radius: 999px; font-size: 13px; font-weight: 500; white-space: nowrap; cursor: pointer; transition: filter var(--dur) var(--ease); }
  .code-badge:hover { filter: brightness(1.12); }
  .code-badge :global(.code-icon) { color: var(--accent); }
  .code-badge.warn { background: color-mix(in srgb, var(--warn) 10%, var(--surface)); border-color: var(--warn); }
  .code-badge.warn :global(.code-icon) { color: var(--warn); }
  .code-badge.danger { background: color-mix(in srgb, var(--bad) 10%, var(--surface)); border-color: var(--bad); }
  .code-badge.danger :global(.code-icon) { color: var(--bad); }
  .time { font-family: var(--mono); font-size: 12.5px; }
  .short { display: none; }
  /* On a phone the badge keeps only its time or state; the full state stays in its accessible name. */
  @media (max-width: 720px) {
    .long { display: none; }
    .short { display: inline; }
    .code-badge { min-height: 36px; padding: 0 6px; gap: 4px; font-size: 12px; }
    .time { font-size: 12px; letter-spacing: -.02em; }
    /* The header row is full at 390px: the plain "On" and the time say enough, the icon gives way, and the words stay (No limit, Damaged), so no state is colour alone. */
    .code-badge :global(.code-icon) { display: none; }
    .code-badge.danger { padding: 0 4px; font-size: 11px; font-weight: 600; }
  }
</style>
