<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchLaneDays, groupSources, newestDay, readableByDay, runNotice, shiftDays, type LaneDay, type LaneRunResult, type LaneSource } from '../../lib/lanes';
  import { runProblem } from '../../lib/lanes';
  import { HUB_TEXT, NUTSHELL, STEP_TEXT } from '../../lib/copy/sourcesCopy';
  import { formatTime } from '../../lib/time';
  import { timeZone } from '../../lib/timeZone.svelte';
  import GrantScriptPanel from './GrantScriptPanel.svelte';
  import LaneResultView from './LaneResultView.svelte';
  import LaneRunner from './LaneRunner.svelte';
  import ProfilerGuide from './ProfilerGuide.svelte';
  import StateSticker from './StateSticker.svelte';
  import './sources.css';

  // Step 7 (SQL Server) and Step 8 (MongoDB). The days already read for this case are listed by source, newest first; one is open at a time.
  // Nothing is read until "Read this day" is pressed. With no source of this kind the step says Cannot test yet and offers to add one: it never
  // reads as a pass. For Step 8 the audit logs alone do not count: the step needs the server itself, its profiler and its health.
  // readOnly: the case cannot take a new day (closed, or changed elsewhere). Nothing that reads a server or adds data is offered; what is held stays readable.
  let { caseId, kind, readOnly = false, onGone, onChanged, onAddData }: {
    caseId: string; kind: 'sql' | 'mongo'; readOnly?: boolean; onGone?: () => void; onChanged?: () => void; onAddData?: () => void;
  } = $props();

  let days = $state.raw<LaneDay[]>([]);
  let loaded = $state(false);
  let loadFailed = $state(false);
  let openId = $state(0);
  let adding = $state(false);
  // What the last read did, kept here (not in the runner) so it stays on screen after the runner is gone. An unsaved day is shown from memory only.
  let notice = $state<{ good: boolean; text: string } | null>(null);
  let unsaved = $state.raw<LaneDay | null>(null);
  let controller: AbortController | null = null;

  const mine = $derived(days.filter(d => d.kind === kind));
  const sources = $derived<LaneSource[]>(groupSources(mine));
  const open = $derived(unsaved ?? mine.find(d => d.id === openId) ?? newestDay(mine));
  const title = $derived(kind === 'sql' ? STEP_TEXT.sqlHeading : STEP_TEXT.mongoHeading);
  const missing = $derived(open?.lane?.checks.some(c => c.fix !== null) ?? false);
  const preset = $derived(open ? { incident: shiftDays(open.incident, readableByDay(kind) ? 1 : 0), reference: open.reference } : null);

  async function load() {
    controller?.abort();
    const signal = (controller = new AbortController()).signal;
    loadFailed = false;
    try {
      const list = await fetchLaneDays(caseId, signal);
      if (signal.aborted) return;
      days = list;
    } catch (error) {
      if (signal.aborted) return;
      if (runProblem(error).gone) onGone?.();
      loadFailed = true;
    }
    loaded = true;
  }
  onMount(() => { void load(); return () => controller?.abort(); });

  async function ran(result: LaneRunResult) {
    adding = false;
    notice = runNotice(result);
    unsaved = result.saved ? null : result.day;
    if (result.saved) openId = result.day.id;
    await load();
    if (result.saved && result.day.id !== 0) openId = result.day.id;
    onChanged?.();
  }
  const when = (iso: string) => formatTime(iso, { zone: timeZone.zone, precision: 'minute', year: true, fallback: iso });
</script>

<section class="src" aria-labelledby="step-{kind}-head" data-step={kind}>
  <div class="nutshell"><b>{NUTSHELL}</b><p>{kind === 'sql' ? STEP_TEXT.sqlLead : STEP_TEXT.mongoLead}</p></div>
  <h3 id="step-{kind}-head">{title}</h3>
  <div aria-live="polite">
    {#if notice}<p class="say {notice.good ? 'good' : 'bad'}" role={notice.good ? 'status' : 'alert'} data-notice>{notice.text}</p>{/if}
  </div>

  {#if loadFailed}
    <div class="empty"><p class="say bad" role="alert">{STEP_TEXT.loadFailed}</p><div class="actions"><button type="button" class="btn" onclick={() => void load()}>{STEP_TEXT.retry}</button></div></div>
  {:else if !loaded}
    <p class="hint">{STEP_TEXT.loading}</p>
  {:else if mine.length === 0}
    <div class="empty" data-no-source>
      <div class="actions"><StateSticker word="Cannot test yet" /></div>
      <p>{kind === 'sql' ? STEP_TEXT.noSql : STEP_TEXT.noMongo}</p>
      {#if kind === 'mongo'}<p class="hint">{STEP_TEXT.noMongoWhy}</p>{/if}
    </div>
    <GrantScriptPanel {kind} />
    {#if kind === 'mongo'}<ProfilerGuide database="" guide={null} />{/if}
    {#if readOnly}<p class="hint" data-read-only>{HUB_TEXT.readOnlyWhy}</p>{:else}
      <LaneRunner {caseId} {kind} {days} onRan={ran} {onGone} ownNotice />
    {/if}
  {:else}
    <section class="field" aria-label={STEP_TEXT.daysHeading}>
      <h4>{STEP_TEXT.daysHeading}</h4>
      {#each sources as source (source.key)}
        <div class="field" data-source={source.key}>
          <h5>{source.label}{source.database ? ' · ' + source.database : ''}</h5>
          <ul class="rows">
            {#each source.days as day (day.id)}
              <li>
                <span class="what"><span class="name">{day.incident.start.slice(0, 16).replace('T', ' ')} UTC</span><span class="hint">{STEP_TEXT.readAt} {when(day.at)}</span></span>
                <StateSticker word={day.state} />
                <button type="button" class="btn" aria-pressed={open?.id === day.id} onclick={() => { openId = day.id; unsaved = null; }}>{open?.id === day.id ? STEP_TEXT.shown : STEP_TEXT.show}</button>
              </li>
            {/each}
          </ul>
        </div>
      {/each}
    </section>

    {#if open}
      <LaneResultView day={open} onAddDay={readableByDay(kind) && !readOnly ? () => { adding = true; } : undefined} onAddData={readOnly ? undefined : onAddData} />
      {#if missing}
        <GrantScriptPanel kind={kind} version={open.lane?.era === '2019' ? '2019' : '2022'} database={open.database} />
      {/if}
      {#if kind === 'mongo'}<ProfilerGuide database={open.database} guide={open.lane?.guide ?? null} />{/if}
    {/if}

    {#if readOnly}
      <p class="hint" data-read-only>{HUB_TEXT.readOnlyWhy}</p>
    {:else if adding || kind === 'mongo'}
      <LaneRunner {caseId} {kind} {days} database={open?.database ?? ''} incident={preset?.incident ?? null} reference={preset?.reference ?? null}
        connectionLabel={open?.label ?? ''} onRan={ran} {onGone} ownNotice />
    {:else}
      <div class="actions"><button type="button" class="btn" onclick={() => { adding = true; }}>{STEP_TEXT.addSql}</button></div>
    {/if}
  {/if}
</section>
