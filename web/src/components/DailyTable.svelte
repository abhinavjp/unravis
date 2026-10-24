<script lang="ts">
  import { dailyCsv, dayLabel, dayLengthNote, latencyText, rollupCopy, trendView, whole, type RollupDay } from '../lib/rollup';
  let { days, zone }: { days: RollupDay[]; zone: string } = $props();

  function download() {
    const blob = new Blob([dailyCsv(days, zone)], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url; link.download = rollupCopy.csvName;
    document.body.append(link); link.click(); link.remove();
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  }
</script>

<div class="daily">
  <div class="daily-head">
    <p class="daily-note">{rollupCopy.trendRule} {rollupCopy.lowTraffic}</p>
    <button type="button" class="secondary-button" onclick={download} disabled={!days.length}>{rollupCopy.csvButton}</button>
  </div>
  {#if days.length}
    <!-- A scroll area a keyboard user can reach, so a wide table can be scrolled without a mouse. -->
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <div class="table-scroll" role="region" aria-label="Daily requests" tabindex="0">
      <table class="daily-table">
        <caption class="sr-only">One row for each day. {rollupCopy.timings}</caption>
        <thead>
          <tr>
            <th scope="col">Day</th>
            <th scope="col" class="numeric">Requests</th>
            <th scope="col">Against an earlier day</th>
            <th scope="col" class="numeric">Sync</th>
            <th scope="col" class="numeric">Background</th>
            <th scope="col" class="numeric">P50</th>
            <th scope="col" class="numeric">P95</th>
            <th scope="col" class="numeric">4xx</th>
            <th scope="col" class="numeric">5xx</th>
          </tr>
        </thead>
        <tbody>
          {#each days as day (day.date)}
            {@const trend = trendView(day)}
            {@const length = dayLengthNote(day)}
            <tr class:low={day.lowTraffic} class:partial={day.partial}>
              <th scope="row">
                <span class="day-name">{dayLabel(day.date)}</span>
                {#if day.partial}<span class="tag" title={rollupCopy.partial}>Partial</span>{/if}
                {#if day.lowTraffic}<span class="tag" title={rollupCopy.lowTraffic}>Few requests</span>{/if}
                {#if length}<span class="tag">{length}</span>{/if}
              </th>
              <td class="numeric">{whole(day.total)}</td>
              <td class="trend trend-{trend.kind}" title={trend.text}>
                {#if trend.arrow}<span aria-hidden="true" class="arrow">{trend.arrow}</span>{/if}
                <span class={trend.arrow ? 'sr-only' : 'trend-text'}>{trend.text}</span>
                {#if trend.arrow && day.vsPrevPct != null}<span aria-hidden="true" class="pct">{Math.round(Math.abs(day.vsPrevPct))}%</span>{/if}
              </td>
              <td class="numeric">{whole(day.sync)}</td>
              <td class="numeric">{whole(day.background)}</td>
              <td class="numeric">{latencyText(day.p50)}</td>
              <td class="numeric">{latencyText(day.p95)}</td>
              <td class="numeric">{whole(day.count4xx)}</td>
              <td class="numeric">{whole(day.count5xx)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <p class="table-empty">{rollupCopy.noDays}</p>
  {/if}
</div>

<style>
  .daily { display: flex; flex-direction: column; gap: var(--space-2, 8px); }
  .daily-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
  .daily-note { margin: 0; flex: 1 1 260px; color: var(--text-2); font-size: 12px; max-width: 70ch; }
  .daily-table { width: 100%; border-collapse: collapse; font-size: 13px; }
  .daily-table th, .daily-table td { padding: 6px 10px; border-bottom: 1px solid var(--line-soft); text-align: left; white-space: nowrap; }
  .daily-table thead th { color: var(--text-2); font-weight: 600; font-size: 12px; }
  .daily-table th.numeric, .daily-table td.numeric { text-align: right; }
  .daily-table tbody th { font-weight: 600; }
  .day-name { margin-right: 6px; }
  .tag { display: inline-block; margin-left: 4px; padding: 0 6px; border: 1px solid var(--line); border-radius: var(--radius); color: var(--text-2); font-size: 11px; font-weight: 500; }
  tr.low td, tr.low th { color: var(--text-2); }
  tr.low .day-name { font-weight: 500; }
  tr.low td { opacity: .75; }
  tr.partial .day-name { font-style: italic; }
  .trend { min-width: 7.5rem; }
  .trend-text { color: var(--text-2); font-size: 12px; }
  .arrow { display: inline-block; width: 1.2em; }
  .trend-up .arrow, .trend-down .arrow { color: var(--accent); }
  .trend-flat .arrow { color: var(--text-2); }
  .pct { margin-left: 4px; font-variant-numeric: tabular-nums; }
  .sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; border: 0; }
</style>
