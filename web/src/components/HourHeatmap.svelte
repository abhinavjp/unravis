<script lang="ts">
  import { tick } from 'svelte';
  import { HOURS_PER_ROW, cellLabel, cellValue, cellWindow, dayLabel, gridMax, gridMetrics, gridRows, moveInGrid, rollupCopy, shadeLevel, slowNote, type GridMetric, type HourGridResponse, type RollupWindow } from '../lib/rollup';
  let { grid, zone, onChoose }: { grid: HourGridResponse; zone: string; onChoose: (window: RollupWindow) => void } = $props();

  let metric = $state<GridMetric>('p95');
  let active = $state<[number, number] | null>(null);
  let root: HTMLElement | undefined;

  const rows = $derived(gridRows(grid));
  const max = $derived(gridMax(grid.cells, metric));
  // The square that takes the Tab stop: the one last used, else the first square that has requests.
  const stop = $derived.by<[number, number]>(() => {
    if (active && rows[active[0]]?.cells[active[1]]) return active;
    for (let r = 0; r < rows.length; r++) for (let c = 0; c < HOURS_PER_ROW; c++) if (rows[r].cells[c]) return [r, c];
    return [0, 0];
  });
  const hourMarks = [0, 3, 6, 9, 12, 15, 18, 21];

  async function onKey(event: KeyboardEvent, row: number, col: number) {
    if (!['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const next = moveInGrid(rows, row, col, event.key);
    active = next;
    await tick();
    root?.querySelector<HTMLElement>(`[data-r="${next[0]}"][data-c="${next[1]}"]`)?.focus();
  }

  function choose(row: number, col: number) {
    const win = cellWindow(rows[row]?.cells[col]);
    if (win) onChoose(win);
  }
</script>

<div class="hm" bind:this={root}>
  <div class="hm-head">
    <div class="hm-metric" role="group" aria-label="Shade squares by">
      {#each gridMetrics as item (item.id)}
        <button type="button" class="chip" aria-pressed={metric === item.id} class:on={metric === item.id} onclick={() => metric = item.id}>{item.label}</button>
      {/each}
    </div>
    <p class="hm-note">{rollupCopy.gridHow} {rollupCopy.gridNoise} {slowNote(grid.slowMs)} {rollupCopy.gridKeys}</p>
  </div>
  {#if rows.length}
    <div class="table-scroll hm-scroll">
      <div class="hm-grid" role="grid" aria-label={'Requests by hour, shaded by ' + gridMetrics.find(item => item.id === metric)?.label.toLowerCase()} aria-rowcount={rows.length}>
        <div class="hm-row hm-marks" role="row">
          <span class="hm-day" role="columnheader"><span class="sr-only">Day</span></span>
          {#each Array(HOURS_PER_ROW) as _, hour}
            <span class="hm-mark" role="columnheader"><span class:sr-only={!hourMarks.includes(hour)}>{hour}</span></span>
          {/each}
        </div>
        {#each rows as row, r (row.date)}
          <div class="hm-row" role="row">
            <span class="hm-day" role="rowheader">{dayLabel(row.date)}{#if row.partial}<span class="hm-partial" title={rollupCopy.partial}>partial</span>{/if}</span>
            {#each row.cells as cell, c}
              {#if cell}
                {@const level = shadeLevel(cellValue(cell, metric), max)}
                <span role="gridcell" class="hm-cell-wrap">
                  <button type="button" class="hm-cell level-{level}" class:noise={cell.noise} class:empty={cell.count <= 0}
                    data-r={r} data-c={c} tabindex={stop[0] === r && stop[1] === c ? 0 : -1}
                    aria-label={cellLabel(cell, zone, grid.slowMs)} title={cellLabel(cell, zone, grid.slowMs)}
                    aria-disabled={cell.count <= 0 ? 'true' : undefined}
                    onfocus={() => active = [r, c]}
                    onkeydown={event => onKey(event, r, c)}
                    onclick={() => choose(r, c)}></button>
                </span>
              {:else}
                <span role="gridcell" class="hm-cell-wrap"><span class="hm-gap" aria-label="No such hour on this day"></span></span>
              {/if}
            {/each}
          </div>
        {/each}
      </div>
    </div>
    <div class="hm-legend" aria-hidden="true">
      <span>Lower</span>
      {#each [1, 2, 3, 4] as level}<span class="hm-swatch level-{level}"></span>{/each}
      <span>Higher</span>
      <span class="hm-swatch noise"></span><span>Few requests</span>
      <span class="hm-swatch empty"></span><span>None</span>
    </div>
  {:else}
    <p class="table-empty">{rollupCopy.noDays}</p>
  {/if}
</div>

<style>
  .hm { display: flex; flex-direction: column; gap: var(--space-2, 8px); }
  .hm-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
  .hm-metric { display: flex; gap: 6px; flex-wrap: wrap; }
  .hm-note { margin: 0; flex: 1 1 260px; color: var(--text-2); font-size: 12px; max-width: 70ch; }
  .hm-scroll { padding-bottom: 4px; }
  .hm-grid { display: flex; flex-direction: column; gap: 2px; min-width: 560px; }
  .hm-row { display: grid; grid-template-columns: 5.5rem repeat(24, minmax(14px, 1fr)); gap: 2px; align-items: center; }
  .hm-day { font-size: 12px; color: var(--text-2); white-space: nowrap; }
  .hm-partial { font-style: italic; margin-left: 4px; }
  .hm-mark { font-size: 11px; color: var(--text-2); text-align: left; line-height: 1; }
  .hm-cell-wrap { display: block; min-width: 0; }
  .hm-cell, .hm-gap { display: block; width: 100%; height: 20px; padding: 0; border-radius: 1px; }
  .hm-cell { border: 1px solid var(--line-soft); background: var(--surface-2); cursor: pointer; }
  .hm-cell.empty { cursor: default; background: transparent; }
  .hm-gap { border: 1px solid transparent; }
  .level-1 { background: var(--heat-1); }
  .level-2 { background: var(--heat-2); }
  .level-3 { background: var(--heat-3); }
  .level-4 { background: var(--heat-4); }
  .hm-cell.noise { border: 1px dashed var(--text-2); }
  .hm-cell:hover:not(.empty) { outline: 1px solid var(--text); outline-offset: 0; }
  .hm-cell:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; position: relative; z-index: 1; }
  .hm-legend { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; color: var(--text-2); font-size: 12px; }
  .hm-swatch { display: inline-block; width: 16px; height: 12px; border: 1px solid var(--line-soft); border-radius: 1px; background: var(--surface-2); }
  .hm-swatch.noise { background: var(--surface-2); border: 1px dashed var(--text-2); margin-left: 8px; }
  .hm-swatch.empty { background: transparent; margin-left: 8px; }
  .sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; border: 0; }
</style>
