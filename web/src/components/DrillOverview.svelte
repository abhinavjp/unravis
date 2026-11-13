<script lang="ts">
  import { DRILL_HELP, DRILL_TEXT } from '../lib/drill/drillCopy';
  import { callFacts, formatMs, pressureLines, whereTimeWent, type FactKey, type PartKey } from '../lib/drill/overview';
  import type { DrillRecord } from '../lib/drill/perf';
  import { timeZone } from '../lib/timeZone.svelte';

  // The Overview tab of the drill pop-up. Every section has a title and an always-visible hint.
  let { record }: { record: DrillRecord } = $props();

  const perf = $derived(record.perf);
  const facts = $derived(callFacts(record, timeZone.zone));
  const where = $derived(whereTimeWent(perf, record.durationMs));
  const pressure = $derived(pressureLines(perf));

  const partLabel: Record<PartKey, string> = {
    filters: DRILL_TEXT.partFilters,
    other: DRILL_TEXT.partOther
  };
  const partHelp: Record<PartKey, string> = {
    filters: DRILL_HELP.partFilters,
    other: DRILL_HELP.partOther
  };

  const factLabel: Record<FactKey, string> = {
    endpoint: DRILL_TEXT.factEndpoint,
    status: DRILL_TEXT.factStatus,
    started: DRILL_TEXT.factStarted,
    duration: DRILL_TEXT.factDuration
  };
  const factHelp: Record<FactKey, string> = {
    endpoint: DRILL_HELP.factEndpoint,
    status: DRILL_HELP.factStatus,
    started: DRILL_HELP.factStarted,
    duration: DRILL_HELP.factDuration
  };

  function fmtMs(ms: number | null): string {
    return ms === null ? DRILL_TEXT.notRecorded : formatMs(ms);
  }

  // True when a known part (not "other") has no value, so the "other" hint applies.
  const someKnownMissing = $derived(
    where !== null && where.parts.some((part) => part.key !== 'other' && part.ms === null)
  );
</script>

