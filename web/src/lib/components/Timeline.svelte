<script lang="ts">
  import { TIMELINE_TEXT as TEXT, rowClockWithOffset, rowSubject, ruleWords, timelineGroups, unlistedLine, type TimelineView } from '../timeline';

  // The thin timeline: audit intervals with the events that overlap them, on one clock. The server writes each row's
  // sentence from the permitted wordings; this component only shows them. It never adds a cause.
  let { view, zone = 'UTC' }: { view: TimelineView; zone?: string } = $props();

  const groups = $derived(timelineGroups(view));
  const timeHeading = $derived(`${TEXT.time} (${zone})`);
  const unlisted = $derived(unlistedLine(view.unlisted));
</script>

<section class="timeline" aria-labelledby="timeline-title">
  <h3 id="timeline-title">{TEXT.title}</h3>
  <p class="timeline-note">{TEXT.intro}</p>

  {#if !view.enabled}
    <div class="timeline-off" role="status">
      <strong>{TEXT.off}</strong>
      <p>{view.reason}</p>
    </div>
  {/if}

  {#if view.sources.length > 0}
    <table class="timeline-table sources">
      <caption>{TEXT.sources}</caption>
      <thead><tr><th scope="col">{TEXT.source}</th><th scope="col">{TEXT.host}</th><th scope="col">{TEXT.timeRule}</th><th scope="col" class="num">{TEXT.files}</th></tr></thead>
      <tbody>
        {#each view.sources as source (source.name)}
          <tr class:unchecked={!source.validated}>
            <th scope="row">{source.name}</th>
            <td>{source.hosts.join(', ')}</td>
            <td>{source.tzRules.length ? source.tzRules.map(ruleWords).join(', ') : TEXT.notChecked}</td>
            <td class="num">{source.files}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}

  {#if view.enabled}
    {#if groups.length === 0}
      <p class="timeline-note">{TEXT.noRows}</p>
    {/if}
    {#each groups as group (group.intervalId)}
      <div class="timeline-group">
        <table class="timeline-table">
          <thead>
            <tr><th scope="col">{timeHeading}</th><th scope="col">{TEXT.label}</th><th scope="col">{TEXT.what}</th></tr>
          </thead>
          <tbody>
            <tr class="interval">
              <th scope="row" class="clock">{rowClockWithOffset(group.interval, zone)}</th>
              <td><span class="label symptom">{group.interval.label}</span></td>
              <td>{group.interval.wording}</td>
            </tr>
            {#each group.rows as row (row.kind + row.source + row.evidenceId)}
              <tr class={row.kind}>
                <th scope="row" class="clock">{rowClockWithOffset(row, zone)}</th>
                <td><span class="label" class:symptom={row.label === 'Symptom'}>{row.label}</span></td>
                <td>
                  <span class="subject">{rowSubject(row)}</span>
                  <span class="wording">{row.wording}</span>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/each}
    {#if unlisted}<p class="timeline-note">{unlisted}</p>{/if}
    {#if view.truncated}<div class="timeline-off" role="status">{TEXT.truncated}</div>{/if}
  {/if}
</section>

<style>
  .timeline { display: grid; gap: 10px; min-width: 0; }
  .timeline h3 { margin: 0; }
  .timeline-note { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
  .timeline-off { padding: 8px 10px; border: 1px solid var(--warn); border-radius: var(--radius); background: color-mix(in srgb, var(--warn) 10%, var(--surface)); font-size: var(--fs-2); }
  .timeline-off p { margin: 4px 0 0; }
  .timeline-group { overflow-x: auto; border: 1px solid var(--line-soft); border-radius: var(--radius); }
  .timeline-table { width: 100%; border-collapse: collapse; font-size: var(--fs-2); }
  .timeline-table caption { text-align: left; color: var(--text-2); font-weight: 600; padding: 0 0 4px; }
  .timeline-table th, .timeline-table td { padding: 5px 8px; text-align: left; vertical-align: top; border-bottom: 1px solid var(--line-soft); font-weight: 400; }
  .timeline-table thead th { background: var(--surface-2); color: var(--text-2); font-weight: 600; white-space: nowrap; }
  .timeline-table .num { text-align: right; font-variant-numeric: tabular-nums; }
  .timeline-table .clock { font-family: var(--mono); white-space: nowrap; }
  .timeline-table .interval td, .timeline-table .interval th { font-weight: 600; }
  .timeline-table .absence td { color: var(--text-2); }
  .timeline-table .unchecked th, .timeline-table .unchecked td { color: var(--warn); }
  .subject { display: block; color: var(--text-2); font-size: var(--fs-1, 12px); overflow-wrap: anywhere; }
  .wording { overflow-wrap: anywhere; }
  .label { display: inline-block; padding: 1px 6px; border: 1px solid var(--line); border-radius: var(--radius-sm); font-size: 12px; white-space: nowrap; }
  .label.symptom { color: var(--text-2); }
</style>
