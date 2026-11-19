<script lang="ts">
  import { DRILL_TEXT, DRILL_HELP } from '../lib/drill/drillCopy';
  import { formatMs } from '../lib/drill/overview';
  import { formatAtOnce } from '../lib/drill/callLanes';
  import { compareToNormal } from '../lib/drill/callCompare';
  import { lightLines } from '../lib/drill/nearbyCalls';
  import type { Readout, SourceItem } from '../lib/drill/callReadout';
  import type { DbStateKey, DbStateView } from '../lib/drill/dbState';
  import { formatTime } from '../lib/time';

  // Right-hand panel of the Slow call screen: the selected item, what ran while the call ran,
  // the data sources with the database sentence, what the chart does not show, how to read it,
  // and (demo only) the "Pretend the database is" switch. The database sentence comes from dbState.
  let {
    selected,
    light,
    whileRan,
    steps,
    selectedCall,
    onOpenCall,
    db,
    sources,
    zone,
    demo,
    pretend,
    onPretend
  }: {
    selected: SourceItem | null;
    /** The selected call's own Database, Redis and Web call times, as its nearby row shows them; null when the row has none, undefined when no row matches. */
    light?: { sqlMs: number | null; redisMs: number | null; httpMs: number | null } | null;
    whileRan: Readout | null;
    steps: readonly { p95Ms: number | null }[];
    selectedCall: { sourceId: string; id: string } | null;
    onOpenCall: (call: { sourceId: string; id: string }) => void;
    db: DbStateView;
    sources: { key: 'api' | 'iis' | 'db'; label: string; recorded: boolean }[];
    zone: string;
    demo: boolean;
    pretend: DbStateKey | null;
    onPretend: (k: DbStateKey | null) => void;
  } = $props();

  const SOURCE_NAME: Record<SourceItem['source'], string> = {
    api: DRILL_TEXT.laneApi,
    iis: DRILL_TEXT.laneIis,
    db: DRILL_TEXT.laneDb
  };

  const NOT_SHOWN = [DRILL_TEXT.notShownTime, DRILL_TEXT.notShownStart, DRILL_TEXT.notShownWholeDb, DRILL_TEXT.notShownIis];
  // When the database lane measures throughput, its faint area is batches per second, not a number running at
  // once: the chart help says so, using the same words the chart itself uses.
  const HOW = $derived(
    db.measure === 'batch-rate'
      ? [DRILL_TEXT.legendWidth, DRILL_TEXT.legendShape, DRILL_TEXT.legendRate, DRILL_TEXT.legendHatched, DRILL_TEXT.legendCall]
      : [DRILL_TEXT.legendWidth, DRILL_TEXT.legendShape, DRILL_TEXT.legendHatched, DRILL_TEXT.legendCall]
  );

  const PRETEND: { id: string; state: DbStateKey | null; label: string }[] = [
    { id: 'real', state: null, label: DRILL_TEXT.pretendReal },
    { id: 'qs', state: 'qs', label: DRILL_TEXT.pretendQs },
    { id: 'qsfile', state: 'qsfile', label: DRILL_TEXT.pretendFile },
    { id: 'api', state: 'api', label: DRILL_TEXT.pretendApi },
    { id: 'off', state: 'off', label: DRILL_TEXT.pretendOff },
    { id: 'ro', state: 'ro', label: DRILL_TEXT.pretendRo },
    { id: 'late', state: 'late', label: DRILL_TEXT.pretendLate },
    { id: 'denied', state: 'denied', label: DRILL_TEXT.pretendDenied },
    { id: 'events', state: 'events', label: DRILL_TEXT.pretendEvents },
    { id: 'notread', state: 'notread', label: DRILL_TEXT.pretendNotRead }
  ];

  // A missing number is "not recorded", never 0.
  const fmtAt = (cell: { atOnce: number | null } | null | undefined): string =>
    cell && cell.atOnce !== null && Number.isFinite(cell.atOnce) ? formatAtOnce(cell.atOnce) : DRILL_TEXT.notRecorded;

  // The selected call's Database, Redis and Web calls lines; a missing time is "not recorded", never 0, as in the nearby table.
  const lightRows = $derived(
    lightLines(light, { database: DRILL_TEXT.colDatabase, redis: DRILL_TEXT.colRedis, web: DRILL_TEXT.colWebCalls, notRecorded: DRILL_TEXT.notRecorded }, formatMs)
  );

  const tookText = $derived(
    selected && selected.tookMs !== null && Number.isFinite(selected.tookMs) ? formatMs(selected.tookMs) : DRILL_TEXT.notRecorded
  );
  // The database line keeps the measure of the lane it describes: a lane that recorded batch counts reports
  // batches per second, a lane that measured busy time reports how many ran at once. A missing number is
  // "not recorded", never 0, and a rate is never printed as a number running at once.
  const dbLine = $derived.by(() => {
    const cell = whileRan?.db;
    if (!cell || db.shape === 'none') return DRILL_TEXT.readDbNone;
    const rate = cell.rate;
    if (db.measure === 'batch-rate' && typeof rate === 'number' && Number.isFinite(rate)) {
      return DRILL_TEXT.readDbRatePeak(formatAtOnce(rate));
    }
    return cell.atOnce !== null && Number.isFinite(cell.atOnce) ? DRILL_TEXT.readDbPeak(formatAtOnce(cell.atOnce)) : DRILL_TEXT.readDbNone;
  });
  // Where the selected API call sits against the slowest-5% marks of the steps on the chart. Only API calls have a duration to compare.
  const normalLine = $derived.by((): string | null => {
    if (!selected || selected.source !== 'api' || selected.marker || selected.tookMs === null) return null;
    const range = compareToNormal(selected.tookMs, steps);
    if (!range) return DRILL_TEXT.vsNormalNone;
    const took = formatMs(selected.tookMs);
    if (range.verdict === 'above') return DRILL_TEXT.vsNormalAbove(took, formatMs(range.highestMs));
    if (range.verdict === 'near') return DRILL_TEXT.vsNormalNear(took, formatMs(range.typicalMs), formatMs(range.highestMs));
    return DRILL_TEXT.vsNormalBelow(took, formatMs(range.typicalMs));
  });
