<script lang="ts">
  import { blackboxApi, defaultHostWindows, gapSummary, refusalText, shownTime, type BlackboxItem, type HostAnswer } from '../lib/blackbox';
  import { BB_REFUSAL, BB_TEXT as T } from '../lib/copy/blackboxCopy';
  import WindowPicker from '../lib/components/WindowPicker.svelte';
  import { timeZone } from '../lib/timeZone.svelte';

  // Step 9, host and network, read from one Blackbox for two windows. A gap of 2 minutes or less warns and the check
  // runs on the minutes that exist. A longer gap makes the minutes inside it Cannot test yet. Missing minutes are never zero.
  let { item, initial = null }: { item: BlackboxItem; initial?: HostAnswer | null } = $props();

  const MINUTE = 60_000;
  const local = (t: number) => new Date(t).toISOString().slice(0, 16);
  // The default windows stay inside what was recorded (first to last sample). A Blackbox that recorded 15 minutes has no
  // earlier reference, so the reference starts empty-handed and the card says why (UX-23).
  // The windows are offered once, from the facts this card opened with.
  // svelte-ignore state_referenced_locally
  const firstAt = Date.parse(item.facts?.firstSample ?? '');
  // svelte-ignore state_referenced_locally
  const defaults = defaultHostWindows(item.facts?.firstSample, item.facts?.lastSample, MINUTE);
  let incStart = $state(defaults ? local(defaults.incStart) : '');
  let incEnd = $state(defaults ? local(defaults.incEnd) : '');
  let refStart = $state(defaults ? local(defaults.refStart) : '');
  let refEnd = $state(defaults ? local(defaults.refEnd) : '');
  // svelte-ignore state_referenced_locally
  let answer = $state<HostAnswer | null>(initial);
  let busy = $state(false);
  let problem = $state('');

  const gaps = $derived(gapSummary(answer?.gaps));
  const toIso = (v: string) => v ? v + ':00Z' : '';
  // True when the reference window ends before the first recorded sample, so it has nothing to compare with.
  const noEarlier = $derived(Number.isFinite(firstAt) && (refEnd === '' || Date.parse(toIso(refEnd)) <= firstAt || refEnd <= refStart));

  function windowsOk(): boolean {
    const w = (s: string, e: string) => {
      const a = Date.parse(toIso(s)), b = Date.parse(toIso(e));
      return Number.isFinite(a) && Number.isFinite(b) && b > a && b - a <= 7 * 24 * 60 * MINUTE;
    };
    return w(incStart, incEnd) && w(refStart, refEnd);
  }

  async function read() {
    if (busy) return;
    problem = '';
    if (!windowsOk()) { answer = null; problem = T.hostWindowProblem; return; }
    busy = true;
    try {
      answer = await blackboxApi.hostReading(item.id, {
        incident: { start: toIso(incStart), end: toIso(incEnd) },
        reference: { start: toIso(refStart), end: toIso(refEnd) }
      });
    } catch (error) {
      answer = null;
      problem = refusalText(error, BB_REFUSAL);
    } finally {
      busy = false;
    }
  }
</script>

