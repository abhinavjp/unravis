<script lang="ts">
  import { rewrapCases, rewrapCounts } from '../lib/masterKeyApi';
  import { KEY_TEXT, rewrapAction, rewrapDone, rewrapOffer } from '../lib/copy/masterKeyCopy';

  // A small notice on Home while some cases are still protected the older way and the master key is open. The one button protects them with the
  // master key too, and it can be pressed again after a try that did not finish. `onChange` gets how many are still left.
  let { count, onChange }: { count: number; onChange: (remaining: number) => void } = $props();

  let busy = $state(false);
  let note = $state('');

  async function protect() {
    if (busy) return;
    busy = true;
    note = '';
    try {
      const counts = rewrapCounts(await rewrapCases({ all: true }));
      note = counts.done + counts.failed === 0 ? KEY_TEXT.rewrapNone : rewrapDone(counts.done, counts.failed);
      onChange(counts.failed);
    } catch {
      note = KEY_TEXT.rewrapFailed;
    } finally {
      busy = false;
    }
  }
</script>

<section class="rewrap" aria-label={KEY_TEXT.rewrapLabel}>
  {#if count > 0}
    <p class="rewrap-text">{rewrapOffer(count)}</p>
    <button type="button" class="secondary-button" disabled={busy} onclick={() => void protect()}>{busy ? KEY_TEXT.rewrapping : rewrapAction(count)}</button>
  {/if}
  <p class="rewrap-text" role="status">{note}</p>
</section>

<style>
  .rewrap { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2) var(--space-3); padding: var(--space-2) var(--space-3); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-family: var(--mono); }
  .rewrap-text { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .rewrap-text:empty { display: none; }
</style>
