<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { ApiError, api, readSessionToken } from '../lib/api';
  import { DRAWER_RESULTS_TEXT, clearDrawerResults, foundAtText, patternsWindowKey, readPatterns, savePatterns } from '../lib/drawerResults.svelte';
  import { formatTime, quickRange, quickRanges, type QuickRangeId } from '../lib/time';
  import { activeRangeId } from '../lib/depth';
  import { app, closeSidePanel, onScopeChange, scope, showRecord } from '../lib/appState.svelte';
  import { citeInLedger } from '../lib/citeInLedger';
  import { formatElapsed } from '../lib/provenance';
  import {
    DEFAULT_EPISODE_GAP_SECONDS, DEFAULT_STALL_SECONDS, DEFAULT_STEP_DAYS, PATTERNS_TEXT, PATTERN_RANGES, STEP_DAY_CHOICES, STEP_TEXT, auditSeriesSection, auditStallLine, citeLabel, type SplitPatch, concentrationSection, coverageLine,
    emptyEpisodesText, endGapsText, episodeCiteLabel, episodeEvidenceId, findingRows, idleSection, isEvidenceId, nBadge, onsetSection, onsetZoneNote, operationText, qualityLine, repeatsOf, repeatsSummary, secondsLabel,
    stallsOf, stepSection, thresholdsChanged, truncationNotices, verdictText, volleysOf, windowInputSeconds, zoneMinuteShift
  } from '../lib/patterns';
  import type { PatternReport, Query, RequestRecord } from '../lib/types';
  import Hint from './Hint.svelte';
  import StallMinutesPanel from './StallMinutesPanel.svelte';
  import ProvenanceNote from './ProvenanceNote.svelte';
  import Drawer from './Drawer.svelte';

  // The Patterns drawer. It never runs by itself: the button starts one search over the window already on screen.
  // Summary first (verdict, findings, counts), then the episode table, which opens one episode at a time.
  // The range chips change the page's own range. A slow request or a slow audit save opens its record (and closes this drawer).
  let { query, zone, sourceLabels, onClose }: { query: Query; zone: string; sourceLabels: Map<string, string>; onClose: () => void } = $props();
  type State = { kind: 'idle' } | { kind: 'running' } | { kind: 'done'; report: PatternReport; key: string; idleAsked: boolean; stepAsked: boolean; auditAsked: boolean } | { kind: 'error'; text: string };
  let phase = $state<State>({ kind: 'idle' });
  let stallSec = $state(String(DEFAULT_STALL_SECONDS));
  let gapSec = $state(String(DEFAULT_EPISODE_GAP_SECONDS));
  // The quiet-gap check reads every request in the window, so it runs only when this box is ticked.
  let idleGaps = $state(false);
  // Step dating and the audit-create series read one day at a time or every request, so each runs only when its box is ticked.
  let stepOn = $state(false);
  let stepDays = $state(String(DEFAULT_STEP_DAYS));
  let auditSeries = $state(false);
  // The suggested split is applied only after a second, explicit press.
  let splitAsking = $state(false);
  let splitBox = $state<HTMLElement | null>(null);
  // The buttons swap in place, so focus follows them. Otherwise a keyboard user lands on the page top.
  async function moveSplit(asking: boolean) {
    splitAsking = asking;
    if (asking) splitNote = '';
    await tick();
    splitBox?.querySelector<HTMLElement>(asking ? '.primary-button' : '.secondary-button')?.focus();
  }
  let splitNote = $state('');
  let thresholdError = $state('');
  let openEpisodes = $state<number[]>([]);
  let citeNote = $state('');
  let runButton = $state<HTMLButtonElement>();
  // The last three reports are kept in memory (lib/drawerResults.svelte.ts). Reopening shows the newest one for this window with its age and a Run again button.
  // savedAt is set only while the report on screen came from memory. A fresh run clears it.
  let savedAt = $state<number | null>(null);
  let now = $state(Date.now());
  let controller: AbortController | null = null;

  const sources = $derived(query.sourceIds.map((id) => sourceLabels.get(id) ?? id).join(', '));
  // Stalls and episodes can sit a few milliseconds apart, so every time here shows milliseconds.
  const at = (value: string) => formatTime(value, { zone, precision: 'ms' });
  const clock = (value: string) => formatTime(value, { zone, precision: 'minute', date: false, suffix: false });
  const labelOf = (id: string) => sourceLabels.get(id) ?? id;
  const activeRange = $derived(activeRangeId(query, quickRanges));
  const changed = $derived(thresholdsChanged(stallSec, gapSec));
  // What the search depends on. When it moves after a run, the numbers on screen describe an older question.
  const askKey = $derived([query.sourceIds.join(','), query.start, query.end, stallSec.trim(), gapSec.trim(), idleGaps ? 'idle' : '', stepOn ? 'step' + stepDays : '', auditSeries ? 'audit' : ''].join('|'));

  onMount(() => {
    runButton?.focus();
    // A forgotten session means nothing from before may be shown.
    if (!readSessionToken()) { clearDrawerResults(); return; }
    const saved = readPatterns(patternsWindowKey(query.sourceIds, query.start, query.end), app.sources.map((source) => source.id));
    if (!saved) return;
    stallSec = saved.stallSec;
    gapSec = saved.gapSec;
    idleGaps = saved.idleAsked;
    stepOn = (saved.stepDays ?? 0) > 0;
    if (stepOn) stepDays = String(saved.stepDays);
    auditSeries = !!saved.auditAsked;
    phase = { kind: 'done', report: saved.report, key: saved.key, idleAsked: saved.idleAsked, stepAsked: stepOn, auditAsked: auditSeries };
    savedAt = saved.savedAt;
    now = Date.now();
  });
  $effect(() => () => controller?.abort());
  // Keeps "N min ago" honest while a saved report stays on screen.
  $effect(() => {
    if (savedAt === null) return;
    const timer = setInterval(() => { now = Date.now(); }, 30_000);
    return () => clearInterval(timer);
  });

  function pickRange(id: QuickRangeId) {
    if (activeRange === id) return;
    const base = new Date(app.info?.now ?? '');
    Object.assign(scope.query, quickRange(id, Number.isFinite(base.getTime()) ? base : new Date()));
    scope.dateInputsValid = true;
    onScopeChange();
  }
  function reset() { stallSec = String(DEFAULT_STALL_SECONDS); gapSec = String(DEFAULT_EPISODE_GAP_SECONDS); thresholdError = ''; }

  async function run() {
    if (phase.kind === 'running') return;
    const stepAsked = stepOn, auditAsked = auditSeries;
    const built = windowInputSeconds(query, stallSec, gapSec, idleGaps, { stepDays: stepAsked ? Number(stepDays) : 0, auditSeries: auditAsked, zone });
    const idleAsked = idleGaps;
    if ('error' in built) { thresholdError = built.error; return; }
    thresholdError = '';
    controller?.abort();
    controller = new AbortController();
    const mine = controller;
    const key = askKey;
    const saveAs = { key, windowKey: patternsWindowKey(query.sourceIds, query.start, query.end), sourceIds: [...query.sourceIds], stallSec: stallSec.trim(), gapSec: gapSec.trim(), idleAsked, stepDays: stepAsked ? Number(stepDays) : 0, auditAsked };
    savedAt = null;
    phase = { kind: 'running' };
    openEpisodes = [];
    citeNote = '';
    splitAsking = false;
    splitNote = '';
    try {
      const report = await api.patterns(built.input, mine.signal);
      if (mine.signal.aborted) return;
      savePatterns({ ...saveAs, report });
      phase = { kind: 'done', report, key, idleAsked, stepAsked, auditAsked };
    } catch (cause) {
      if (mine.signal.aborted) return;
      if (cause instanceof ApiError && cause.status === 401) clearDrawerResults();
      phase = { kind: 'error', text: cause instanceof Error && cause.message ? cause.message : PATTERNS_TEXT.failed };
    }
  }
  // The page takes the suggested ranges only after the second press. The patch is checked again against the page's own rules.
  function applySplit(patch: SplitPatch) {
    Object.assign(scope.query, patch);
    scope.dateInputsValid = true;
    splitNote = STEP_TEXT.splitDone;
    void moveSplit(false);
    onScopeChange();
  }
  function toggle(index: number) {
    openEpisodes = openEpisodes.includes(index) ? openEpisodes.filter((i) => i !== index) : [...openEpisodes, index];
  }
  // The record view needs only the source and the id of the request. The drawer closes first so the record is not hidden behind it.
  function openRecord(sourceId: string, id: string) {
    void closeSidePanel();
    void showRecord({ sourceId, id } as RequestRecord);
  }
  async function cite(evidenceId: string, label: string) {
    const result = await citeInLedger(evidenceId, label);
    citeNote = result === 'added' ? PATTERNS_TEXT.citeAdded : result === 'already' ? PATTERNS_TEXT.citeAlready : result === 'copied' ? PATTERNS_TEXT.citeCopied : PATTERNS_TEXT.citeFailed;
  }