<section class="bb-host" aria-label={T.hostTitle}>
  <h3>{T.hostTitle}</h3>
  <p class="bb-note">{T.hostQuestion}</p>

  <details class="bb-windows" open>
    <summary>{T.hostRead}: {item.name}</summary>
    <div class="bb-grid">
      <fieldset><legend>{T.hostIncident} ({timeZone.short})</legend>
        <WindowPicker variant="inline" label={T.hostIncident} bind:start={incStart} bind:end={incEnd} zone={timeZone.zone} zoneName={timeZone.short} />
      </fieldset>
      <fieldset><legend>{T.hostReference} ({timeZone.short})</legend>
        <WindowPicker variant="inline" label={T.hostReference} bind:start={refStart} bind:end={refEnd} zone={timeZone.zone} zoneName={timeZone.short} />
      </fieldset>
    </div>
    {#if noEarlier}<p class="bb-note" id="bb-host-no-earlier">{T.hostNoEarlier(shownTime(item.facts?.firstSample, timeZone.zone))}</p>{/if}
    <button type="button" class="secondary-button" disabled={busy} onclick={read} aria-describedby={noEarlier ? 'bb-host-no-earlier' : undefined}>{busy ? T.hostRunning : T.hostRun}</button>
  </details>

  <div>
    {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
    {#if answer}
      <div class="bb-step">
        <p class="bb-ready"><strong>{T.hostWords[answer.step.readiness] ?? answer.step.readiness}</strong></p>
        <p class="bb-nutshell"><span class="bb-label">{T.hostNutshellLabel}</span> {T.hostNutshell}</p>
        {#if answer.step.reasons?.length}<ul class="bb-list">{#each answer.step.reasons as reason, i (i)}<li>{reason}</li>{/each}</ul>{/if}

        {#if gaps}
          <div class="bb-gap" id="bb-gap-warning" role="note" aria-label={T.hostGapTitle}>
            <strong>{T.hostGapTitle}</strong>
            {#if gaps.shortMinutes > 0}<p>{T.hostGapShort(gaps.shortMinutes)}</p>{/if}
            {#if gaps.longMinutes > 0}<p>{T.hostGapLong(gaps.longMinutes)}</p>{/if}
            <ul class="bb-list" aria-label={T.hostGapList}>
              {#each answer.gaps as gap, i (i)}<li>{shownTime(gap.start, timeZone.zone)} to {shownTime(gap.end, timeZone.zone)}: {T.hostGapMinutes(gap.minutes)} ({gap.window})</li>{/each}
            </ul>
          </div>
        {/if}

        <h4>{T.hostChecks}</h4>
        {#if answer.step.findings?.length}
          <ul class="bb-list">{#each answer.step.findings as finding, i (i)}<li>{finding}</li>{/each}</ul>
        {/if}
        {#if answer.onset}<p class="bb-note">{T.hostOnset} {shownTime(answer.onset, timeZone.zone)}.</p>{/if}
        {#if answer.topProcess}<p class="bb-note">{T.hostTopProcess}: {answer.topProcess}</p>{/if}
        <p class="bb-note">{T.hostMinutes(answer.covered)} {T.hostReadNote(answer.read.files, answer.read.samples)}</p>
        {#if answer.read.torn > 0}<p class="bb-note">{T.hostTorn(answer.read.torn)}</p>{/if}
        {#if answer.read.dropped?.length}<p class="bb-note">{T.hostDropped}</p>{/if}
      </div>
    {/if}
  </div>
</section>

<style>
  .bb-host { display: grid; gap: var(--space-3); min-width: 0; }
  .bb-host h3 { margin: 0; font-size: 14px; font-weight: 600; }
  .bb-host h4 { margin: var(--space-2) 0 0; font-size: 13px; font-weight: 600; }
  .bb-windows summary { width: fit-content; color: var(--text-2); font-size: 13px; }
  .bb-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(210px, 1fr)); gap: var(--space-3); margin: var(--space-2) 0; }
  .bb-grid fieldset { display: grid; gap: var(--space-2); margin: 0; padding: 0; border: 0; min-width: 0; }
  .bb-grid legend { padding: 0 0 4px; font-size: 13px; font-weight: 500; }
  .bb-step { display: grid; gap: var(--space-2); min-width: 0; }
  .bb-ready { margin: 0; font-size: 15px; }
  .bb-nutshell { margin: 0; padding: var(--space-2) var(--space-3); font-size: 13px; background: var(--surface-2); border-radius: var(--radius-sm); }
  .bb-label { font-weight: 600; margin-right: 4px; }
  .bb-list { margin: 0; padding-left: 18px; display: grid; gap: 3px; font-size: 13px; overflow-wrap: anywhere; }
  .bb-gap { display: grid; gap: 4px; padding: var(--space-2) var(--space-3); font-size: 13px; background: color-mix(in srgb, var(--warn) 10%, var(--surface)); border-left: 3px solid var(--warn); border-radius: var(--radius-sm); }
  .bb-gap p { margin: 0; }
</style>
