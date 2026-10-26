<script lang="ts">
  import { restore } from '../lib/appState.svelte';
  import { CASE_RESTORE_TEXT, signInText } from '../lib/copy/caseRestoreCopy';

  // What a case that used a MongoDB source says while it waits for its database: it names the database it used and offers the sign-in, in
  // place of the first-run connect step. `compact` is the same words as a strip above the analysis, for when other sources are already there.
  let { compact = false }: { compact?: boolean } = $props();

  const phase = $derived(restore.phase);
  const working = $derived(phase === 'working');
  const gone = $derived(phase === 'missing');
  const unreachable = $derived(phase === 'unreachable');
  const heading = $derived(gone ? CASE_RESTORE_TEXT.missingHeading : unreachable ? CASE_RESTORE_TEXT.unreachableHeading : phase === 'failed' ? CASE_RESTORE_TEXT.failedHeading : CASE_RESTORE_TEXT.heading);
  const body = $derived(gone ? CASE_RESTORE_TEXT.missing : unreachable ? CASE_RESTORE_TEXT.unreachable : phase === 'failed' ? CASE_RESTORE_TEXT.failed : signInText(restore.databases));
</script>

{#if compact}
  <div class="banner case-reconnect" role="status">
    {#if working}<span>{CASE_RESTORE_TEXT.working}</span>{:else}<strong>{heading}</strong><span>{body}</span>{/if}
    <span class="empty-actions">
      {#if working}
        <!-- only Cancel: the buttons below belong to a restore that is waiting for the person -->
      {:else if gone}
        <button type="button" class="primary-button" onclick={() => void restore.another()}>{CASE_RESTORE_TEXT.newButton}</button>
      {:else if unreachable}
        <button type="button" class="primary-button" onclick={() => void restore.retry()}>{CASE_RESTORE_TEXT.tryAgainButton}</button>
        <button type="button" class="quiet-button" onclick={() => void restore.another()}>{CASE_RESTORE_TEXT.anotherButton}</button>
      {:else}
        <button type="button" class="primary-button" onclick={() => void restore.reconnect()}>{CASE_RESTORE_TEXT.signInButton}</button>
        <button type="button" class="quiet-button" onclick={() => void restore.another()}>{CASE_RESTORE_TEXT.anotherButton}</button>
      {/if}
      <button type="button" class="quiet-button" onclick={restore.cancel}>{CASE_RESTORE_TEXT.cancelButton}</button>
    </span>
  </div>
{:else}
  <div class="welcome-state case-reconnect-state">
    <h3>{heading}</h3>
    <p>{body}</p>
    <div class="empty-actions">
      {#if gone}
        <button type="button" class="primary-button" onclick={() => void restore.another()}>{CASE_RESTORE_TEXT.newButton}</button>
      {:else if unreachable}
        <button type="button" class="primary-button" onclick={() => void restore.retry()}>{CASE_RESTORE_TEXT.tryAgainButton}</button>
        <button type="button" class="secondary-button" onclick={() => void restore.another()}>{CASE_RESTORE_TEXT.anotherButton}</button>
      {:else}
        <button type="button" class="primary-button" onclick={() => void restore.reconnect()}>{CASE_RESTORE_TEXT.signInButton}</button>
        <button type="button" class="secondary-button" onclick={() => void restore.another()}>{CASE_RESTORE_TEXT.anotherButton}</button>
      {/if}
      <button type="button" class="quiet-button" onclick={restore.cancel}>{CASE_RESTORE_TEXT.cancelButton}</button>
    </div>
  </div>
{/if}

<style>
  .case-reconnect { display: flex; flex-wrap: wrap; align-items: center; gap: 0.5rem 1rem; }
  .case-reconnect .empty-actions { display: inline-flex; flex-wrap: wrap; gap: 0.5rem; }
</style>
