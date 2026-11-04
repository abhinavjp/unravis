<script lang="ts">
  // The Suspects rail of the Investigation Timeline (the wireframe's right-hand panel). It reads the answer the timeline already holds
  // (no second request) through lib/tracing/suspects.ts and lists places to look, never causes.
  import type { TracingLaneKey, TracingResponse } from '../lib/tracing/model';
  import { LANE_NAME } from '../lib/tracing/lanes';
  import { anyLaneRead, evaluateSuspects, momentStartMs, suspectsView, CATALOGUE_TOTAL, type Suspect, type SuspectColour } from '../lib/tracing/suspects';
  import { SUSPECT_TEXT } from '../lib/copy/suspectsCopy';
  import { formatTime } from '../lib/time';
  import { needsYear } from '../lib/chartTime';
  import Sticker from '../brand/components/Sticker.svelte';

  let { response, zone, onShow }: { response: TracingResponse; zone: string; onShow: (slot: number, lane?: TracingLaneKey) => void } = $props();

  const list = $derived(evaluateSuspects(response));
  let all = $state(false);
  const view = $derived(suspectsView(list, all));
  const readable = $derived(anyLaneRead(response));
  const showYear = $derived.by(() => {
    const start = Date.parse(response.window.start);
    const end = Date.parse(response.window.end);
    return Number.isFinite(start) && Number.isFinite(end) ? needsYear(start, end, zone) : false;
  });

  const COLOURS: SuspectColour[] = ['red', 'orange', 'green', 'grey'];
  const WORD: Record<SuspectColour, string> = { red: SUSPECT_TEXT.red, orange: SUSPECT_TEXT.orange, green: SUSPECT_TEXT.green, grey: SUSPECT_TEXT.grey };
  const MEAN: Record<SuspectColour, string> = { red: SUSPECT_TEXT.meanRed, orange: SUSPECT_TEXT.meanOrange, green: SUSPECT_TEXT.meanGreen, grey: SUSPECT_TEXT.meanGrey };
  const READING: Record<string, string> = { yes: SUSPECT_TEXT.yes, no: SUSPECT_TEXT.no, unknown: SUSPECT_TEXT.unknown };

  function momentLine(suspect: Suspect): string {
    if (suspect.momentSlot == null || !suspect.momentLane) return '';
    const ms = momentStartMs(response, suspect.momentSlot);
    return ms == null ? '' : SUSPECT_TEXT.moment(LANE_NAME[suspect.momentLane], formatTime(ms, { zone, precision: 'minute', year: showYear }));
  }

  // "See all" and "Show fewer" swap the list under the pressed button: the button stays, so focus stays on it.
  function toggleAll() { all = !all; }
</script>

