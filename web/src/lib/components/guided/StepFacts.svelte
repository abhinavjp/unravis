<script lang="ts" module>
  const STEP_HAS_SUSPECTS = new Set([1, 4, 5, 6, 7, 8, 9]);
</script>

<script lang="ts">
  import { GUIDED_TEXT as T } from '../../copy/guidedCopy';
  import type { GuidedStore } from '../../guided/guided.svelte';
  import {
    answeredRoute, fmtCount, fmtWhen, loadRows, movedCount, notEnoughData, nothingMoved, partBars, partRows, shapeRows, sourceRows, stepOf, stickerFor,
    suspectsForStep, trustCards
  } from '../../guided/stepModel';
  import type { Suspect } from '../../guided/types';
  import MinuteLine from './MinuteLine.svelte';
  import SlowCalls from './SlowCalls.svelte';
  import StepShell from './StepShell.svelte';
  import SuspectCard from './SuspectCard.svelte';

  // Steps 1 to 9. Each reads one part of the engine's answer. With no answer yet the page says so and shows nothing in its place.
  let { store, sourceIds, step, readOnly, onAddData }: { store: GuidedStore; sourceIds: string[]; step: number; readOnly: boolean; onAddData: () => void } = $props();

  const result = $derived(store.result);
  const engineStep = $derived(stepOf(result, step));
  const sticker = $derived(stickerFor(result, step, store.phase, store.outOfDate(sourceIds)));
  const suspects = $derived(result ? suspectsForStep(result, step) : []);
  const read = $derived(store.answer?.read);
  const focusRoute = $derived(answeredRoute(store.answeredKey));
  let showAll = $state(false);

  const GROUP_LEAD: Record<number, string> = { 6: T.groupLead6, 7: T.groupLead7, 8: T.groupLead8, 9: T.groupLead9 };
  const SOURCE_CTA: Record<number, string> = { 7: T.addSource, 8: T.addSource, 9: T.placeBlackbox };

  const addCheck = (s: Suspect) => { void store.addItem('todo', `${s.id}: ${s.nextCheck}`, s.id); };
</script>

