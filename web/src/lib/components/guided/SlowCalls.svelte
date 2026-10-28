<script lang="ts">
  import { api } from '../../api';
  import { GUIDED_TEXT as T } from '../../copy/guidedCopy';
  import { slowQuery } from '../../guided/frame';
  import type { GuidedStore } from '../../guided/guided.svelte';
  import { fmtMs, fmtWhen } from '../../guided/stepModel';
  import type { RequestRecord } from '../../types';

  // The longest calls that started in the incident window. Only the route, the start and the duration are shown: no user, address or URL.
  let { store, sourceIds }: { store: GuidedStore; sourceIds: string[] } = $props();

  let rows = $state.raw<RequestRecord[]>([]);
  let phase = $state<'idle' | 'loading' | 'done' | 'failed'>('idle');
  let controller: AbortController | null = null;

  async function load() {
    const query = slowQuery(store.frame, sourceIds);
    if (!query) { phase = 'failed'; return; }
    controller?.abort();
    const mine = new AbortController();
    controller = mine;
    phase = 'loading';
    try {
      const page = await api.slowest(query, mine.signal);
      if (mine.signal.aborted) return;
      rows = (page?.rows ?? []).slice(0, 10);
      phase = 'done';
    } catch {
      if (!mine.signal.aborted) phase = 'failed';
    }
  }
  $effect(() => () => controller?.abort());
</script>

<div class="slow">
  <h4>{T.slowestCalls}</h4>
  <div><button type="button" class="secondary-button" disabled={phase === 'loading'} onclick={() => void load()}>{T.showSlowCalls}</button></div>
  {#if phase === 'loading'}<p class="note" role="status">{T.running}</p>
  {:else if phase === 'failed'}<p class="note" role="alert">{T.toolFailed}</p>
  {:else if phase === 'done'}
    {#if rows.length === 0}
      <p class="note">{T.noRecords}</p>
    {:else}
      <div class="table-scroll">
        <table>
          <thead><tr><th scope="col">{T.apiCol}</th><th scope="col">{T.startedCol}</th><th scope="col">{T.timeCol}</th></tr></thead>
          <tbody>
            {#each rows as row (row.id)}
              <tr><td>{row.controller}.{row.action}</td><td>{fmtWhen(row.initiatedAt ?? undefined)}</td><td>{fmtMs(row.durationMs ?? undefined)}</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
      <p class="note">{T.slowCallsLead}</p>
    {/if}
  {/if}
</div>

<style>
  .slow { display: grid; gap: 8px; min-width: 0; }
  h4 { margin: 0; font-size: 14px; }
  .note { margin: 0; color: var(--text-2); font-size: 12px; }
</style>
