<script lang="ts">
  // The explanation block of the Investigation Timeline: how to read the chart, what each lane shows, how the
  // axis was worked out, and where each lane's data came from. Every word is built in `lib/tracing/explain.ts`.
  import { EXPLAIN_TEXT, LANE_SHOWS, axisLines, detailsRows } from '../lib/tracing/explain';
  import { LANE_ASK, LANE_NAME, LANE_ORDER } from '../lib/tracing/lanes';
  import type { TracingLane } from '../lib/tracing/model';

  let { lanes, zoneName, bucketLabel, formatAt }: {
    lanes: TracingLane[];
    zoneName: string;
    bucketLabel: string;
    formatAt: (iso: string) => string;
  } = $props();

  const axis = $derived(axisLines(bucketLabel, zoneName));
  const rows = $derived(detailsRows(lanes, zoneName, formatAt));
</script>

<section class="tracing-explain">
  <details open data-testid="explain-read">
    <summary>{EXPLAIN_TEXT.readTitle}</summary>
    <ol>
      {#each EXPLAIN_TEXT.readLines as line}
        <li>{line}</li>
      {/each}
    </ol>
  </details>
  <details data-testid="explain-lanes">
    <summary>{EXPLAIN_TEXT.lanesTitle}</summary>
    <dl>
      {#each LANE_ORDER as key (key)}
        <dt>{LANE_NAME[key]}</dt>
        <dd><span class="ask">{LANE_ASK[key]}</span> {LANE_SHOWS[key]}</dd>
      {/each}
    </dl>
  </details>
  <details data-testid="explain-axis">
    <summary>{EXPLAIN_TEXT.axisTitle}</summary>
    <ul>
      {#each axis as line}
        <li>{line}</li>
      {/each}
    </ul>
  </details>
  <details data-testid="explain-details">
    <summary>{EXPLAIN_TEXT.detailsTitle}</summary>
    <table>
      <caption class="sr-only">Where each lane's data came from</caption>
      <thead>
        <tr><th>Lane</th><th>Where from</th><th>Time of data</th><th>Whole window</th><th>Files or sources</th></tr>
      </thead>
      <tbody>
        {#each rows as row (row.key)}
          <tr data-testid="explain-details-row" data-lane={row.key}>
            <td>{row.name}</td><td>{row.where}</td><td>{row.when}</td><td>{row.whole}</td><td>{row.sources}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </details>
</section>

<style>
  .tracing-explain { display: grid; gap: var(--space-2); padding-left: var(--space-3); border-left: var(--hairline-width) solid var(--line); color: var(--text-2); font-size: var(--fs-2); }
  details > summary { cursor: pointer; width: fit-content; font-weight: 500; }
  details > summary:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  ol, ul { display: grid; gap: var(--space-1); margin: var(--space-1) 0 0; padding-left: var(--space-4); }
  dl { display: grid; gap: var(--space-1); margin: var(--space-1) 0 0; }
  dt { color: var(--text); }
  dd { margin: 0 0 var(--space-1); }
  dd .ask { font-style: italic; }
  table { margin-top: var(--space-1); border-collapse: collapse; }
  th, td { padding: var(--space-1) var(--space-2); text-align: left; vertical-align: top; border-top: var(--hairline-width) solid var(--line); }
  th { color: var(--text); font-weight: 500; }
  .sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; border: 0; }
</style>