<StepShell {step} {sticker} nutshell={engineStep?.nutshell ?? ''} reasons={engineStep?.reasons ?? []} onGo={(n) => store.goTo(n)} nav={store.mode === 'guided'}>
  {#if !result}
    <p class="note">{store.phase === 'running' ? T.running : T.jumpedAhead}</p>
  {:else if step === 1}
    <p class="lead">{T.trustLead}</p>
    {#if notEnoughData(result)}
      <div class="stop">
        <strong>{T.notEnoughTitle}</strong>
        <p>{T.notEnoughLead}</p>
        <ul>{#each T.notEnoughReasons as reason (reason)}<li>{reason}</li>{/each}</ul>
        <div class="row">
          <button type="button" class="secondary-button" onclick={() => store.goTo(0)}>{T.changeWindow}</button>
          <button type="button" class="secondary-button" disabled={readOnly} onclick={onAddData}>{T.addData}</button>
        </div>
      </div>
    {/if}
    <div class="cards">
      {#each trustCards(result) as card (card.label)}
        <div class="card"><span class="k">{card.label}</span><strong>{card.value}</strong><span class="n">{card.note}</span></div>
      {/each}
    </div>
    <div class="table-scroll">
      <table>
        <caption>{T.sourcesHeading}</caption>
        <thead><tr><th scope="col">{T.sourceCol}</th><th scope="col">{T.stateCol}</th></tr></thead>
        <tbody>{#each sourceRows(result) as row (row.name)}<tr><td>{row.name}</td><td>{row.state}</td></tr>{/each}</tbody>
      </table>
    </div>
    <h4>{T.gapsHeading}</h4>
    {#if (result.facts.gaps ?? []).length === 0}
      <p class="note">{T.noGaps}</p>
    {:else}
      <ul class="plain">
        {#each result.facts.gaps ?? [] as gap, i (i)}
          <li>{fmtWhen(gap.start)} to {fmtWhen(gap.end)} UTC, {gap.minutes} {gap.minutes === 1 ? 'minute' : 'minutes'}, {gap.window}. {gap.kind === 'long' ? T.gapLong : T.gapShort}</li>
        {/each}
      </ul>
    {/if}
    {#if read}
      <p class="note">{T.readNote(fmtCount(read.requests), fmtCount(read.iisRows))}</p>
      {#if read.truncated}<p class="warn" role="alert">{T.truncated}</p>{/if}
      {#each read.warnings as warning (warning)}<p class="note">{warning}</p>{/each}
    {/if}
  {:else if step === 2}
    <p class="lead">{T.shapeLead}</p>
    {#if nothingMoved(result)}
      <p class="finding">{T.nothingMoved(result.thresholds.changeMinPercent, result.thresholds.changeMinMs)}</p>
    {/if}
    {#if result.facts.routes.length === 0}
      <p class="note">{T.noRecords}</p>
    {:else}
      {@const rows = shapeRows(result)}
      {@const shown = showAll ? rows : rows.slice(0, 8)}
      <div class="table-scroll">
        <table>
          <caption>{T.byApi}. {fmtCount(movedCount(result))} of {fmtCount(rows.length)} moved.</caption>
          <thead><tr><th scope="col">{T.apiCol}</th><th scope="col">{T.callsCol}</th><th scope="col">{T.p95Col}</th><th scope="col">{T.refP95Col}</th><th scope="col">{T.changeCol}</th></tr></thead>
          <tbody>
            {#each shown as row (row.route)}
              <tr>
                <td>{row.route}</td><td>{fmtCount(row.calls)}</td><td>{row.incident}</td><td>{row.reference}</td>
                <td><span class={'mark ' + row.mark.tone}><span aria-hidden="true">{row.mark.symbol}</span> {row.mark.word}</span> {row.change}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if rows.length > 8}<div><button type="button" class="quiet-button" onclick={() => (showAll = !showAll)}>{showAll ? T.showLess : T.showAll}</button></div>{/if}
    {/if}
    {#if focusRoute}<p class="note">{T.byApiFocusNote}</p>{/if}
    <p class="note">{T.shapeRule(result.thresholds.changeMinPercent, result.thresholds.changeMinMs)}</p>
    {#if result.facts.retries.known}
      <p class="note">{T.retriesLine(fmtCount(result.facts.retries.incRetried), fmtCount(result.facts.retries.refRetried))}</p>
    {:else}
      <p class="note">{T.retriesUnknown}</p>
    {/if}
    {#if result.facts.shape.kind}<p class="note">{T.shapeKind(result.facts.shape.kind)}</p>{/if}
    {#if result.facts.series?.length}<MinuteLine series={result.facts.series} field="p95" label={T.p95Label} unit="ms" />{/if}
  {:else if step === 3}
    <p class="lead">{T.splitLead}</p>
    {@const rows = partRows(result)}
    <div class="table-scroll">
      <table>
        <thead><tr><th scope="col">{T.partCol}</th><th scope="col">{T.normalCol}</th><th scope="col">{T.incidentCol}</th><th scope="col">{T.changeCol}</th><th scope="col">{T.shareCol}</th></tr></thead>
        <tbody>{#each rows as row (row.key)}<tr><td>{row.label}</td><td>{row.normal}</td><td>{row.incident}</td><td>{row.change}</td><td>{row.share}</td></tr>{/each}</tbody>
      </table>
    </div>
    {@const bars = partBars(result)}
    {#if bars.normal.length || bars.incident.length}
      <div class="bars" role="img" aria-label={T.barLabel}>
        <div class="bar-row">
          <span class="bar-name">{T.barNormal}</span>
          <span class="bar-track">{#each bars.normal as b (b.key)}<span class={'seg part-' + b.key} style={`width:${b.pct}%`} title={`${b.label}: ${Math.round(b.ms)} ms`}></span>{/each}</span>
        </div>
        <div class="bar-row">
          <span class="bar-name">{T.barIncident}</span>
          <span class="bar-track">{#each bars.incident as b (b.key)}<span class={'seg part-' + b.key} style={`width:${b.pct}%`} title={`${b.label}: ${Math.round(b.ms)} ms`}></span>{/each}</span>
        </div>
      </div>
      <p class="note">{T.barLabel}</p>
    {/if}
    {#each rows.filter((r) => !r.tested) as row (row.key)}<p class="note">{T.untestedParts} {row.label}: {row.why || T.noRecords}</p>{/each}
    {#if result.facts.parts.dominantWhy}<p class="note">{result.facts.parts.dominantWhy}</p>{/if}
    {#if !result.facts.parts.oTested}
      <div class="row"><button type="button" class="secondary-button" disabled={readOnly} onclick={onAddData}>{T.addAuditLogs}</button></div>
    {/if}
    <SlowCalls {store} {sourceIds} />
  {:else if step === 4}
    <p class="lead">{T.loadLead}</p>
    <div class="table-scroll">
      <table>
        <thead><tr><th scope="col">{T.measureCol}</th><th scope="col">{T.normalCol}</th><th scope="col">{T.incidentCol}</th><th scope="col">{T.changeCol}</th></tr></thead>
        <tbody>{#each loadRows(result) as row (row.measure)}<tr><td>{row.measure}</td><td>{row.normal}</td><td>{row.incident}</td><td>{row.direction}</td></tr>{/each}</tbody>
      </table>
    </div>
    {#if result.facts.load.reading}<p class="finding">{result.facts.load.reading}</p>{/if}
    {#if result.signals.pool.entered && result.signals.pool.reading}<p class="note">{result.signals.pool.reading}</p>{/if}
    {#if result.facts.series?.length}<MinuteLine series={result.facts.series} field="inFlight" label={T.inFlightLabel} unit="calls" />{/if}
  {:else if step === 5}
    <p class="lead">{T.changesLead}</p>
    {#if !result.signals.changes.entered || result.signals.changes.items.length === 0}
      <p class="note">{T.changesNone}</p>
      <div><button type="button" class="secondary-button" onclick={() => store.goTo(0)}>{T.changeWindow}</button></div>
    {:else}
      <div class="table-scroll">
        <table>
          <thead><tr><th scope="col">{T.changeTimeCol}</th><th scope="col">{T.changeWhatCol}</th><th scope="col">{T.changeRelCol}</th></tr></thead>
          <tbody>{#each result.signals.changes.items as item, i (i)}<tr><td>{fmtWhen(item.at)} UTC</td><td>{item.label}</td><td>{item.relation}</td></tr>{/each}</tbody>
        </table>
      </div>
      {#if result.signals.changes.why}<p class="note">{result.signals.changes.why}</p>{/if}
    {/if}
    {#if result.signals.changes.caveat}<p class="note">{result.signals.changes.caveat}</p>{/if}
  {:else}
    <p class="lead">{GROUP_LEAD[step] ?? ''}</p>
  {/if}

  {#if result && (engineStep?.findings.length ?? 0) > 0}
    <div class="findings">
      <h4>{T.findings}</h4>
      <ul class="plain">{#each engineStep?.findings ?? [] as finding, i (i)}<li>{finding}</li>{/each}</ul>
    </div>
  {/if}

  {#if result && step >= 6 && step <= 9 && sticker.tone === 'cannot'}
    <div class="row"><button type="button" class="secondary-button" disabled={readOnly} onclick={onAddData}>{SOURCE_CTA[step]}</button></div>
  {/if}

  {#if result && STEP_HAS_SUSPECTS.has(step)}
    <div class="suspects">
      <h4>{T.suspectsHere}</h4>
      {#if suspects.length === 0}
        <p class="note">{T.noSuspectsHere}</p>
      {:else}
        {#each suspects as s (s.id)}<SuspectCard suspect={s} compact canAdd={!readOnly} onAdd={addCheck} />{/each}
      {/if}
    </div>
  {/if}
</StepShell>

<style>
  .lead, .note, .finding { margin: 0; font-size: 13px; overflow-wrap: anywhere; }
  .lead { font-size: 14px; }
  .note { color: var(--text-2); font-size: 12px; }
  .finding { padding: 8px 12px; background: var(--surface-2); border-radius: var(--radius-sm); font-size: 14px; }
  .warn { margin: 0; overflow-wrap: anywhere; padding: 8px 12px; font-size: 13px; border: 1px solid var(--warn); border-radius: var(--radius-sm); }
  h4 { margin: 0; font-size: 14px; }
  .row { display: flex; flex-wrap: wrap; gap: 8px; }
  .stop { display: grid; gap: 6px; padding: 12px; border: 1px solid var(--warn); border-radius: var(--radius-sm); }
  .stop p, .stop ul { margin: 0; font-size: 13px; overflow-wrap: anywhere; }
  .stop ul { padding-left: 18px; color: var(--text-2); }
  .cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 1fr)); gap: 8px; }
  .card { display: grid; gap: 2px; min-width: 0; overflow-wrap: anywhere; padding: 10px 12px; background: var(--surface-2); border-radius: var(--radius-sm); }
  .card .k { color: var(--text-2); font-size: 12px; }
  .card strong { font-size: 18px; font-variant-numeric: tabular-nums; }
  .card .n { color: var(--text-2); font-size: 12px; }
  caption { padding: 0 0 6px; text-align: left; font-size: 13px; font-weight: 600; }
  table { white-space: normal; }
  .plain { margin: 0; overflow-wrap: anywhere; padding-left: 18px; display: grid; gap: 4px; font-size: 13px; }
  .mark { font-weight: 600; white-space: nowrap; }
  .mark.bad { color: var(--suspect-red); }
  .mark.ok { color: var(--suspect-green); }
  .mark.grey { color: var(--text-2); }
  .bars { display: grid; gap: 6px; }
  .bar-row { display: grid; grid-template-columns: 72px 1fr; align-items: center; gap: 8px; font-size: 12px; }
  .bar-track { display: flex; height: 16px; background: var(--chart-empty); border-radius: var(--radius-sm); overflow: hidden; }
  .seg { height: 100%; min-width: 1px; }
  .part-A { background: var(--series-1); }
  .part-C { background: var(--series-4); }
  .part-P { background: var(--series-2); }
  .part-O { background: var(--series-3); }
  .suspects, .findings { display: grid; gap: 8px; }
</style>