</script>

<aside class="scall-inspector" data-testid="scall-inspector" aria-label={DRILL_TEXT.secSelected}>
  <section class="insp-section">
    <h3>{DRILL_TEXT.secSelected}</h3>
    {#if selected}
      <p class="insp-what">{selected.what || DRILL_TEXT.notRecorded}</p>
      <p>{SOURCE_NAME[selected.source]}</p>
      <p>{formatTime(selected.startMs, { zone })}</p>
      <p>{tookText}</p>
      {#if lightRows}
        <ul class="insp-light" data-testid="scall-selected-light">
          {#each lightRows as line (line.label)}
            <li>{line.label}: {line.value}</li>
          {/each}
        </ul>
      {/if}
      {#if normalLine}
        <p class="insp-note" data-testid="scall-vsnormal" title={DRILL_TEXT.vsNormalHow}><strong>{DRILL_TEXT.vsNormalTitle}:</strong> {normalLine}</p>
      {/if}
      {#if selectedCall}
        <button type="button" class="secondary-button insp-open" data-testid="scall-open-call" onclick={() => onOpenCall(selectedCall)}>{DRILL_TEXT.openThisCall}</button>
      {/if}
    {:else}
      <p>{DRILL_TEXT.selectedNone}</p>
    {/if}
  </section>

  <section class="insp-section">
    <h3>{DRILL_TEXT.secWhile}</h3>
    {#if whileRan}
      <ul>
        <li>{DRILL_TEXT.readApiPeak(fmtAt(whileRan.api))}</li>
        <li>{DRILL_TEXT.readIisPeak(fmtAt(whileRan.iis))}</li>
        <li>{dbLine}</li>
      </ul>
      <p class="insp-note">{DRILL_TEXT.whileNote}</p>
    {:else}
      <p>{DRILL_TEXT.notRecorded}</p>
    {/if}
  </section>

  <section class="insp-section" data-testid="scall-sources">
    <h3>{DRILL_TEXT.secSources}</h3>
    <ul>
      {#each sources as source (source.key)}
        <li>{source.label}{#if !source.recorded} · {DRILL_TEXT.notRecorded}{/if}</li>
      {/each}
    </ul>
    <p data-testid="scall-db-state" data-state={db.key}>{db.message}</p>
    <p class="insp-note">{DRILL_TEXT.coverageNote}</p>
  </section>

  <section class="insp-section">
    <details>
      <summary><h3>{DRILL_TEXT.secNotShown}</h3></summary>
      <ul>
        {#each NOT_SHOWN as line (line)}
          <li>{line}</li>
        {/each}
      </ul>
    </details>
  </section>

  <section class="insp-section">
    <details>
      <summary><h3>{DRILL_TEXT.secHow}</h3></summary>
      <ul>
        {#each HOW as line (line)}
          <li>{line}</li>
        {/each}
      </ul>
      <p class="insp-note">{DRILL_HELP.atOnce}</p>
    </details>
  </section>

  {#if demo}
    <div class="scall-pretend" data-testid="scall-pretend">
      <span>{DRILL_TEXT.pretendLabel}</span>
      <div class="pretend-buttons">
        {#each PRETEND as option (option.id)}
          <button
            type="button"
            class="secondary-button"
            data-testid="scall-pretend-{option.id}"
            aria-pressed={pretend === option.state}
            onclick={() => onPretend(option.state)}
          >{option.label}</button>
        {/each}
      </div>
      <p class="insp-note">{DRILL_TEXT.pretendNote}</p>
    </div>
  {/if}
</aside>

<style>
  .scall-inspector {
    display: flex;
    flex-direction: column;
    gap: 14px;
    min-width: 0;
    font-size: 13px;
  }
  .insp-section h3 {
    margin: 0 0 6px;
    font-size: 13px;
    font-weight: 600;
    color: var(--text-2);
  }
  .insp-section p,
  .scall-pretend p {
    margin: 0 0 4px;
  }
  .insp-section ul {
    margin: 0 0 6px;
    padding-left: 18px;
  }
  .insp-section summary {
    cursor: pointer;
  }
  .insp-section summary h3 {
    display: inline;
    margin: 0;
  }
  .insp-open {
    margin-top: 6px;
    min-height: 28px;
    padding: 3px 10px;
    font-size: 13px;
    border-radius: var(--radius-sm);
  }
  .insp-what {
    font-weight: 600;
    overflow-wrap: anywhere;
  }
  .insp-note {
    color: var(--text-2);
  }
  .scall-pretend {
    border-top: 1px dashed var(--line);
    padding-top: 10px;
  }
  .scall-pretend > span {
    display: block;
    margin-bottom: 6px;
    font-weight: 600;
  }
  .pretend-buttons {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-bottom: 6px;
  }
  .pretend-buttons .secondary-button {
    min-height: 28px;
    padding: 3px 10px;
    font-size: 13px;
    border-radius: var(--radius-sm);
  }
  .pretend-buttons .secondary-button[aria-pressed='true'] {
    border-color: var(--text);
    box-shadow: inset 0 0 0 1px var(--text);
    font-weight: 600;
  }
</style>
