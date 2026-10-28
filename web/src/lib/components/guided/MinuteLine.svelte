<script lang="ts">
  import { fmtWhen, peakCaption } from '../../guided/stepModel';
  import type { Minute } from '../../guided/types';

  // A small line chart of one number per minute. A minute with no value breaks the line: it is never drawn as zero.
  let { series, field, label, unit }: { series: Minute[]; field: 'p95' | 'inFlight'; label: string; unit: string } = $props();

  const W = 640;
  const H = 140;
  const PAD = 4;

  const points = $derived(
    [...series].sort((a, b) => a.at.localeCompare(b.at)).map((m) => ({ at: m.at, window: m.window, v: field === 'p95' ? m.p95 : m.inFlight, gap: !!m.gap }))
  );
  const peak = $derived(Math.max(0, ...points.map((p) => (typeof p.v === 'number' ? p.v : 0))));
  const top = $derived(Math.max(1, peak));
  const x = (i: number) => (points.length <= 1 ? W / 2 : PAD + (i / (points.length - 1)) * (W - 2 * PAD));
  const y = (v: number) => H - PAD - (v / top) * (H - 2 * PAD);

  /** Runs of consecutive minutes of one window that have a value. */
  const runs = $derived.by(() => {
    const out: Array<{ window: string; d: string }> = [];
    let current: { window: string; parts: string[] } | null = null;
    points.forEach((p, i) => {
      if (typeof p.v !== 'number' || p.gap) { if (current) out.push({ window: current.window, d: current.parts.join(' ') }); current = null; return; }
      if (!current || current.window !== p.window) {
        if (current) out.push({ window: current.window, d: current.parts.join(' ') });
        current = { window: p.window, parts: [] };
      }
      current.parts.push(`${current.parts.length ? 'L' : 'M'}${x(i).toFixed(1)} ${y(p.v).toFixed(1)}`);
    });
    if (current) out.push({ window: (current as { window: string }).window, d: (current as { parts: string[] }).parts.join(' ') });
    return out;
  });
  const first = $derived(points[0]);
  const last = $derived(points[points.length - 1]);
</script>

{#if points.length > 1}
  <figure>
    <figcaption>{label} <span class="range">{fmtWhen(first?.at)} to {fmtWhen(last?.at)} UTC. {peakCaption(field, peak, unit)}</span></figcaption>
    <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={label} preserveAspectRatio="none">
      <line x1={PAD} y1={H - PAD} x2={W - PAD} y2={H - PAD} class="axis" />
      {#each runs as run, i (i)}<path d={run.d} class={'line ' + (run.window === 'incident' ? 'incident' : 'normal')} />{/each}
    </svg>
    <p class="legend"><span class="key normal"></span> Normal <span class="key incident"></span> Incident. A break in a line is a minute with no data.</p>
  </figure>
{/if}

<style>
  figure { display: grid; gap: 4px; margin: 0; min-width: 0; }
  figcaption { font-size: 13px; font-weight: 600; }
  .range { color: var(--text-2); font-weight: 400; font-size: 12px; }
  svg { width: 100%; height: 140px; background: var(--chart-empty); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .axis { stroke: var(--line); stroke-width: 1; }
  .line { fill: none; stroke-width: 2; vector-effect: non-scaling-stroke; }
  .line.normal { stroke: var(--series-1); }
  .line.incident { stroke: var(--series-4); }
  .legend { margin: 0; color: var(--text-2); font-size: 12px; }
  .key { display: inline-block; width: 12px; height: 3px; margin: 0 4px 3px 8px; vertical-align: middle; }
  .key.normal { background: var(--series-1); }
  .key.incident { background: var(--series-4); }
</style>
