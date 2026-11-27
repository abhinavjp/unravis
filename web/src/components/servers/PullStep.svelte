<script lang="ts">
  import type { ServersFlow } from '../../lib/serversFlow.svelte';
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';
  import { failureText, formatBytes, jobStateLabel } from '../../lib/servers';

  // Step 6. Progress comes from the copy's own job on the backend, read about once a second. Stop asks the server side to stop.
  let { flow }: { flow: ServersFlow } = $props();

  const anyRunning = $derived(flow.order.some((k) => flow.pulls[k]?.state === 'running'));
  const anyStopping = $derived(flow.order.some((k) => flow.pulls[k]?.stopping));
  const finished = $derived(flow.order.length > 0 && flow.order.every((k) => { const s = flow.pulls[k]?.state; return s === 'done' || s === 'failed' || s === 'stopped'; }));
  const anyDone = $derived(flow.order.some((k) => flow.pulls[k]?.state === 'done'));
</script>

<section class="sv-block" aria-labelledby="sv-pull-title">
  <h3 id="sv-pull-title">{T.pullTitle}</h3>
  <p class="sv-lead">{T.pullLead}</p>
  {#if flow.note === 'attached'}<p class="sv-note">{T.pullAttached}</p>{/if}

  <ul class="sv-list" aria-label={T.pullTitle}>
    {#each flow.order as key (key)}
      {@const p = flow.pulls[key]}
      {@const t = flow.targets.find((x) => x.key === key)}
      {#if p && t}
        {@const pct = flow.percent(key)}
        <li class="sv-row">
          <div class="sv-row-main"><strong>{t.label}</strong> <span class="sv-note">{t.machine}</span>
            <span class="sv-chip sv-tone-{p.state === 'done' ? 'ok' : p.state === 'failed' ? 'bad' : 'pending'}">{p.state === 'queued' ? T.pullQueued : jobStateLabel(p.state)}</span></div>
          <code class="sv-dir">{t.dir}</code>
          {#if p.state === 'running' || p.state === 'done'}
            <div class="sv-bar" role="progressbar" aria-label={t.label + ' ' + t.machine} aria-valuemin="0" aria-valuemax="100" aria-valuenow={pct ?? undefined}>
              <span class="sv-bar-fill" class:sv-bar-unknown={pct === null} style={pct === null ? undefined : 'width:' + pct + '%'}></span>
            </div>
          {/if}
          {#if p.job?.progress}
            <p class="sv-note">{T.pullFile(Math.min(p.job.progress.filesDone + (p.state === 'running' ? 1 : 0), p.job.progress.filesTotal || 1), p.job.progress.filesTotal)}, {T.pullCopied(formatBytes(p.job.progress.jobBytes))}{#if pct !== null} . {pct}%{/if}</p>
          {/if}
          {#if p.stopping}<p class="sv-note" role="status">{T.pullStopping}</p>{/if}
          {#if p.failure}<p class="sv-bad" role="alert">{failureText(p.failure)}</p>{/if}
          {#if (p.state === 'stopped' || p.state === 'failed') && p.job && !flow.running}
            <div class="sv-actions">
              <button type="button" class="secondary-button" onclick={() => flow.resume(key)}>{T.pullResume}</button>
              <span class="sv-note">{T.pullResumeHelp}</span>
            </div>
          {/if}
        </li>
      {/if}
    {/each}
  </ul>

  <div class="sv-actions">
    {#if anyRunning}
      <button type="button" class="secondary-button" disabled={anyStopping} onclick={() => flow.stop()}>{T.pullStop}</button>
    {/if}
    {#if finished && anyDone}
      <button type="button" class="primary-button" onclick={() => flow.goLand()}>{T.pullNext}</button>
    {/if}
  </div>
</section>

<style>
  .sv-list { display: grid; margin: 0; padding: 0; list-style: none; }
  .sv-dir { font-family: var(--mono); font-size: 12px; overflow-wrap: anywhere; color: var(--text-2); }
  .sv-bar { height: 8px; background: var(--surface-2); border: 1px solid var(--line); border-radius: 4px; overflow: hidden; }
  .sv-bar-fill { display: block; height: 100%; background: var(--accent); transition: width 0.3s ease; }
  .sv-bar-unknown { width: 30%; opacity: 0.6; }
  @media (prefers-reduced-motion: reduce) { .sv-bar-fill { transition: none; } }
</style>