</script>

<Drawer class="glossary-drawer patterns-drawer" title={PATTERNS_TEXT.title} titleId="patterns-title" intro={PATTERNS_TEXT.intro} closeLabel="Close patterns" focusable={'button:not(:disabled), input:not(:disabled), summary, [href], [tabindex]:not([tabindex="-1"])'} {onClose}>

  <section class="patterns-window" aria-label="Window">
    <div class="range-row" role="group" aria-label={PATTERNS_TEXT.rangeLabel}>
      <span class="range-label">{PATTERNS_TEXT.rangeLabel}</span>
      {#each PATTERN_RANGES as preset (preset.id)}
        <button type="button" class="chip" class:on={activeRange === preset.id} aria-pressed={activeRange === preset.id} onclick={() => pickRange(preset.id)}>{preset.label}</button>
      {/each}
    </div>
    <p class="patterns-note">{PATTERNS_TEXT.rangeNote}</p>
    <dl>
      <div><dt>Sources</dt><dd>{sources || 'None selected'}</dd></div>
      <div><dt>From</dt><dd>{at(query.start)}</dd></div>
      <div><dt>To</dt><dd>{at(query.end)}</dd></div>
      <div><dt>Time zone</dt><dd>{zone}</dd></div>
    </dl>
    <details class="patterns-thresholds">
      <summary>{PATTERNS_TEXT.thresholds}{#if changed} ({PATTERNS_TEXT.thresholdsChanged}){/if}</summary>
      <div class="threshold-fields">
        <label>{PATTERNS_TEXT.stallLabel}<input type="text" inputmode="decimal" bind:value={stallSec} autocomplete="off" /></label>
        <label>{PATTERNS_TEXT.gapLabel}<input type="text" inputmode="numeric" bind:value={gapSec} autocomplete="off" /></label>
      </div>
      <p class="patterns-note">{PATTERNS_TEXT.thresholdNote}</p>
      {#if changed}<button type="button" class="reset-link" onclick={reset}>{PATTERNS_TEXT.reset}</button>{/if}
    </details>
    <label class="idle-option"><input type="checkbox" bind:checked={idleGaps} /> {PATTERNS_TEXT.idleOptIn}</label>
    <p class="patterns-note">{PATTERNS_TEXT.idleOptInNote}</p>
    <label class="idle-option"><input type="checkbox" bind:checked={auditSeries} /> {STEP_TEXT.seriesOptIn}</label>
    <p class="patterns-note">{STEP_TEXT.seriesOptInNote}</p>
    <label class="idle-option"><input type="checkbox" bind:checked={stepOn} /> {STEP_TEXT.stepOptIn}</label>
    {#if stepOn}
      <label class="step-days">{STEP_TEXT.stepDaysLabel}
        <select bind:value={stepDays}>
          {#each STEP_DAY_CHOICES as choice (choice)}<option value={String(choice)}>{choice}</option>{/each}
        </select>
      </label>
    {/if}
    <p class="patterns-note">{STEP_TEXT.stepOptInNote}</p>
    {#if thresholdError}<div class="inline-error" role="alert">{thresholdError}</div>{/if}
    <button type="button" class="primary-button patterns-run" bind:this={runButton} disabled={phase.kind === 'running' || query.sourceIds.length === 0} onclick={run}>{phase.kind === 'running' ? PATTERNS_TEXT.running : PATTERNS_TEXT.run}</button>
    <p class="patterns-note">{PATTERNS_TEXT.runNote}</p>
  </section>

  <div aria-live="polite">
    {#if phase.kind === 'error'}<div class="inline-error" role="alert">{phase.text}</div>{/if}
    {#if phase.kind === 'done'}
      {@const report = phase.report}
      {@const coverage = coverageLine(report.coverage)}
      {@const rows = findingRows(report)}
      {@const repeatsLine = repeatsSummary(report)}
      {#if savedAt !== null}
        <div class="notice saved-result" role="status">
          <span>{foundAtText(savedAt, now, zone)}. {DRAWER_RESULTS_TEXT.savedNote}</span>
          <button type="button" class="secondary-button" disabled={query.sourceIds.length === 0} onclick={run}>{DRAWER_RESULTS_TEXT.runAgain}</button>
        </div>
      {/if}
      {#if phase.key !== askKey}<div class="notice" role="status">{PATTERNS_TEXT.stale}</div>{/if}
      <section class="patterns-summary" aria-label="Summary">
        <p class="patterns-verdict">{verdictText(report)}<Hint termId="episode" /></p>
        {#if citeNote}<p class="cite-note" role="status">{citeNote}</p>{/if}
        {#if rows.length}
          <ul class="finding-list">
            {#each rows as finding (finding.key)}
              <li class="finding">
                <div class="finding-meta">
                  <span class="evidence-badge" class:strong={finding.label === 'Strong correlation'}>{finding.label}</span>
                  <span class="finding-n">{nBadge(finding.n)}</span>
                  {#if finding.lowN}<span class="low-n">{PATTERNS_TEXT.lowN}</span>{/if}
                </div>
                <p>{finding.headline}</p>
                {#if finding.evidenceId}
                  <p class="finding-ref" title={PATTERNS_TEXT.referenceNote}><span class="finding-ref-label">{PATTERNS_TEXT.reference}</span> <code>{finding.evidenceId}</code>
                    {#if isEvidenceId(finding.evidenceId)}<button type="button" class="cite-button" aria-label={PATTERNS_TEXT.cite + ': ' + citeLabel(finding.headline)} onclick={() => cite(finding.evidenceId, finding.headline)}>{PATTERNS_TEXT.cite}</button>{/if}
                  </p>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
        {#if repeatsLine}<p class="patterns-quality">{repeatsLine}</p>{/if}
        <p class="patterns-quality">{qualityLine(report.quality)}<Hint termId="incomplete-request" /></p>
        {#if coverage}<p class="patterns-quality">{coverage}<Hint termId="coverage" /></p>{/if}
        {#each truncationNotices(report) as notice (notice)}<div class="notice" role="status">{notice}</div>{/each}
        {#each report.warnings as warning (warning)}<div class="notice" role="status">{warning}</div>{/each}
        <ProvenanceNote provenance={report.provenance} {sourceLabels} />
      </section>

      <section class="patterns-detail" aria-label={PATTERNS_TEXT.episodes}>
        <h3>{PATTERNS_TEXT.episodes}</h3>
        {#if report.episodes.length === 0}
          <div class="empty-state patterns-empty"><strong>{PATTERNS_TEXT.emptyTitle}</strong><p>{emptyEpisodesText(report.params.stallMs)}</p></div>
        {:else}
          <div class="table-scroll">
            <table class="patterns-table">
              <thead><tr><th scope="col">Start</th><th scope="col">End</th><th scope="col" class="num">Requests</th><th scope="col" class="num">Users</th><th scope="col" class="num">Longest</th><th scope="col">Operations</th><th scope="col">Ledger</th></tr></thead>
              <tbody>
                {#each report.episodes as episode (episode.index)}
                  {@const isOpen = openEpisodes.includes(episode.index)}
                  {@const episodeId = episodeEvidenceId(episode.start)}
                  <tr>
                    <th scope="row"><button type="button" class="episode-toggle" aria-expanded={isOpen} aria-controls={'episode-' + episode.index} onclick={() => toggle(episode.index)}>{at(episode.start)}</button></th>
                    <td>{at(episode.end)}</td>
                    <td class="num">{episode.requests}</td>
                    <td class="num">{episode.users}</td>
                    <td class="num">{formatElapsed(episode.maxMs)}</td>
                    <td class="ops">{episode.operations.join(', ')}</td>
                    <td>{#if episodeId}<button type="button" class="cite-button" aria-label={PATTERNS_TEXT.cite + ': episode at ' + at(episode.start)} onclick={() => cite(episodeId, episodeCiteLabel(clock(episode.start)))}>{PATTERNS_TEXT.cite}</button>{/if}</td>
                  </tr>
                  {#if isOpen}
                    {@const volleys = volleysOf(report, episode)}
                    {@const stalls = stallsOf(report, episode)}
                    <tr class="episode-detail" id={'episode-' + episode.index}>
                      <td colspan="7">
                        <h4>{PATTERNS_TEXT.volleys}</h4>
                        {#if volleys.length === 0}<p class="patterns-note">{PATTERNS_TEXT.none}</p>{:else}
                          <table class="patterns-table inner">
                            <thead><tr><th scope="col">User id</th><th scope="col" class="num">Requests</th><th scope="col" class="num">Start spread</th><th scope="col" class="num">End stagger</th><th scope="col">End gaps</th></tr></thead>
                            <tbody>
                              {#each volleys as volley (volley.userId + volley.firstStart)}
                                <tr><td>{volley.userId}</td><td class="num">{volley.requests}</td><td class="num">{volley.startSpreadMs} ms</td><td class="num">{volley.endStaggerMs} ms</td><td>{endGapsText(volley)}</td></tr>
                              {/each}
                            </tbody>
                          </table>
                        {/if}
                        <h4>{PATTERNS_TEXT.stalls}</h4>
                        {#if stalls.length === 0}<p class="patterns-note">{PATTERNS_TEXT.none}</p>{:else}
                          <table class="patterns-table inner">
                            <thead><tr><th scope="col">Operation</th><th scope="col">Started</th><th scope="col" class="num">Observed</th></tr></thead>
                            <tbody>
                              {#each stalls as stall (stall.sourceId + stall.id)}
                                <tr>
                                  <td class="ops"><button type="button" class="record-link" title={PATTERNS_TEXT.openRecord} aria-label={PATTERNS_TEXT.openRecord + ': ' + operationText(stall) + ', started ' + at(stall.initiatedAt)} onclick={() => openRecord(stall.sourceId, stall.id)}>{operationText(stall)}</button>{#if query.sourceIds.length > 1}{' · '}<span class="source-tag">{labelOf(stall.sourceId)}</span>{/if}</td>
                                  <td>{at(stall.initiatedAt)}</td>
                                  <td class="num">{secondsLabel(stall.observedMs)}</td>
                                </tr>
                              {/each}
                            </tbody>
                          </table>
                        {/if}
                        {#if report.repeats}
                          {@const repeats = repeatsOf(report, episode)}
                          <h4>{PATTERNS_TEXT.repeatsTitle}</h4>
                          <p class="patterns-note">{PATTERNS_TEXT.repeatsIntro}</p>
                          {#if repeats.length === 0}<p class="patterns-note">{PATTERNS_TEXT.none}</p>{:else}
                            <ul class="finding-list">
                              {#each repeats as repeat (repeat.key)}
                                <li class="finding">
                                  <div class="finding-meta">
                                    <span class="evidence-badge">{PATTERNS_TEXT.repeatsLabel}</span>
                                    <span class="finding-n">{repeat.kindText}</span>
                                  </div>
                                  <p>{repeat.line}</p>
                                </li>
                              {/each}
                            </ul>
                          {/if}
                        {/if}
                        <StallMinutesPanel episode={{ start: episode.start, end: episode.end }} sourceIds={query.sourceIds} stallMs={report.params.stallMs} {zone} />
                      </td>
                    </tr>
                  {/if}
                {/each}
              </tbody>
            </table>
          </div>
        {/if}
      </section>

      {@const onset = onsetSection(report, query, zoneMinuteShift(zone, Date.parse(query.start)))}
      {#if onset.kind !== 'hidden'}
        <section class="patterns-detail" aria-label={PATTERNS_TEXT.onsetTitle}>
          <h3>{PATTERNS_TEXT.onsetTitle}</h3>
          <p class="patterns-note">{PATTERNS_TEXT.onsetIntro}</p>
          {#if onset.kind === 'note'}
            <p class="patterns-note">{onset.text}</p>
          {:else}
            <ul class="finding-list">
              {#each onset.rows as row (row.key)}
                <li class="finding">
                  <div class="finding-meta">
                    <span class="evidence-badge">{PATTERNS_TEXT.onsetLabel}</span>
                    <span class="finding-n">{nBadge(row.onsets)}</span>
                  </div>
                  <p>{row.line}</p>
                </li>
              {/each}
            </ul>
            <p class="patterns-note">{PATTERNS_TEXT.onsetHint} {onsetZoneNote(zone)}</p>
          {/if}
        </section>
      {/if}

      {@const concentration = concentrationSection(report)}
      {#if concentration.kind !== 'hidden'}
        <section class="patterns-detail" aria-label={PATTERNS_TEXT.concTitle}>
          <h3>{PATTERNS_TEXT.concTitle}<Hint termId="concentration" /></h3>
          <p class="patterns-note">{PATTERNS_TEXT.concIntro}</p>
          {#if concentration.kind === 'note'}
            <p class="patterns-note">{concentration.text}</p>
          {:else}
            <ul class="finding-list">
              {#each concentration.rows as row (row.key)}
                <li class="finding">
                  {#if row.label}
                    <div class="finding-meta">
                      <span class="evidence-badge">{row.label}</span>
                      <span class="finding-n">{nBadge(row.stallN)}</span>
                      <span class="finding-n">{row.episodesText}</span>
                    </div>
                  {/if}
                  <p>{row.line}</p>
                </li>
              {/each}
            </ul>
          {/if}
        </section>
      {/if}

      {@const idle = idleSection(report, phase.idleAsked)}
      {#if idle.kind !== 'hidden'}
        <section class="patterns-detail" aria-label={PATTERNS_TEXT.idleTitle}>
          <h3>{PATTERNS_TEXT.idleTitle}</h3>
          <p class="patterns-note">{PATTERNS_TEXT.idleIntro}</p>
          {#if idle.kind === 'note'}
            <p class="patterns-note">{idle.text}</p>
          {:else}
            <div class="finding-meta idle-meta">
              <span class="evidence-badge">{PATTERNS_TEXT.idleLabel}</span>
              {#if idle.raised}<span class="finding-n">{PATTERNS_TEXT.idleRaised}</span>{/if}
            </div>
            <div class="table-scroll">
              <table class="patterns-table">
                <caption class="sr-only">{PATTERNS_TEXT.idleTitle}</caption>
                <thead><tr><th scope="col">Quiet gap before</th><th scope="col" class="num">Requests</th><th scope="col" class="num">Median</th><th scope="col" class="num">Slowest 1 in 20</th><th scope="col" class="num">Slow</th></tr></thead>
                <tbody>
                  {#each idle.bands as band (band.key)}
                    <tr><th scope="row">{band.label}</th><td class="num">{band.n}</td><td class="num">{band.median}</td><td class="num">{band.slowest}</td><td class="num">{band.slowRate}</td></tr>
                  {/each}
                </tbody>
              </table>
            </div>
            <ul class="idle-lifts">
              {#each idle.lifts as lift (lift.key)}<li>{lift.line}</li>{/each}
            </ul>
            {#if idle.partial}<div class="notice" role="status">{idle.partial}</div>{/if}
            {#if idle.excluded}<p class="patterns-note">{idle.excluded}</p>{/if}
            <p class="patterns-note">{PATTERNS_TEXT.idleCaveat}</p>
            <p class="patterns-note">{PATTERNS_TEXT.idleSources}</p>
          {/if}
        </section>
      {/if}

      {@const step = stepSection(report, phase.stepAsked)}
      {#if step.kind !== 'hidden'}
        <section class="patterns-detail" aria-label={STEP_TEXT.stepTitle}>
          <h3>{STEP_TEXT.stepTitle}</h3>
          <p class="patterns-note">{STEP_TEXT.stepIntro}</p>
          {#if step.kind === 'note'}
            <p class="patterns-note">{step.text}</p>
          {:else}
            <div class="table-scroll">
              <table class="patterns-table">
                <caption class="sr-only">{STEP_TEXT.stepTitle}</caption>
                <thead><tr><th scope="col">{STEP_TEXT.stepColOperation}</th><th scope="col" class="num">{STEP_TEXT.stepColDays}</th><th scope="col" class="num">{STEP_TEXT.stepColBefore}</th><th scope="col" class="num">{STEP_TEXT.stepColAfter}</th><th scope="col" class="num">{STEP_TEXT.stepColChange}</th><th scope="col">{STEP_TEXT.stepColResult}</th></tr></thead>
                <tbody>
                  {#each step.rows as row (row.key)}
                    <tr>
                      <th scope="row" class="ops">{row.operation}</th>
                      <td class="num">{row.days}</td>
                      <td class="num">{row.before}</td>
                      <td class="num">{row.after}</td>
                      <td class="num">{row.change}</td>
                      <td>{row.result}{#if row.date}{' · '}{row.date}{/if}{#if row.reason}<span class="step-reason">{row.reason}</span>{/if}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
            <p class="patterns-note">{step.daysLine}</p>
            {#if step.split && step.scope}
              {@const patch = step.scope}
              <div class="split-offer" bind:this={splitBox}>
                <h4>{STEP_TEXT.splitTitle}</h4>
                <p class="patterns-note">{STEP_TEXT.splitOffer}: {STEP_TEXT.splitBefore} {at(step.split.baselineStart)} to {at(step.split.baselineEnd)}. {STEP_TEXT.splitAfter} {at(step.split.start)} to {at(step.split.end)}. {STEP_TEXT.splitNote}</p>
                {#if splitAsking}
                  <p class="patterns-note" role="status">{STEP_TEXT.splitConfirm}</p>
                  <div class="split-actions">
                    <button type="button" class="primary-button" onclick={() => applySplit(patch)}>{STEP_TEXT.splitUse}</button>
                    <button type="button" class="secondary-button" onclick={() => moveSplit(false)}>{STEP_TEXT.splitCancel}</button>
                  </div>
                {:else}
                  <button type="button" class="secondary-button" onclick={() => moveSplit(true)}>{STEP_TEXT.splitAsk}</button>
                {/if}
                {#if splitNote}<p class="patterns-note" role="status">{splitNote}</p>{/if}
              </div>
            {/if}
            <p class="patterns-note">{STEP_TEXT.stepCaveat}</p>
          {/if}
        </section>
      {/if}

      {@const series = auditSeriesSection(report, phase.auditAsked)}
      {#if series.kind !== 'hidden'}
        <section class="patterns-detail" aria-label={STEP_TEXT.seriesTitle}>
          <h3>{STEP_TEXT.seriesTitle}</h3>
          <p class="patterns-note">{STEP_TEXT.seriesIntro}</p>
          {#if series.kind === 'note'}
            <p class="patterns-note">{series.text}</p>
          {:else}
            <div class="table-scroll">
              <table class="patterns-table">
                <caption class="sr-only">{STEP_TEXT.seriesTitle}</caption>
                <thead><tr><th scope="col">{STEP_TEXT.seriesColDay}</th><th scope="col" class="num">{STEP_TEXT.seriesColRequests}</th><th scope="col" class="num">{STEP_TEXT.seriesColMedian}</th><th scope="col" class="num">{STEP_TEXT.seriesColSlowest}</th><th scope="col" class="num">{STEP_TEXT.seriesColShare}</th></tr></thead>
                <tbody>
                  {#each series.days as day (day.key)}
                    <tr><th scope="row">{day.date}{#if day.partDay}{' '}({STEP_TEXT.seriesPartDay}){/if}</th><td class="num">{day.n}</td><td class="num">{day.median}</td><td class="num">{day.slowest}</td><td class="num">{day.share}</td></tr>
                  {/each}
                </tbody>
              </table>
            </div>
            <h4>{STEP_TEXT.bandTitle}</h4>
            <p class="patterns-note">{series.band}</p>
            {#if series.partial}<div class="notice" role="status">{series.partial}</div>{/if}
            {#if series.excluded}<p class="patterns-note">{series.excluded}</p>{/if}
            {#if series.without}<p class="patterns-note">{series.without}</p>{/if}
            <p class="patterns-note">{STEP_TEXT.seriesShareNote}</p>
            <p class="patterns-note">{STEP_TEXT.seriesNote}</p>
          {/if}
        </section>
      {/if}

      {#if report.auditStalls.length}
        <section class="patterns-detail" aria-label={PATTERNS_TEXT.auditStalls}>
          <h3>{PATTERNS_TEXT.auditStalls}</h3>
          <ul class="audit-list">
            {#each report.auditStalls as stall (stall.sourceId + stall.id)}
              <li>
                <span class="ops"><button type="button" class="record-link" title={PATTERNS_TEXT.openRecord} aria-label={PATTERNS_TEXT.openRecord + ': ' + operationText(stall) + ', started ' + at(stall.initiatedAt)} onclick={() => openRecord(stall.sourceId, stall.id)}>{operationText(stall)}</button>{#if query.sourceIds.length > 1}{' · '}<span class="source-tag">{labelOf(stall.sourceId)}</span>{/if}</span>
                <span class="audit-when">{at(stall.initiatedAt)}</span>
                <span class="audit-line">{auditStallLine(stall)}</span>
              </li>
            {/each}
          </ul>
        </section>
      {/if}
    {/if}
  </div>
</Drawer>

<style>
  :global(.connections-drawer.patterns-drawer) { overflow-x: hidden; gap: var(--space-3); }
  .patterns-window { display: grid; gap: 10px; margin-top: 13px; min-width: 0; }
  .range-row { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
  .range-label { margin-right: var(--space-1); color: var(--text-2); font-size: var(--fs-2); }
  .patterns-window dl { display: grid; gap: 4px; margin: 0; font-size: var(--fs-3); }
  .patterns-window dl div { display: flex; gap: 10px; min-width: 0; }
  .patterns-window dt { flex: 0 0 74px; color: var(--text-2); }
  .patterns-window dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
  .patterns-thresholds summary { width: fit-content; color: var(--text-2); font-size: var(--fs-3); }
  .threshold-fields { display: grid; grid-template-columns: repeat(auto-fit, minmax(170px, 1fr)); gap: 10px; margin-top: 8px; }
  .threshold-fields label { display: grid; gap: 4px; color: var(--text-2); font-size: var(--fs-2); }
  .threshold-fields input { min-width: 0; min-height: 36px; padding: 6px 10px; color: var(--text); background: var(--bg); border: 1px solid var(--control-line); border-radius: var(--radius); font-size: var(--fs-4); font-variant-numeric: tabular-nums; }
  .patterns-thresholds .patterns-note { margin-top: var(--space-2); }
  .reset-link { margin-top: var(--space-1); padding: 3px 0; color: var(--text-2); background: none; border: 0; font-size: var(--fs-2); text-decoration: underline; text-underline-offset: 3px; cursor: pointer; }
  .reset-link:hover { color: var(--text); }
  .reset-link:focus-visible, .cite-button:focus-visible, .record-link:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius); }
  .patterns-run { justify-self: start; }
  .saved-result { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px; margin-bottom: 10px; }
  .saved-result button { margin-left: 0; }
  .idle-option { display: flex; align-items: center; gap: 8px; color: var(--text-2); font-size: var(--fs-3); cursor: pointer; }
  .idle-option input { width: 16px; height: 16px; }
  .idle-meta { margin-bottom: 8px; }
  .step-days { display: flex; align-items: center; gap: 8px; margin-left: 24px; color: var(--text-2); font-size: var(--fs-3); }
  .step-days select { min-height: 32px; padding: 4px 8px; color: var(--text); background: var(--bg); border: 1px solid var(--control-line); border-radius: var(--radius); font-size: var(--fs-3); }
  .step-reason { display: block; color: var(--text-2); font-size: var(--fs-1); }
  .split-offer { display: grid; gap: 6px; margin: 10px 0; padding: 9px 12px; background: var(--surface-2); border: 1px solid var(--line-soft); border-radius: var(--radius); justify-items: start; }
  .split-offer h4 { margin: 0; }
  .split-actions { display: flex; flex-wrap: wrap; gap: 8px; }
  .idle-lifts { display: grid; gap: 4px; margin: 8px 0; padding-left: 18px; font-size: var(--fs-3); }
  .sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
  .patterns-note { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; }
  .patterns-summary { display: grid; gap: 10px; min-width: 0; }
  .patterns-verdict { margin: 0; font-size: var(--fs-5); font-weight: 600; }
  .cite-note { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
  .finding-list { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
  .finding { padding: 9px 12px; background: var(--surface-2); border: 1px solid var(--line-soft); border-radius: var(--radius); }
  .finding p { margin: 6px 0 0; font-size: var(--fs-3); overflow-wrap: anywhere; }
  .finding-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 6px 10px; font-size: var(--fs-2); }
  .evidence-badge { padding: 2px 8px; color: var(--text); border: 1px solid var(--line); border-radius: 4px; font-weight: 650; }
  .evidence-badge.strong { border-color: var(--accent); }
  .finding-n { color: var(--text-2); font-variant-numeric: tabular-nums; }
  .finding p.finding-ref { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 8px; margin-top: 4px; color: var(--text-2); font-size: var(--fs-1); }
  .finding-ref code { font-family: var(--mono); user-select: all; overflow-wrap: anywhere; }
  .cite-button { padding: 3px 0; color: var(--text-2); background: none; border: 0; font-size: var(--fs-2); text-decoration: underline; text-underline-offset: 3px; cursor: pointer; white-space: nowrap; }
  .cite-button:hover, .record-link:hover { color: var(--text); }
  .record-link { padding: 0; color: var(--text); background: none; border: 0; font: inherit; text-align: left; text-decoration: underline; text-underline-offset: 3px; cursor: pointer; overflow-wrap: anywhere; }
  .low-n { padding: 1px 6px; color: var(--warn); border: 1px solid var(--warn); border-radius: 4px; }
  .patterns-quality { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; }
  .patterns-detail { margin-top: var(--space-4); min-width: 0; }
  .patterns-detail h3 { margin: 0 0 8px; font-size: var(--fs-4); font-weight: 600; }
  .patterns-detail h4 { margin: 10px 0 6px; color: var(--text-2); font-size: var(--fs-2); font-weight: 650; }
  .patterns-empty { padding: var(--space-4); }
  .table-scroll { overflow-x: auto; border: 1px solid var(--line-soft); border-radius: var(--radius); }
  .patterns-table { width: 100%; border-collapse: collapse; font-size: var(--fs-2); }
  .patterns-table th, .patterns-table td { padding: 5px 8px; text-align: left; vertical-align: top; border-bottom: 1px solid var(--line-soft); font-weight: 400; }
  .patterns-table thead th { background: var(--surface-2); color: var(--text-2); font-weight: 600; white-space: nowrap; }
  .patterns-table .num { text-align: right; font-variant-numeric: tabular-nums; white-space: nowrap; }
  .patterns-table .ops { overflow-wrap: anywhere; }
  .patterns-table.inner { margin-bottom: 4px; }
  .episode-detail > td { background: var(--surface-2); }
  .episode-toggle { padding: 0; color: var(--text); background: none; border: 0; font: inherit; font-weight: 600; text-align: left; text-decoration: underline; text-underline-offset: 3px; cursor: pointer; }
  .episode-toggle:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius); }
  .audit-list { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
  .audit-list li { display: grid; justify-items: start; gap: 2px; padding: 8px 12px; background: var(--surface-2); border: 1px solid var(--line-soft); border-radius: var(--radius); font-size: var(--fs-3); }
  .audit-when { color: var(--text-2); font-size: var(--fs-2); }
  .source-tag { color: var(--text-2); font-size: var(--fs-1); }
</style>
