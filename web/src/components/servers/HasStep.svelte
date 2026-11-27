<script lang="ts">
  import type { ServersFlow } from '../../lib/serversFlow.svelte';
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';
  import { caseRangeProblem, failureText, formatBytes, formatUtc, pickedTotals, planSummary, rangeChoices, rangeLabel, type RangeChoice } from '../../lib/servers';

  // Step 5. The file list of each folder that could be read, for a time range. Nothing is copied here. The case's own windows are
  // shown read only; changing them is done in the Investigation timeline.
  let { flow, inCase, onChangeWindows }: { flow: ServersFlow; inCase: boolean; onChangeWindows: () => void } = $props();

  const range = $derived(flow.caseRange());
  const choices = $derived(rangeChoices(range));
  const problem = $derived(caseRangeProblem(range));
  const totals = $derived(pickedTotals(flow.chosenKeys.map((k) => flow.rows[k]?.plan ?? null)));
  const loading = $derived(flow.targets.some((t) => flow.rows[t.key]?.state === 'loading'));

  const asRange = (value: string) => value as RangeChoice;
  const dayTime = (ms: number) => formatUtc(ms);
</script>

<section class="sv-block" aria-labelledby="sv-has-title">
  <h3 id="sv-has-title">{T.hasTitle}</h3>
  <p class="sv-lead">{T.hasLead}</p>

  <div class="sv-case">
    <h4>{T.hasCaseHeading}</h4>
    {#if range.state === 'ok'}
      <dl class="sv-facts">
        <div><dt>{T.hasCaseIncident}</dt><dd>{dayTime(range.incident.start)} to {dayTime(range.incident.end)}</dd></div>
        <div><dt>{T.hasCaseComparison}</dt><dd>{dayTime(range.comparison.start)} to {dayTime(range.comparison.end)}</dd></div>
      </dl>
      <p class="sv-note">{T.hasCaseSettings}</p>
    {:else}
      <p class="sv-note">{problem}</p>
    {/if}
    {#if inCase}
      <div class="sv-actions"><button type="button" class="secondary-button" onclick={onChangeWindows}>{T.hasCaseChange}</button></div>
    {:else}
      <p class="sv-note">{T.hasCaseOutside}</p>
    {/if}
  </div>

  {#if flow.targets.length > 1}
    <label class="sv-field sv-inline"><span>{T.hasRangeAll}</span>
      <select disabled={loading} onchange={(e) => { const v = e.currentTarget.value; e.currentTarget.value = ''; if (v) void flow.setAllRanges(asRange(v)); }}>
        <option value="">-</option>
        {#each choices as c (c)}<option value={c}>{rangeLabel(c)}</option>{/each}
      </select></label>
  {/if}

  <ul class="sv-list" aria-label={T.hasTitle}>
    {#each flow.targets as t (t.key)}
      {@const row = flow.rows[t.key]}
      {#if row}
        {@const sum = row.plan ? planSummary(row.plan) : null}
        <li class="sv-row sv-item">
          <label class="sv-check">
            <input type="checkbox" bind:checked={row.on} disabled={row.state !== 'ok' || !sum || sum.empty || !!sum.problem} />
            <strong>{t.label}</strong>
            <span class="sv-note">{t.machine}</span>
          </label>
          <code class="sv-dir">{t.dir}</code>
          <label class="sv-field sv-inline"><span class="sv-note">{T.hasRange}</span>
            <select value={row.range} disabled={row.state === 'loading'} onchange={(e) => flow.setRange(t.key, asRange(e.currentTarget.value))}>
              {#each choices as c (c)}<option value={c}>{rangeLabel(c)}</option>{/each}
              {#if !choices.includes(row.range)}<option value={row.range}>{rangeLabel(row.range)}</option>{/if}
            </select></label>
          {#if row.state === 'loading'}
            <p class="sv-note" role="status">{T.hasPlanning}</p>
          {:else if row.state === 'failed' && row.failure}
            <p class="sv-bad" role="alert">{failureText(row.failure)} <button type="button" class="quiet-button" onclick={() => flow.planRow(t.key)}>{T.hasReplan}</button></p>
          {:else if sum}
            {#if sum.problem}
              <p class="sv-bad">{sum.problem}</p>
            {:else if sum.empty}
              <p class="sv-note">{T.hasEmpty}</p>
            {:else}
              <p class="sv-line">{T.hasFiles(sum.files, formatBytes(sum.bytes))}{#if sum.oldest} <span class="sv-note">{T.hasOldest} {sum.oldest}. {T.hasNewest} {sum.newest}.</span>{/if}</p>
            {/if}
            {#if sum.skipped > 0}<p class="sv-note">{T.hasSkipped(sum.skipped)} {row.plan?.skipped.slice(0, 3).map((s) => s.name + ': ' + s.reason).join('; ')}</p>{/if}
          {/if}
        </li>
      {/if}
    {/each}
  </ul>

  <p class="sv-note">{T.hasLimit}</p>
  <p class="sv-line" role="status">{T.hasTotals(totals.items, totals.files, formatBytes(totals.bytes))}</p>
  <div class="sv-actions">
    <button type="button" class="primary-button" disabled={totals.items === 0 || loading || !!flow.busy} onclick={() => flow.startPulls()}>{T.hasGo(totals.items)}</button>
    {#if totals.items === 0 && !loading}<span class="sv-note">{T.hasNothing}</span>{/if}
  </div>
</section>

<style>
  .sv-case { display: grid; gap: var(--space-2); padding: var(--space-3); background: var(--surface-2); border-radius: var(--radius-sm); }
  .sv-case h4 { margin: 0; font-size: var(--fs-4); font-weight: 600; }
  .sv-facts { display: grid; gap: 4px; margin: 0; font-size: 13px; }
  .sv-facts div { display: flex; flex-wrap: wrap; gap: 10px; }
  .sv-facts dt { flex: 0 0 140px; color: var(--text-2); }
  .sv-facts dd { margin: 0; }
  .sv-list { display: grid; margin: 0; padding: 0; list-style: none; }
  .sv-item { align-items: start; }
  .sv-dir { font-family: var(--mono); font-size: 12px; overflow-wrap: anywhere; color: var(--text-2); }
  .sv-inline { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .sv-line { margin: 0; font-size: 13px; }
</style>