<section class="block" aria-labelledby="ov-facts" data-testid="drill-facts">
  <h3 id="ov-facts">{DRILL_TEXT.factsTitle}</h3>
  <p class="hint">{DRILL_HELP.factsTitle}</p>
  <ul class="rows">
    {#each facts as fact (fact.key)}
      <li class="row" data-testid="drill-fact-{fact.key}">
        <span class="label" title={factHelp[fact.key]}>{factLabel[fact.key]}</span>
        {#if fact.value === null}
          <span class="value muted" title={DRILL_HELP.notRecorded}>{DRILL_TEXT.notRecorded}</span>
        {:else}
          <span class="value">{fact.value}</span>
        {/if}
      </li>
    {/each}
  </ul>
</section>

{#if perf === null}
  <section class="block" data-testid="drill-no-timings">
    <p>{DRILL_TEXT.noTimings}</p>
    <p class="hint">{DRILL_HELP.noTimings}</p>
  </section>
{:else if perf && where}
  <section class="block" aria-labelledby="ov-where">
    <h3 id="ov-where">{DRILL_TEXT.whereTimeWent}</h3>
    <p class="hint">{DRILL_HELP.whereTimeWent}</p>
    <div class="bar" role="img" aria-label={DRILL_TEXT.barLabel} data-testid="drill-bar" title={DRILL_HELP.bar}>
      {#each where.parts as part (part.key)}
        {#if part.ms !== null && part.share !== null && part.share > 0}
          <span class="seg seg-{part.key}" style="width: {Math.min(100, Math.max(0, part.share))}%" data-testid="drill-bar-{part.key}"></span>
        {/if}
      {/each}
    </div>
    <ul class="rows">
      {#each where.parts as part (part.key)}
        <li class="row">
          <span class="swatch swatch-{part.key}" aria-hidden="true"></span>
          <span class="label" title={partHelp[part.key]}>{partLabel[part.key]}</span>
          {#if part.ms === null && part.key === 'other'}
            <span class="value muted" title={DRILL_HELP.otherUnknown}>{DRILL_TEXT.otherUnknown}</span>
          {:else if part.ms === null}
            <span class="value muted" title={DRILL_HELP.notRecorded}>{DRILL_TEXT.notRecorded}</span>
          {:else}
            <span class="value">{fmtMs(part.ms)}{part.share !== null ? ` (${part.share}%)` : ''}</span>
          {/if}
        </li>
      {/each}
    </ul>
    {#if someKnownMissing}
      <p class="hint">{DRILL_HELP.partOther}</p>
    {/if}
  </section>

  {#if pressure.length > 0}
    <section class="block" aria-labelledby="ov-pressure">
      <h3 id="ov-pressure">{DRILL_TEXT.pressureTitle}</h3>
      <p class="hint">{DRILL_HELP.pressureTitle}</p>
      <ul class="rows">
        {#each pressure as line, i (i)}
          <li class="row">
            <span class="label">{line.name}</span>
            <span class="value" class:muted={line.value === null}>{line.value === null ? DRILL_TEXT.notRecorded : line.value}</span>
          </li>
        {/each}
      </ul>
    </section>
  {/if}

  {#if perf.steps.length > 0}
    <section class="block" aria-labelledby="ov-steps">
      <h3 id="ov-steps">{DRILL_TEXT.stepsTitle}</h3>
      <p class="hint">{DRILL_HELP.stepsTitle}</p>
      <ul class="rows">
        {#each perf.steps as step, i (i)}
          <li class="row">
            <span class="label">{step.name}</span>
            <span class="value" class:muted={step.ms === null}>{fmtMs(step.ms)}</span>
          </li>
        {/each}
      </ul>
    </section>
  {/if}

  {#if perf.filters.length > 0}
    <section class="block" aria-labelledby="ov-filters">
      <h3 id="ov-filters">{DRILL_TEXT.filtersTitle}</h3>
      <p class="hint">{DRILL_HELP.filtersTitle}</p>
      <ul class="rows">
        {#each perf.filters as filter, i (i)}
          <li class="row">
            <span class="label">{filter.name}</span>
            <span class="value" class:muted={filter.ms === null}>{fmtMs(filter.ms)}</span>
          </li>
        {/each}
      </ul>
    </section>
  {/if}

  {#if perf.hitCount !== null}
    <section class="block">
      <p>{DRILL_TEXT.hitCount(perf.hitCount)}</p>
      <p class="hint">{DRILL_HELP.hitCount}</p>
    </section>
  {/if}
{/if}

<style>
  .block { display: grid; gap: 8px; margin: 0 0 18px; }
  .block h3 { margin: 0; font-size: 14px; font-weight: 600; color: var(--text); }
  .block p { margin: 0; font-size: 13px; color: var(--text); }
  .hint { color: var(--text-2); font-size: 12px; line-height: 1.45; }
  .muted { color: var(--text-2); }
  .rows { display: grid; gap: 6px; margin: 0; padding: 0; list-style: none; }
  .row { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 12px; min-width: 0; font-size: 13px; }
  .label { flex: 1 1 140px; min-width: 0; overflow-wrap: anywhere; color: var(--text); }
  .value { flex: 0 1 auto; color: var(--text); font-variant-numeric: tabular-nums; overflow-wrap: anywhere; }
  .bar { display: flex; width: 100%; height: 14px; background: var(--surface-2); border: 1px solid var(--line); border-radius: 3px; overflow: hidden; }
  .seg { display: block; height: 100%; min-width: 2px; }
  .seg-filters, .swatch-filters { background: var(--accent); }
  .seg-other, .swatch-other { background: var(--text-2); }
  .swatch { flex: 0 0 auto; width: 10px; height: 10px; border-radius: 2px; }
</style>