{#snippet dot(colour: SuspectColour)}<span class="dot {colour}" aria-hidden="true"></span>{/snippet}

{#snippet card(suspect: Suspect, pin: boolean)}
  <article class="susc {suspect.colour}" data-sus={suspect.id} data-colour={suspect.colour}>
    <header class="row">
      {@render dot(suspect.colour)}<span class="sid">{suspect.id}</span><b class="sttl">{suspect.title}</b>
      {#if pin}<span class="stk"><Sticker tone="neutral" label={SUSPECT_TEXT.dataCheck} /></span>{/if}
    </header>
    <p class="sm"><b>{WORD[suspect.colour]} · {suspect.result}.</b> {suspect.why}</p>
    {#if suspect.colour === 'grey' && suspect.needs}<p class="sm susneeds"><b>{SUSPECT_TEXT.needs}</b> {suspect.needs}</p>{/if}
    {#if momentLine(suspect)}<p class="sm dim susmoment">{momentLine(suspect)}</p>{/if}
    <details>
      <summary class="sm">{SUSPECT_TEXT.evidence}</summary>
      <div class="tscroll">
        <table class="sm susev">
          <thead><tr><th scope="col">{SUSPECT_TEXT.colSignal}</th><th scope="col">{SUSPECT_TEXT.colRole}</th><th scope="col">{SUSPECT_TEXT.colReading}</th><th scope="col">{SUSPECT_TEXT.colWhy}</th></tr></thead>
          <tbody>
            {#each suspect.evidence as row, i (i)}
              <tr><td>{row.signal}</td><td>{row.role === 'must' ? SUSPECT_TEXT.roleMust : SUSPECT_TEXT.roleExpect}</td><td><b>{READING[row.value]}</b></td><td class="dim">{row.why}</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
      <p class="sm"><b>{SUSPECT_TEXT.cheapest}</b> {suspect.check}</p>
      <p class="sm"><b>{SUSPECT_TEXT.overturn}</b> {suspect.overturn}</p>
    </details>
    {#if suspect.momentSlot != null}
      {@const slot = suspect.momentSlot}
      {@const lane = suspect.momentLane ?? undefined}
      <div class="row"><button type="button" class="secondary-button sm" aria-label={SUSPECT_TEXT.showOnChartLabel(suspect.id)} onclick={() => onShow(slot, lane)}>{SUSPECT_TEXT.showOnChart}</button></div>
    {/if}
  </article>
{/snippet}

<aside class="sus" id="sus-rail" aria-label={SUSPECT_TEXT.label} data-testid="sus-rail">
  <h3>{SUSPECT_TEXT.heading}</h3>
  <p class="note"><b>{SUSPECT_TEXT.lead.split('. ')[0]}.</b> {SUSPECT_TEXT.lead.split('. ').slice(1).join('. ')}</p>
  <p class="note" data-testid="sus-scope">{SUSPECT_TEXT.scope}</p>
  {#if !readable}
    <p class="note" role="status" data-testid="sus-empty">{SUSPECT_TEXT.nothingToRead}</p>
  {:else}
    {#if !view.anyRanked}<p class="note" data-testid="sus-norise">{SUSPECT_TEXT.noRise}</p>{/if}
    <p class="note" data-testid="sus-nored">{SUSPECT_TEXT.noRed}</p>
  {/if}
  <ul class="susleg" aria-label={SUSPECT_TEXT.legendLabel}>
    {#each COLOURS as colour (colour)}<li>{@render dot(colour)}<b>{WORD[colour]}:</b> {MEAN[colour]}</li>{/each}
  </ul>
  <p class="sm" role="status" data-testid="sus-summary">{SUSPECT_TEXT.summary(view.total, view.counts.red, view.counts.orange, view.counts.grey, view.counts.green)}</p>
  {#each view.pinned as suspect (suspect.id)}{@render card(suspect, true)}{/each}
  {#each view.shown as suspect (suspect.id)}{@render card(suspect, false)}{/each}
  {#if all && view.green.length > 0}
    <details class="susgreen" data-testid="sus-ruledout">
      <summary><b>{SUSPECT_TEXT.ruledOut(view.green.length)}</b></summary>
      {#each view.green as suspect (suspect.id)}{@render card(suspect, false)}{/each}
    </details>
  {/if}
  {#if all && CATALOGUE_TOTAL > view.total}<p class="sm dim" data-testid="sus-notshown">{SUSPECT_TEXT.notShown(CATALOGUE_TOTAL - view.total)}</p>{/if}
  {#if view.more || all}
    <div class="susmore"><button type="button" class="secondary-button sm" aria-expanded={all} data-testid="sus-all" onclick={toggleAll}>{all ? SUSPECT_TEXT.showFewer : SUSPECT_TEXT.seeAll(view.total)}</button></div>
  {/if}
</aside>

<style>
  .sus { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--space-3); min-width: 0; padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .sus h3 { margin: 0; font-size: var(--fs-4, 16px); font-weight: 600; }
  .note, .sm { margin: 0; font-size: var(--fs-2); }
  .note { color: var(--text-2); }
  .dim { color: var(--text-2); }
  .susleg { display: grid; gap: var(--space-1); margin: 0; padding: 0; list-style: none; font-size: var(--fs-2); }
  .susleg li { display: flex; gap: var(--space-2); align-items: baseline; }
  .dot { flex: none; display: inline-block; width: 10px; height: 10px; border-radius: 50%; background: var(--pending); }
  .dot.red { background: var(--bad); }
  .dot.orange { background: var(--warn); }
  .dot.green { background: var(--setup-ok); }
  .dot.grey { background: var(--pending); }
  .susc { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--space-2); min-width: 0; padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-left: 4px solid var(--pending); border-radius: var(--radius-sm); }
  .susc.red { border-left-color: var(--bad); }
  .susc.orange { border-left-color: var(--warn); }
  .susc.green { border-left-color: var(--setup-ok); }
  .susc.grey { border-left-color: var(--pending); }
  .susc .row, .row { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; min-width: 0; }
  .sid { padding: 0 6px; font-family: var(--mono); font-size: 12px; background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .sttl { min-width: 0; overflow-wrap: anywhere; }
  .stk { margin-left: auto; }
  summary { cursor: pointer; }
  summary:focus-visible, button:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .tscroll { overflow-x: auto; margin: var(--space-2) 0; }
  .susev { width: 100%; border-collapse: collapse; }
  .susev th, .susev td { padding: var(--space-1) var(--space-2); text-align: left; vertical-align: top; border-bottom: var(--hairline-width) solid var(--line-soft); }
  .susgreen { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--space-2); min-width: 0; }
  .susgreen > summary { margin-bottom: var(--space-2); }
  .susmore { display: flex; justify-content: center; padding: var(--space-2) 0; }
  .sm.secondary-button { font-size: 13px; }
</style>
