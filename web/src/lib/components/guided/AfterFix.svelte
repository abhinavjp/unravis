<script lang="ts">
  import { GUIDED_TEXT as T } from '../../copy/guidedCopy';
  import { classifyDay, dayLabel, laterFrame, verdictIsCurrent, type FixVerdict } from '../../guided/afterFix';
  import { runInvestigation } from '../../guided/api';
  import { frameProblems, toRequest, windowMinutes } from '../../guided/frame';
  import type { GuidedStore } from '../../guided/guided.svelte';

  // After the fix: the same hours on a later day, set beside the incident day. One more run of the engine with the later day as the incident
  // window and the earlier day as the reference. Nothing here says a day is fixed unless the slow end is back inside the normal range.
  let { store, sourceIds, readOnly }: { store: GuidedStore; sourceIds: string[]; readOnly: boolean } = $props();

  let day = $state('');
  let route = $state('');
  let phase = $state<'idle' | 'running' | 'done' | 'failed'>('idle');
  let made = $state.raw<FixVerdict | null>(null);
  // The answer the verdict was compared against: a new main run makes the verdict stale, so it goes from the page.
  let madeFor = $state.raw<unknown>(null);
  let problem = $state('');
  let recorded = $state(false);
  let run = 0;
  let controller: AbortController | null = null;

  const result = $derived(store.result);
  const verdict = $derived(made && verdictIsCurrent(madeFor, store.answer) ? made : null);
  const routes = $derived((result?.facts.routes ?? []).map((r) => r.route));

  async function compare() {
    problem = '';
    recorded = false;
    if (!result) { problem = T.fixNeedsRun; return; }
    if (!day) { problem = T.fixDayFirst; return; }
    const frame = laterFrame({ ...store.frame, route: route || store.frame.route }, day);
    if (!frame) { problem = T.fixDayFirst; return; }
    const issues = frameProblems(frame);
    const request = toRequest(frame, sourceIds, false);
    if (!request) { problem = issues[0] ?? T.noSources; return; }
    const base = result;
    const forAnswer = store.answer;
    controller?.abort();
    const mine = new AbortController();
    controller = mine;
    const id = ++run;
    phase = 'running';
    try {
      const answer = await runInvestigation(store.caseId, request, mine.signal);
      if (id !== run) return;
      made = classifyDay(answer.result, {
        normalP95: base.facts.change.p95.refMs,
        windowMinutes: windowMinutes(frame.incidentStart, frame.incidentEnd),
        laterLabel: dayLabel(request.incident.start, request.incident.end)
      });
      madeFor = forAnswer;
      phase = 'done';
    } catch {
      if (id !== run || mine.signal.aborted) return;
      phase = 'failed';
    }
  }

  async function record() {
    if (!verdict?.canRecord) return;
    if (await store.addItem('learned', verdict.recordLine)) recorded = true;
  }
  $effect(() => () => { run++; controller?.abort(); });
</script>

<section class="fix" aria-labelledby="fix-title">
  <header><h3 id="fix-title">{T.fixHeading}</h3></header>
  <p class="lead">{T.fixLead}</p>
  {#if !result}
    <p class="notice">{T.fixNeedsRun}</p>
  {:else}
    <p class="note">{T.fixNote}</p>
    <div class="form">
      <label>{T.fixDay}<input type="date" bind:value={day} /></label>
      <label>{T.fixRoute}
        <select bind:value={route}>
          <option value="">{T.allApis}</option>
          {#each routes as r (r)}<option value={r}>{r}</option>{/each}
        </select>
      </label>
    </div>
    <div><button type="button" class={verdict ? 'secondary-button' : 'primary-button'} disabled={phase === 'running'} onclick={() => void compare()}>{T.fixRun}</button></div>
  {/if}

  {#if problem}<p class="inline-error" role="alert">{problem}</p>{/if}
  {#if phase === 'running'}<p class="loading-state" role="status">{T.fixRunning}</p>{/if}
  {#if phase === 'failed'}<p class="inline-error" role="alert">{T.fixFailed}</p>{/if}

  {#if verdict && phase === 'done'}
    <div class="verdict" role="status">
      <h4>{verdict.word}</h4>
      <p class="finding">{verdict.sentence}</p>
    </div>
    <div class="table-scroll">
      <table>
        <thead><tr><th scope="col">{T.measureFix}</th><th scope="col">{T.earlierDay}</th><th scope="col">{T.laterDay}</th><th scope="col">{T.changeVsEarlier}</th></tr></thead>
        <tbody>
          {#each verdict.rows as row (row.measure)}
            <tr>
              <td>{row.measure}</td><td>{row.earlier}</td><td>{row.later}</td>
              <td>{#if row.mark}<span class={'mark ' + row.mark.tone}><span aria-hidden="true">{row.mark.symbol}</span> {row.mark.word}</span> {/if}{row.change}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    {#if verdict.warnings.length > 0}
      <div class="warn" role="alert">
        <strong>{T.fixWarnings}</strong>
        <ul>{#each verdict.warnings as w (w)}<li>{w}</li>{/each}</ul>
      </div>
    {/if}
    {#if verdict.outcome === 'unlike'}<p class="note">{T.pickAlike}. {T.oneRoute}.</p>{/if}
    {#if verdict.canRecord}
      {#if readOnly}
        <p class="notice">{T.fixClosed}</p>
      {:else if recorded}
        <p class="note" role="status">{T.recorded}</p>
      {:else}
        <div class="row">
          <button type="button" class="primary-button" onclick={() => void record()}>{verdict.warnings.length > 0 ? T.recordWarning : T.record}</button>
          <button type="button" class="quiet-button" onclick={() => { made = null; phase = 'idle'; }}>{T.notNow}</button>
        </div>
      {/if}
    {/if}
  {/if}
</section>

<style>
  .fix { display: grid; gap: 12px; min-width: 0; padding: 16px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  h3 { margin: 0; font-size: 16px; }
  h4 { margin: 0; font-size: 14px; }
  .lead, .note { margin: 0; font-size: 13px; }
  .note { color: var(--text-2); font-size: 12px; }
  .form { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 1fr)); gap: 8px; }
  .form label { display: grid; gap: 4px; font-size: 13px; }
  .verdict { display: grid; gap: 4px; }
  .finding { margin: 0; padding: 8px 12px; background: var(--surface-2); border-radius: var(--radius-sm); font-size: 14px; }
  .warn { display: grid; gap: 4px; padding: 8px 12px; border: 1px solid var(--warn); border-radius: var(--radius-sm); font-size: 13px; }
  .warn ul { margin: 0; padding-left: 18px; }
  table { white-space: normal; }
  .mark { font-weight: 600; white-space: nowrap; margin-inline-end: 0.5em; }
  .mark.bad { color: var(--suspect-red); }
  .mark.ok { color: var(--suspect-green); }
  .mark.grey { color: var(--text-2); }
  .row { display: flex; flex-wrap: wrap; gap: 8px; }
</style>
