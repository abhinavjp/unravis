<script lang="ts">
  import { dismissUpdate, updateBarVisible, updates } from '../lib/updateState.svelte';
  import { isOwnReleaseUrl, updateCheckNote } from '../lib/updateCheck';

  // The bar under the header when a newer, signed version exists. It only tells; installing waits for a later step.
  const reply = $derived(updates.reply);
  const show = $derived(updateBarVisible(reply, updates.dismissed));
</script>

{#if show && reply}
  <div class="notice update-bar" role="status" data-testid="update-bar">
    <span class="update-bar-text">{updateCheckNote(reply)}</span>
    {#if reply.url && isOwnReleaseUrl(reply.url)}<a class="update-bar-link" data-testid="update-bar-link" href={reply.url} target="_blank" rel="noopener noreferrer">Open the release page</a>{/if}
    <button type="button" class="quiet-button" data-testid="update-bar-dismiss" onclick={() => dismissUpdate(reply.latest)}>Not now</button>
  </div>
{/if}

<style>
  .update-bar { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 14px; margin: 0; border-radius: 0; border-width: 0 0 1px; row-gap: 0; border-bottom: 1px solid var(--line); background: color-mix(in srgb, var(--accent) 8%, var(--surface)); color: var(--text); padding: 8px 24px; }
  .update-bar-text { font-weight: 500; }
  .update-bar-link { font-size: 13px; }
  .update-bar .quiet-button { margin-left: auto; }
  @media (max-width: 720px) { .update-bar { padding: 8px 16px; } }
</style>
