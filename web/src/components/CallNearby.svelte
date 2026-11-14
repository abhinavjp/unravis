<script lang="ts">
  import { DRILL_TEXT } from '../lib/drill/drillCopy';
  import { chipAvailable, gapLabel, isThisCall, lightCells, type CallRef, type NearbyChip, type NearbyRow } from '../lib/drill/nearbyCalls';
  import { formatMs } from '../lib/drill/overview';
  import { formatTime } from '../lib/time';

  // The exact API calls in the window, with the Everyone / Only this user / Only this API chips and the three
  // light totals columns. A missing number is shown through the "not recorded" copy key, never as 0.
  // The parent filters the rows (filterRows) and decides when the server is asked; this list only draws them.
  let {
    rows,
    call,
    chip,
    start,
    end,
    asking,
    failed,
    truncated,
    zone,
    onChip,
    onSelect
  }: {
    rows: NearbyRow[];
    call: CallRef;
    chip: NearbyChip;
    start: number;
    end: number;
    asking: boolean;
    failed: boolean;
    truncated: boolean;
    zone: string;
    onChip: (c: NearbyChip) => void;
    onSelect: (row: NearbyRow) => void;
  } = $props();

  const CHIPS: NearbyChip[] = ['everyone', 'user', 'api'];

  const hintFrom = $derived(formatTime(start, { zone, date: false, precision: 'minute', suffix: false }));
  const hintTo = $derived(formatTime(end, { zone, date: false, precision: 'minute', suffix: false }));
  const apiName = $derived(`${call.controller}.${call.action}`);

  function chipLabel(c: NearbyChip): string {
    if (c === 'everyone') return DRILL_TEXT.chipEveryone;
    if (c === 'user') return call.userId !== null && call.userId !== undefined ? DRILL_TEXT.chipUser(call.userId) : DRILL_TEXT.chipUserNone;
    return chipAvailable('api', call) ? DRILL_TEXT.chipApi(apiName) : DRILL_TEXT.chipApiNone;
  }

  const askingText = $derived(chip === 'user' ? DRILL_TEXT.askingUser : chip === 'api' ? DRILL_TEXT.askingApi : DRILL_TEXT.askingWider);

  function startMs(row: NearbyRow): number {
    return Date.parse(row.initiatedAt);
  }

  function ms(value: number | null | undefined): string {
    return value === null || value === undefined ? DRILL_TEXT.notRecorded : formatMs(value);
  }

  function userText(row: NearbyRow): string {
    return row.userId === null || row.userId === undefined ? DRILL_TEXT.notRecorded : String(row.userId);
  }

  function rowKey(event: KeyboardEvent, row: NearbyRow): void {
    if (event.key !== 'Enter' && event.key !== ' ') return;
    event.preventDefault();
    onSelect(row);
  }
</script>

<section class="scall-nearby-wrap">
  <p class="hint" data-testid="scall-nearby-hint">{DRILL_TEXT.nearbyHint(hintFrom, hintTo, rows.length)}</p>

  <div class="chips" role="group" aria-label={DRILL_TEXT.chipsLabel}>
    {#each CHIPS as c (c)}
      <button
        type="button"
        class="chip"
        data-testid={`scall-chip-${c}`}
        aria-pressed={chip === c}
        disabled={!chipAvailable(c, call)}
        onclick={() => onChip(c)}
      >{chipLabel(c)}</button>
    {/each}
  </div>

  {#if asking}
    <p class="status" role="status" data-testid="scall-nearby-asking">{askingText}</p>
  {/if}
  {#if failed}
    <p class="status bad" role="alert">{DRILL_TEXT.askFailed}</p>
  {/if}

  {#if rows.length === 0}
    {#if !asking}
      <p class="status">{DRILL_TEXT.noNearby}</p>
    {/if}
  {:else}
    <table class="nearby" data-testid="scall-nearby">
      <thead>
        <tr>
          <th scope="col">{DRILL_TEXT.colTime}</th>
          <th scope="col">{DRILL_TEXT.colFromCall}</th>
          <th scope="col">{DRILL_TEXT.colUser}</th>
          <th scope="col">{DRILL_TEXT.colApi}</th>
          <th scope="col" class="num">{DRILL_TEXT.colTook}</th>
          <th scope="col" class="num">{DRILL_TEXT.colDatabase}</th>
          <th scope="col" class="num">{DRILL_TEXT.colRedis}</th>
          <th scope="col" class="num">{DRILL_TEXT.colWebCalls}</th>
        </tr>
      </thead>
      <tbody>
        {#each rows as row (row.sourceId + '|' + row.id)}
          {@const light = lightCells(row)}
          {@const own = isThisCall(row, call)}
          <!-- svelte-ignore a11y_no_noninteractive_tabindex, a11y_no_noninteractive_element_interactions -->
          <tr
            class:thiscall={own}
            aria-current={own ? 'true' : undefined}
            tabindex="0"
            onclick={() => onSelect(row)}
            onkeydown={(event) => rowKey(event, row)}
          >
            <td class="time">{formatTime(row.initiatedAt, { zone, precision: 'second', date: false, suffix: false })}</td>
            <td>
              {gapLabel(startMs(row), call, { thisCall: DRILL_TEXT.gapThis, during: DRILL_TEXT.gapDuring })}
              {#if own}<strong class="mark">{DRILL_TEXT.thisCallMark}</strong>{/if}
            </td>
            <td>{userText(row)}</td>
            <td>{row.controller}.{row.action}</td>
            <td class="num">{ms(row.durationMs)}</td>
            {#if light === null}
              <td colspan="3" class="muted" data-testid="scall-light-none">{DRILL_TEXT.notRecorded}</td>
            {:else}
              <td class="num">{ms(light.sqlMs)}</td>
              <td class="num">{ms(light.redisMs)}</td>
              <td class="num">{ms(light.httpMs)}</td>
            {/if}
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}

  {#if truncated}
    <p class="status" data-testid="scall-nearby-truncated">{DRILL_TEXT.truncated(rows.length)}</p>
  {/if}
</section>

<style>
  .scall-nearby-wrap {
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 0;
  }
  .hint,
  .status {
    margin: 0;
    font-size: 12px;
    color: var(--text-2);
  }
  .status.bad {
    color: var(--bad);
  }
  .chips {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .chip {
    min-height: 28px;
    padding: 0 10px;
    font-size: 12px;
    color: var(--text);
    background: var(--surface);
    border: 1px solid var(--control-line);
    border-radius: 14px;
    cursor: pointer;
  }
  .chip[aria-pressed='true'] {
    background: var(--surface-2);
    border-color: var(--text-2);
    font-weight: 600;
  }
  .chip:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .chip:focus-visible,
  tbody tr:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }
  .nearby {
    width: 100%;
    border-collapse: collapse;
    font-size: 12px;
  }
  .nearby th,
  .nearby td {
    padding: 5px 8px;
    text-align: left;
    border-bottom: 1px solid var(--line-soft);
  }
  .nearby th {
    font-weight: 600;
    color: var(--text-2);
    border-bottom-color: var(--line);
  }
  .nearby .num {
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
  .nearby .time {
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
  .nearby .muted {
    color: var(--text-2);
  }
  tbody tr {
    cursor: pointer;
  }
  tbody tr:hover {
    background: var(--surface-2);
  }
  tr.thiscall {
    background: var(--surface-2);
    box-shadow: inset 3px 0 0 var(--bad);
  }
  .mark {
    margin-left: 4px;
    font-weight: 600;
  }
</style>
