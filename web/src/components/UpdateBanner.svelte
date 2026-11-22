<script lang="ts">
  import { tick } from 'svelte';
  import { dismissUpdate, dismissUpdateResult, installing, startUpdateDownload, startUpdateInstall, updateBarVisible, updates } from '../lib/updateState.svelte';
  import { downloadPercent, installAnnouncement, installNote, isOwnReleaseUrl, updateCheckNote } from '../lib/updateCheck';

  // The bar under the header when a newer, signed version exists. Nothing downloads or installs until the person presses a button.
  const reply = $derived(updates.reply);
  const show = $derived(updateBarVisible(reply, updates.dismissed));
  const step = $derived(reply?.install);
  const stage = $derived(step?.state ?? 'idle');
  const percent = $derived(step ? downloadPercent(step.done, step.total) : 0);
  const text = $derived(reply ? (stage === 'idle' ? updateCheckNote(reply) : installNote(reply)) : '');
  const announcement = $derived(reply ? installAnnouncement(reply) : '');
  let bar: HTMLDivElement | undefined = $state();

  // A pressed button disappears with its state; keep the keyboard position on the bar instead of dropping it to the page.
  async function press(action: () => Promise<void>) {
    await action();
    await tick();
    bar?.focus();
  }
</script>

{#if reply?.result}
  <div class="notice update-bar update-result" role="status" tabindex="-1" data-testid="update-result" data-outcome={reply.result.outcome}>
    <span class="update-bar-text" data-testid="update-result-text">{reply.result.message}</span>
    <button type="button" class="quiet-button" data-testid="update-result-dismiss" onclick={() => dismissUpdateResult()}>OK</button>
  </div>
{/if}

{#if show && reply}
  <div class="notice update-bar" role="region" aria-label="Unravis update" bind:this={bar} tabindex="-1" data-testid="update-bar" data-state={stage}>
    <span class="sr-only" role="status" data-testid="update-bar-announce">{announcement}</span>
    <span class="update-bar-text" data-testid="update-bar-text">{text}</span>
    {#if stage === 'downloading'}
      <progress class="update-bar-progress" max="100" value={percent} aria-label="Download progress" data-testid="update-bar-progress"></progress>
    {/if}
    {#if stage === 'idle' && reply.canInstall}
      <button type="button" class="primary-button update-bar-action" data-testid="update-bar-download" disabled={installing.busy} onclick={() => press(startUpdateDownload)}>Update now</button>
    {:else if stage === 'ready'}
      <button type="button" class="primary-button update-bar-action" data-testid="update-bar-install" disabled={installing.busy} onclick={() => press(startUpdateInstall)}>Apply now</button>
    {:else if stage === 'launched'}
      <button type="button" class="primary-button update-bar-action" data-testid="update-bar-reopen" disabled={installing.busy} onclick={() => press(startUpdateInstall)}>Apply now</button>
    {:else if stage === 'failed'}
      <button type="button" class="primary-button update-bar-action" data-testid="update-bar-retry" disabled={installing.busy} onclick={() => press(startUpdateDownload)}>Try again</button>
    {/if}
    {#if stage === 'idle' && reply.url && isOwnReleaseUrl(reply.url)}<a class="update-bar-link" data-testid="update-bar-link" href={reply.url} target="_blank" rel="noopener noreferrer">Open the release page <span class="sr-only">(opens in a new tab)</span></a>{/if}
    {#if installing.error}<span class="update-bar-error" role="alert" data-testid="update-bar-error">{installing.error}</span>{/if}
    {#if stage === 'idle' || stage === 'failed' || stage === 'launched' || stage === 'ready'}
      <button type="button" class="quiet-button" data-testid="update-bar-dismiss" aria-label={`${stage === 'ready' ? 'Later' : 'Not now'}. Hide the update bar for version ${reply.latest}`} onclick={() => dismissUpdate(reply.latest)}>{stage === 'ready' ? 'Later' : 'Not now'}</button>
    {/if}
  </div>
{/if}

<style>
  .update-bar { box-sizing: border-box; min-height: 46px; display: flex; flex-wrap: wrap; align-items: center; gap: 6px 14px; margin: 0; border-radius: 0; border-width: 0 0 1px; row-gap: 0; border-bottom: 1px solid var(--line); background: color-mix(in srgb, var(--accent) 8%, var(--surface)); color: var(--text); padding: 8px 24px; }
  .update-bar:focus { outline: none; }
  .update-bar:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
  .update-bar-text { font-weight: 500; }
  /* 24px tap target (BL-475): the hit area grows in a pseudo-element, so the link, the bar and the focus ring stay exactly as they were. */
  .update-bar-link { position: relative; font-size: 13px; }
  .update-bar-link::after { content: ''; position: absolute; inset: -3px 0; }
  .update-bar-error { font-size: 13px; color: var(--bad); }
  .update-bar-progress { width: 160px; height: 8px; accent-color: var(--accent); }
  .update-bar-action { padding: 3px 12px; font-size: 13px; margin: 0; }
  @media (max-width: 720px) { .update-bar-action { margin: 4px 0; } }
  /* A failed update is bad news, so it is not drawn in the green of good news (CASES-08). */
  .update-bar[data-state='failed'] { background: color-mix(in srgb, var(--bad) 10%, var(--surface)); border-bottom-color: var(--bad); }
  /* Not now sits next to the main button, not 600 px away at the edge. */
  .update-bar .quiet-button { margin-left: 0; }
  .sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; border: 0; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
  @media (max-width: 720px) { .update-bar { padding: 8px 16px; } }
</style>
