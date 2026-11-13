<script lang="ts">
  import { DRILL_TEXT } from '../lib/drill/drillCopy';
  import { formatMs } from '../lib/drill/overview';
  import type { SourceItem } from '../lib/drill/callReadout';
  import { formatTime } from '../lib/time';

  // Every slow item and event marker of the shown sources in one time-ordered table.
  // Kept apart from Nearby calls. The lanes are matched by time only.
  // Row keys come from allSources(): api:<id>, iis:<id>, db:<source>:<evidenceId> (the same key the lanes chart selects).
  let {
    items,
    zone,
    selectedKey,
    onSelect
  }: {
    items: SourceItem[];
    zone: string;
    selectedKey: string | null;
    onSelect: (key: string) => void;
  } = $props();

  const SOURCE_NAME: Record<SourceItem['source'], string> = {
    api: DRILL_TEXT.laneApi,
    iis: DRILL_TEXT.laneIis,
    db: DRILL_TEXT.laneDb
  };
  const SOURCE_COLOR: Record<SourceItem['source'], string> = {
    api: 'var(--lane-audit)',
    iis: 'var(--lane-iis)',
    db: 'var(--lane-sql)'
  };

  // A marker has no length: the cell stays empty. A bar with no duration says "not recorded", never 0.
  function took(item: SourceItem): string {
    if (item.marker) return '';
    if (item.tookMs === null || !Number.isFinite(item.tookMs)) return DRILL_TEXT.notRecorded;
    return formatMs(item.tookMs);
  }

  function onKey(event: KeyboardEvent, key: string): void {
    if (event.key === 'Enter') {
      event.preventDefault();
      onSelect(key);
    }
  }
</script>

<section class="all-sources">
  <h3 data-testid="scall-all-heading">{DRILL_TEXT.allHeading(items.length)}</h3>
  {#if items.length === 0}
    <p data-testid="scall-all-empty">{DRILL_TEXT.allEmpty}</p>
  {:else}
    <table data-testid="scall-all">
      <thead>
        <tr>
          <th scope="col">{DRILL_TEXT.colTime}</th>
          <th scope="col">{DRILL_TEXT.colSource}</th>
          <th scope="col">{DRILL_TEXT.colWhat}</th>
          <th scope="col">{DRILL_TEXT.colTook}</th>
        </tr>
      </thead>
      <tbody>
        {#each items as item (item.key)}
          <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <tr
            class:selected={selectedKey === item.key}
            aria-current={item.thisCall ? 'true' : undefined}
            tabindex="0"
            onclick={() => onSelect(item.key)}
            onkeydown={(event) => onKey(event, item.key)}
          >
            <td class="time">{formatTime(item.startMs, { zone, date: false, precision: 'second' })}</td>
            <td class="source"><span class="dot" aria-hidden="true" style:background={SOURCE_COLOR[item.source]}></span>{SOURCE_NAME[item.source]}</td>
            <td class="what">{item.what}{#if item.thisCall} <strong class="mark">{DRILL_TEXT.thisCallMark}</strong>{/if}</td>
            <td class="took">{took(item)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
  <p class="muted">{DRILL_TEXT.notShownTime}</p>
</section>

<style>
  .all-sources {
    display: block;
  }
  table {
    width: 100%;
    border-collapse: collapse;
    font-size: 13px;
  }
  th {
    text-align: left;
    font-weight: 600;
    color: var(--text-2);
    padding: 6px 8px;
    border-bottom: 1px solid var(--line);
  }
  td {
    padding: 6px 8px;
    border-bottom: 1px solid var(--line-soft);
    vertical-align: top;
  }
  tbody tr {
    cursor: pointer;
  }
  tbody tr:hover {
    background: var(--surface-2);
  }
  tbody tr:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: -2px;
  }
  tbody tr.selected {
    background: var(--surface-2);
    box-shadow: inset 3px 0 0 var(--text);
  }
  .time,
  .took {
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }
  .dot {
    display: inline-block;
    width: 9px;
    height: 9px;
    border-radius: 50%;
    margin-right: 6px;
  }
  .mark {
    color: var(--text-2);
    font-weight: 600;
  }
  .muted {
    color: var(--text-2);
    font-size: 12px;
  }
</style>
