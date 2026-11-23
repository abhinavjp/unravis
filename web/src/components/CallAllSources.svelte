<script lang="ts">
  import { DRILL_TEXT } from '../lib/drill/drillCopy';
  import { formatMs } from '../lib/drill/overview';
  import type { SourceItem } from '../lib/drill/callReadout';
  import { capRows } from '../lib/drill/callLanes';
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

  // Only the first rows are drawn (the heading still counts them all), so a very long window stays quick.
  const capped = $derived(capRows(items));

  // The row stays pressable with a mouse (a press anywhere on it selects the item). The real control for a keyboard, a screen reader and a
  // touch screen is the button in the "What" cell: it keeps its own press to itself, so one press never selects twice (BL-495).
  function pick(event: MouseEvent, key: string): void {
    event.stopPropagation();
    onSelect(key);
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
        {#each capped.rows as item (item.key)}
          <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
          <tr class:selected={selectedKey === item.key} onclick={() => onSelect(item.key)}>
            <td class="time" data-label={DRILL_TEXT.colTime}>{formatTime(item.startMs, { zone, date: false, precision: 'second' })}</td>
            <td class="source" data-label={DRILL_TEXT.colSource}><span><span class="dot" aria-hidden="true" style:background={SOURCE_COLOR[item.source]}></span>{SOURCE_NAME[item.source]}</span></td>
            <td class="what" data-label={DRILL_TEXT.colWhat}><button type="button" class="row-btn" data-testid="scall-all-open" aria-pressed={selectedKey === item.key} aria-current={item.thisCall ? 'true' : undefined} onclick={(event) => pick(event, item.key)}>{item.what}{#if item.thisCall} <strong class="mark">{DRILL_TEXT.thisCallMark}</strong>{/if}</button></td>
            <td class="took" data-label={DRILL_TEXT.colTook}>{took(item)}</td>
          </tr>
        {/each}
      </tbody>
    </table>
    {#if capped.capped}
      <p class="muted" data-testid="scall-all-capped">{DRILL_TEXT.rowsCapped(capped.rows.length, capped.total)}</p>
    {/if}
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
  /* The real button of a row (BL-495). It looks like the plain cell text it replaced and moves nothing: no padding, no border, the font and the
     line height of the cell. An invisible ::after grows its press area to the cell (24 px tall or more) without changing its box or its ring. */
  .row-btn {
    position: relative;
    display: inline-block;
    margin: 0;
    padding: 0;
    border: 0;
    border-radius: var(--radius-sm);
    background: none;
    color: inherit;
    font: inherit;
    letter-spacing: inherit;
    text-align: inherit;
    overflow-wrap: inherit;
    word-break: inherit;
    cursor: pointer;
  }
  .row-btn::after {
    content: '';
    position: absolute;
    inset: -6px -8px;
  }
  .row-btn:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }
  tbody tr:has(.row-btn:focus-visible) {
    background: var(--surface-2);
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
  /* Phone width: each item becomes a small card with labelled lines, as in Nearby calls, so the page never scrolls sideways (BL-521: the table was 55 px
     wider than a 390 px window). Wide windows keep the table as it was. */
  @media (max-width: 760px) {
    table,
    tbody,
    tr,
    td {
      display: block;
      width: 100%;
    }
    thead {
      position: absolute;
      width: 1px;
      overflow: hidden;
      clip-path: inset(50%);
      white-space: nowrap;
    }
    tbody tr {
      padding: 6px 0;
      border-bottom: 1px solid var(--line);
    }
    td {
      display: grid;
      grid-template-columns: minmax(0, 6.5rem) minmax(0, 1fr);
      gap: 12px;
      border-bottom: 0;
      padding: 2px 8px;
      text-align: right;
      white-space: normal;
      overflow-wrap: anywhere;
    }
    td::before {
      content: attr(data-label);
      color: var(--text-2);
      text-align: left;
    }
  }
</style>
