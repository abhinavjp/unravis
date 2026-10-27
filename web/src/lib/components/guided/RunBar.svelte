<script lang="ts">
  import { GUIDED_TEXT as T } from '../../copy/guidedCopy';
  import { frameProblems } from '../../guided/frame';
  import type { GuidedStore } from '../../guided/guided.svelte';

  // The run bar. "Run the steps" is the one primary button of the steps page until an answer is on screen; after that it is "Run again" and
  // secondary. A run only reads: it cannot change a case or a source.
  let { store, sourceIds, readOnly, onAddData }: { store: GuidedStore; sourceIds: string[]; readOnly: boolean; onAddData: () => void } = $props();

  const problems = $derived(frameProblems(store.frame));
  const running = $derived(store.phase === 'running');
  const stale = $derived(store.outOfDate(sourceIds));
  const hasAnswer = $derived(!!store.answer);
  const label = $derived(store.phase === 'failed' ? T.retry : hasAnswer ? T.rerun : T.run);
</script>

<div class="bar">
  {#if sourceIds.length === 0}
    <p class="note" role="status">{T.noSources}</p>
    <div><button type="button" class="secondary-button" disabled={readOnly} onclick={onAddData}>{T.addData}</button></div>
  {:else}
    <div class="row">
      <button type="button" class={hasAnswer && store.phase !== 'failed' ? 'secondary-button' : 'primary-button'} disabled={running || problems.length > 0} onclick={() => void store.run(sourceIds)}>{label}</button>
      {#if running}<button type="button" class="secondary-button" onclick={() => store.cancel()}>{T.cancel}</button>{/if}
    </div>
    {#if problems.length > 0}<p class="note">{T.fixWindows} {problems[0]}</p>{/if}
    {#if running}<p class="loading-state" role="status">{T.running}</p>{/if}
    {#if store.error}<p class="inline-error" role="alert">{store.error}</p>{/if}
    {#if stale}<p class="notice" role="status">{T.outOfDateNote}</p>{/if}
    {#if !hasAnswer && !running && !store.error && problems.length === 0}<p class="note">{T.needRun}</p>{/if}
  {/if}
</div>

<style>
  .bar { display: grid; gap: 8px; }
  .row { display: flex; flex-wrap: wrap; gap: 8px; }
  .note { margin: 0; font-size: 12px; color: var(--text-2); }
</style>
