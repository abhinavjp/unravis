<script lang="ts">
  import type { ServersFlow } from '../../lib/serversFlow.svelte';
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';
  import { FOLDER_KINDS, failureText, formatBytes, planSummary, rangeChoices, rangeLabel, type RangeChoice } from '../../lib/servers';

  // One machine of step 5: a table of what was found in each folder kind, with a tick and a time range per row.
  let { flow, machine }: { flow: ServersFlow; machine: string } = $props();

  const choices = $derived(rangeChoices(flow.caseRange()));
  const mine = $derived(flow.targets.filter((t) => t.machine === machine));
  const missing = $derived(FOLDER_KINDS.filter((k) => !mine.some((t) => t.kind === k.kind)));
  const chosenBytes = $derived(mine.reduce((n, t) => {
    const row = flow.rows[t.key];
    const sum = row?.on && row.plan ? planSummary(row.plan) : null;
    return n + (sum && !sum.empty && !sum.problem ? sum.bytes : 0);
  }, 0));
  const asRange = (value: string) => value as RangeChoice;
</script>

<div class="sv-panel">
  <header><h4>{machine}</h4><span class="sv-sp"></span><span class="sv-sub">{T.hasChosen(formatBytes(chosenBytes))}</span></header>
  <div class="sv-table-scroll">
  <table class="sv-tbl">
    <thead><tr><th><span class="sv-sr">{T.hasWhat}</span></th><th>{T.hasWhat}</th><th>{T.hasHowMuch}</th><th>{T.hasFound}</th><th class="r">{T.hasSize}</th></tr></thead>
    <tbody>
      {#each mine as t (t.key)}
        {@const row = flow.rows[t.key]}
        {#if row}
          {@const sum = row.plan ? planSummary(row.plan) : null}
          <tr>
            <td><input type="checkbox" aria-label={t.label} bind:checked={row.on} disabled={row.state !== 'ok' || !sum || sum.empty || !!sum.problem} /></td>
            <td><b>{t.label}</b><div class="sv-sub sv-mono">{t.dir}</div></td>
            <td>
              <select aria-label={T.hasRange + ': ' + t.label} value={row.range} disabled={row.state === 'loading'} onchange={(e) => flow.setRange(t.key, asRange(e.currentTarget.value))}>
                {#each choices as c (c)}<option value={c}>{rangeLabel(c)}</option>{/each}
                {#if !choices.includes(row.range)}<option value={row.range}>{rangeLabel(row.range)}</option>{/if}
              </select>
            </td>
            {#if row.state === 'loading'}
              <td colspan="2" class="sv-sub">{T.hasPlanning}</td>
            {:else if row.state === 'failed' && row.failure}
              <td colspan="2" class="sv-bad">{failureText(row.failure)} <button type="button" class="quiet-button" onclick={() => flow.planRow(t.key)}>{T.hasReplan}</button></td>
            {:else if sum}
              {#if sum.problem}
                <td colspan="2" class="sv-bad">{sum.problem}</td>
              {:else if sum.empty}
                <td class="sv-sub">{T.hasEmpty}</td><td class="r"></td>
              {:else}
                <td>{T.hasFileCount(sum.files)}</td><td class="r sv-mono">{formatBytes(sum.bytes)}</td>
              {/if}
            {:else}
              <td></td><td></td>
            {/if}
          </tr>
          {#if sum && sum.skipped > 0}
            <tr><td></td><td colspan="4" class="sv-sub">{T.hasSkipped(sum.skipped)} {row.plan?.skipped.slice(0, 3).map((s) => s.name + ': ' + s.reason).join('; ')}</td></tr>
          {/if}
        {/if}
      {/each}
      {#each missing as k (k.kind)}
        <tr class="sv-dim"><td><input type="checkbox" disabled aria-label={k.label} /></td><td>{k.label}</td><td class="sv-sub">{T.hasNotFound}</td><td></td><td class="r"></td></tr>
      {/each}
    </tbody>
  </table>
  </div>
</div>

<style>
  .sv-table-scroll { overflow-x: auto; }
  .sv-sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
  select { max-width: 100%; padding: 4px 6px; font: inherit; font-size: var(--fs-3); color: var(--text); background: var(--surface); border: 1px solid var(--control-line); border-radius: var(--radius-sm); }
</style>
