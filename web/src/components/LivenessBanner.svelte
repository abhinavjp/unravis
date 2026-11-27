<script lang="ts">
  import { liveness } from '../lib/liveness.svelte';
  import { LIVENESS_TEXT, heardAtText } from '../lib/copy/livenessCopy';

  // The strip under the header when the Unravis app cannot be reached (it stopped) or refuses this page (it was started again, so the
  // page belongs to the run before). Amber, not red: the app is gone, it is not failing. Red stays for a failed action.
  // The leading space is part of the text: a space written at the start of an {#if} block is dropped by the compiler.
  const heard = $derived(heardAtText(liveness.lastHeardAt) ? ` ${LIVENESS_TEXT.lastHeard(heardAtText(liveness.lastHeardAt))}` : '');
  const reload = () => { try { location.reload(); } catch { /* nothing to reload in this window */ } };
</script>

{#if liveness.earlier}
  <div class="live-bar" role="alert" data-testid="earlier-banner">
    <svg class="live-ic" viewBox="0 0 16 16" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M13.2 8a5.2 5.2 0 1 1-1.6-3.8" /><path d="M13.4 2.4v3.2h-3.2" /></svg>
    <div class="live-msg">
      <strong>{LIVENESS_TEXT.earlierTitle}</strong>
      <span>{LIVENESS_TEXT.earlierBody} <b>{LIVENESS_TEXT.earlierStep}</b>{LIVENESS_TEXT.earlierStepRest}</span>
    </div>
  </div>
{:else if liveness.stopped}
  <div class="live-bar" role="alert" data-testid="stopped-banner">
    <svg class="live-ic" viewBox="0 0 16 16" width="18" height="18" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="8" cy="8" r="6.4" /><rect x="5.6" y="5.6" width="4.8" height="4.8" rx=".6" /></svg>
    <div class="live-msg">
      <strong>{LIVENESS_TEXT.stoppedTitle}</strong>
      <span>{LIVENESS_TEXT.stoppedBody}{heard} {LIVENESS_TEXT.saved}</span>
      <span><b>{LIVENESS_TEXT.stoppedStep}</b>{LIVENESS_TEXT.stoppedStepRest}</span>
    </div>
    <button type="button" class="primary-button live-reload" data-testid="stopped-reload" onclick={reload}>{LIVENESS_TEXT.reload}</button>
  </div>
{/if}

<style>
  .live-bar { box-sizing: border-box; display: flex; flex-wrap: wrap; align-items: center; gap: 8px 14px; padding: 10px 24px; border-bottom: 1px solid var(--warn); background: color-mix(in srgb, var(--warn) 10%, var(--surface)); color: var(--text); font-size: 13px; }
  .live-ic { flex: none; color: var(--warn); align-self: flex-start; margin-top: 1px; }
  .live-msg { flex: 1 1 380px; display: grid; gap: 2px; min-width: 0; }
  .live-msg strong { font-weight: 600; font-size: 14px; }
  .live-msg span { color: var(--text-2); max-width: 78ch; }
  .live-msg span b { color: var(--text); font-weight: 600; }
  .live-reload { white-space: nowrap; }
  @media (max-width: 720px) {
    .live-bar { padding: 10px 16px; }
    .live-msg { flex-basis: calc(100% - 34px); }
    .live-reload { min-height: 40px; flex: 1 1 100%; }
  }
</style>
