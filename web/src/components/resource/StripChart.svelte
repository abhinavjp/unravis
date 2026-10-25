<script lang="ts">
  import { onMount } from 'svelte';
  import { MarkAreaComponent, TitleComponent } from 'echarts/components';
  import { echarts, palette } from '../../lib/charts';
  import { theme } from '../../lib/theme.svelte';
  import { GRID_LEFT, GRID_RIGHT, chartHeight, isBrush, stripsOption, type EpisodeSpan, type Marker, type Strip, type ViewWindow } from '../../lib/resource';

  // The stacked strips: one chart with one grid per strip on one shared time axis. A drag across it zooms to that span; a click picks a time.
  // Both are also offered as buttons beside the chart (the panel owns them), so the canvas is never the only way in.
  echarts.use([MarkAreaComponent, TitleComponent]);

  let { strips, view, episodes, markers, zone, label, onBrush, onPick }: {
    strips: Strip[]; view: ViewWindow; episodes: EpisodeSpan[]; markers: Marker[]; zone: string; label: string;
    onBrush: (fromMs: number, toMs: number) => void; onPick: (t: number) => void;
  } = $props();

  let node = $state<HTMLDivElement>();
  let chart = $state<ReturnType<typeof echarts.init>>();
  let drag = $state<{ x0: number; x1: number } | null>(null);
  const height = $derived(chartHeight(strips.length));

  onMount(() => {
    if (!node) return;
    // A sliding drawer or a hidden section can give the node no width yet, and ECharts warns when it starts on a zero-size node.
    // So the chart starts on the first size the node reports, and resizes after that.
    const target = node;
    const observer = new ResizeObserver(() => {
      if (!chart) {
        if (target.clientWidth > 0 && target.clientHeight > 0) chart = echarts.init(target);
        return;
      }
      chart.resize();
    });
    observer.observe(target);
    return () => { observer.disconnect(); chart?.dispose(); chart = undefined; };
  });

  $effect(() => {
    void theme.epoch;
    if (!chart || strips.length === 0) return;
    chart.setOption(stripsOption({ strips, view, episodes, markers, zone, pal: palette() }), true);
    chart.resize({ height });
  });

  const localX = (event: PointerEvent): number => (node ? event.clientX - node.getBoundingClientRect().left : 0);
  function timeAt(x: number): number | null {
    if (!chart || !node) return null;
    const width = node.clientWidth;
    if (x < GRID_LEFT || x > width - GRID_RIGHT) return null;
    const value = chart.convertFromPixel({ xAxisIndex: 0 }, x);
    const t = Array.isArray(value) ? Number(value[0]) : Number(value);
    return Number.isFinite(t) ? Math.min(view.toMs, Math.max(view.fromMs, t)) : null;
  }
  function down(event: PointerEvent) {
    if (event.button !== 0 || !node) return;
    const x = localX(event);
    drag = { x0: x, x1: x };
    node.setPointerCapture(event.pointerId);
  }
  function move(event: PointerEvent) { if (drag) drag = { x0: drag.x0, x1: localX(event) }; }
  function up(event: PointerEvent) {
    if (!drag) return;
    const done = drag;
    drag = null;
    node?.releasePointerCapture(event.pointerId);
    const a = timeAt(done.x0), b = timeAt(Math.min(Math.max(done.x1, GRID_LEFT), (node?.clientWidth ?? 0) - GRID_RIGHT));
    if (a == null) return;
    if (isBrush(done.x1 - done.x0) && b != null && b !== a) onBrush(Math.min(a, b), Math.max(a, b));
    else onPick(a);
  }
  const cancel = () => { drag = null; };
</script>

<div class="wrap">
  <!-- The pointer handlers draw a zoom box on the canvas. The same zoom and the stall periods are offered as buttons beside it. -->
  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div class="chart" bind:this={node} role="img" aria-label={label} style:height={height + 'px'} onpointerdown={down} onpointermove={move} onpointerup={up} onpointercancel={cancel}></div>
  {#if drag && isBrush(drag.x1 - drag.x0)}
    <div class="box" aria-hidden="true" style:left={Math.min(drag.x0, drag.x1) + 'px'} style:width={Math.abs(drag.x1 - drag.x0) + 'px'}></div>
  {/if}
</div>

<style>
  .wrap { position: relative; width: 100%; min-width: 0; }
  .chart { width: 100%; touch-action: pan-y; cursor: crosshair; user-select: none; }
  .box { position: absolute; top: 20px; bottom: 30px; pointer-events: none; background: color-mix(in srgb, var(--accent) 14%, transparent); border: 1px solid var(--accent); border-radius: 2px; }
</style>
