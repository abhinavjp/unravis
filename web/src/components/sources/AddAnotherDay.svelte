<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchLaneDays, groupSources, readableByDay, shiftDays, type LaneDay, type LaneRunResult, type LaneSource } from '../../lib/lanes';
  import { DAY_TEXT, HUB_TEXT, NUTSHELL } from '../../lib/copy/sourcesCopy';
  import LaneRunner from './LaneRunner.svelte';
  import StateSticker from './StateSticker.svelte';
  import './sources.css';

  // Screen 113: read another day of a SQL Server the case already holds. Only SQL can be read by day; MongoDB keeps just the latest profiler
  // entries, so it is listed as "Not by day" with the reason. The new range starts as the same hours on the next day. The days already held
  // are never replaced: a day is added next to them.
  let { caseId, readOnly = false, onGone, onDone, onCancel }: { caseId: string; readOnly?: boolean; onGone?: () => void; onDone?: () => void; onCancel?: () => void } = $props();

  let days = $state.raw<LaneDay[]>([]);
  let loaded = $state(false);
  let pickKey = $state('');
  let added = $state(false);

  const sources = $derived<LaneSource[]>(groupSources(days));
  const byDay = $derived(sources.filter(s => readableByDay(s.kind)));
  const picked = $derived(byDay.find(s => s.key === pickKey) ?? byDay[0] ?? null);
  const latest = $derived(picked ? picked.days[0] : null);
  const preset = $derived(latest ? shiftDays(latest.incident, 1) : null);
  const range = (w: { start: string; end: string }) => `${w.start.slice(0, 16).replace('T', ' ')} to ${w.end.slice(0, 16).replace('T', ' ')}`;

  async function load() {
    try { days = await fetchLaneDays(caseId); } catch { /* the page then says nothing is held */ }
    loaded = true;
  }
  onMount(() => { void load(); });
  async function ran(result: LaneRunResult) {
    added = result.saved;
    await load();
    if (result.saved) onDone?.();
  }
</script>

<section class="src" aria-labelledby="day-head" data-add-day>
  <div class="nutshell"><b>{NUTSHELL}</b><p>{DAY_TEXT.lead}</p></div>
  <h3 id="day-head">{DAY_TEXT.heading}</h3>

  {#if loaded && byDay.length === 0}
    <div class="empty"><p>{DAY_TEXT.nothingHeld}</p><p class="hint">{DAY_TEXT.onlyByDay}</p></div>
  {:else}
    <div class="field">
      <h4>{DAY_TEXT.holds}</h4>
      <ul class="rows">
        {#each sources as source (source.key)}
          <li data-source={source.key}>
            <span class="what">
              <span class="name">{source.label}{source.database ? ' · ' + source.database : ''}</span>
              {#if readableByDay(source.kind)}
                {#each source.days as day (day.id)}<span class="hint">{DAY_TEXT.inCase}: {range(day.incident)}</span>{/each}
              {:else}
                <span class="hint">{DAY_TEXT.notByDay}. {DAY_TEXT.profilerWhy}</span>
              {/if}
            </span>
            <StateSticker word={source.days[0]?.state ?? 'Cannot test yet'} />
          </li>
        {/each}
      </ul>
    </div>
    {#if byDay.length > 1}
      <div class="field">
        <label for="day-pick">{DAY_TEXT.pickSource}</label>
        <select id="day-pick" value={picked?.key ?? ''} onchange={(e) => { pickKey = e.currentTarget.value; }}>
          {#each byDay as source (source.key)}<option value={source.key}>{source.label}{source.database ? ' · ' + source.database : ''}</option>{/each}
        </select>
      </div>
    {/if}
    {#if readOnly}
      <p class="hint" data-read-only>{HUB_TEXT.readOnlyWhy}</p>
    {:else if picked && latest && preset}
      {#key picked.key + preset.start}
        <LaneRunner {caseId} kind="sql" {days} database={picked.database} lockDatabase incident={preset} reference={latest.reference}
          connectionLabel={picked.label} runLabel={DAY_TEXT.run} onRan={ran} {onGone} />
      {/key}
      <p class="hint">{DAY_TEXT.prefill}</p>
      <p class="hint">{DAY_TEXT.expects}</p>
      <p class="hint">{DAY_TEXT.emptyDay}</p>
      <p class="hint">{DAY_TEXT.rerun}</p>
    {/if}
    {#if added}<p class="say good" role="status">{DAY_TEXT.added}</p>{/if}
  {/if}
  {#if onCancel}<div class="actions"><button type="button" class="btn" onclick={() => onCancel?.()}>{DAY_TEXT.cancel}</button></div>{/if}
</section>
