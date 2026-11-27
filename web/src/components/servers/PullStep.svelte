<script lang="ts">
  import StepFooter from './StepFooter.svelte';
  import type { ServersFlow } from '../../lib/serversFlow.svelte';
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';
  import { failureText, formatBytes } from '../../lib/servers';

  // Step 6. Progress comes from the copy's own job on the backend, read about once a second. Stop asks the server side to stop.
  let { flow }: { flow: ServersFlow } = $props();

  const anyRunning = $derived(flow.order.some((k) => flow.pulls[k]?.state === 'running'));
  const anyStopping = $derived(flow.order.some((k) => flow.pulls[k]?.stopping));
  const finished = $derived(flow.order.length > 0 && flow.order.every((k) => { const s = flow.pulls[k]?.state; return s === 'done' || s === 'failed' || s === 'stopped'; }));
  const anyDone = $derived(flow.order.some((k) => flow.pulls[k]?.state === 'done'));
  const broken = $derived(flow.order.filter((k) => { const p = flow.pulls[k]; return !!p?.job && (p.state === 'stopped' || p.state === 'failed'); }));

  const failureLines = $derived([...new Set(broken.map((k) => flow.pulls[k]?.failure).filter((f): f is NonNullable<typeof f> => !!f).map((f) => failureText(f)))]);
  const planned = $derived(flow.order.reduce((n, k) => n + (flow.pulls[k]?.planned ?? 0), 0));
  const copiedOf = (k: string): number => {
    const p = flow.pulls[k];
    if (!p) return 0;
    if (p.state === 'done') return p.planned;
    return Math.min(p.planned, p.job?.progress?.jobBytes ?? 0);
  };
  const copied = $derived(flow.order.reduce((n, k) => n + copiedOf(k), 0));
  const overall = $derived(planned > 0 ? (finished && !broken.length ? 100 : Math.min(99, Math.floor((copied / planned) * 100))) : 0);
  const statusOf = (k: string): { tone: 'ok' | 'bad' | 'wait'; word: string } => {
    const p = flow.pulls[k];
    if (!p || p.state === 'queued') return { tone: 'wait', word: T.pullQueued };
    if (p.state === 'done') return { tone: 'ok', word: T.pullDoneStatus };
    if (p.state === 'failed') return { tone: 'bad', word: T.pullFailedStatus };
    if (p.state === 'stopped') return { tone: 'bad', word: T.pullStoppedStatus };
    return { tone: 'wait', word: T.pullCopyingStatus };
  };
  const fraction = (k: string): number | null => {
    const p = flow.pulls[k];
    if (p?.state === 'done') return 100;
    if (!p || p.state === 'queued') return 0;
    return flow.percent(k);
  };
</script>

<section class="sv-block" aria-labelledby="sv-pull-title">
  <h3 id="sv-pull-title">{T.pullTitle}</h3>
  <p class="sv-lead">{T.pullLead}</p>
  {#if flow.note === 'attached'}<p class="sv-note">{T.pullAttached}</p>{/if}

  {#if broken.length > 0 && !flow.running}
    <div class="sv-stop" role="alert">
      <h4>{T.pullDropHeading}</h4>
      <p class="sv-note">{T.pullDropBody(formatBytes(copied), formatBytes(planned))}</p>
      {#each failureLines as line (line)}<p class="sv-bad">{line}</p>{/each}
      <div class="sv-actions">
        <button type="button" class="primary-button" onclick={() => flow.resume(broken[0])}>{T.pullResume}</button>
        <button type="button" class="secondary-button" onclick={() => flow.go('has')}>{T.pullChooseAgain}</button>
      </div>
      <p class="sv-note">{T.pullResumeHelp}</p>
    </div>
  {/if}

  <div class="sv-panel">
    <header><h4 role="status">{T.pullHeading(overall, formatBytes(planned))}</h4><span class="sv-sp"></span>{#if flow.order.some((k) => flow.pulls[k]?.stopping)}<span class="sv-sub" role="status">{T.pullStopping}</span>{:else if broken.length > 0 && !flow.running}<span class="sv-sub">{T.pullStoppedStatus}</span>{/if}</header>
    <div class="sv-table-scroll">
    <table class="sv-tbl">
      <thead><tr><th>{T.pullColMachine}</th><th>{T.pullColWhat}</th><th>{T.pullColSize}</th><th>{T.pullColProgress}</th><th>{T.pullColStatus}</th></tr></thead>
      <tbody>
        {#each flow.order as key (key)}
          {@const p = flow.pulls[key]}
          {@const t = flow.targets.find((x) => x.key === key)}
          {#if p && t}
            {@const pct = fraction(key)}
            {@const st = statusOf(key)}
            <tr>
              <td><b>{t.machine}</b></td>
              <td>{t.label}<div class="sv-sub sv-mono">{t.dir}</div></td>
              <td class="sv-mono">{formatBytes(p.planned)}</td>
              <td class="sv-bar-cell">
                <div class="sv-meter" class:sv-meter-wait={st.tone === 'wait' && !pct} class:sv-meter-bad={st.tone === 'bad'} role="progressbar" aria-label={t.label + ' ' + t.machine} aria-valuemin="0" aria-valuemax="100" aria-valuenow={pct ?? undefined}>
                  <i class:sv-unknown={pct === null && p.state === 'running'} style={pct === null ? undefined : 'width:' + pct + '%'}></i>
                </div>
                {#if p.job?.progress && p.state === 'running'}<div class="sv-sub">{T.pullFile(Math.min(p.job.progress.filesDone + 1, p.job.progress.filesTotal || 1), p.job.progress.filesTotal)}, {T.pullCopied(formatBytes(p.job.progress.jobBytes))}</div>{/if}
              </td>
              <td><span class="sv-st" class:sv-st-ok={st.tone === 'ok'} class:sv-st-bad={st.tone === 'bad'}>{st.word}</span></td>
            </tr>
          {/if}
        {/each}
      </tbody>
    </table>
    </div>
  </div>

  {#snippet extra()}{#if anyRunning}<button type="button" class="secondary-button sv-danger" disabled={anyStopping} onclick={() => flow.stop()}>{T.pullStop}</button>{/if}{/snippet}
  <StepFooter back={anyRunning ? undefined : () => flow.go('has')} {extra} primary={{ label: T.pullNext, onclick: () => flow.goLand(), disabled: !finished || !anyDone }} />
</section>

<style>
  .sv-table-scroll { overflow-x: auto; }
  .sv-bar-cell { min-width: 150px; display: table-cell; }
  .sv-danger { color: var(--bad); border-color: var(--bad); }
  .sv-unknown { width: 30%; opacity: 0.6; }
</style>
