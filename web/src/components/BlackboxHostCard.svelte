<script lang="ts">
  import { blackboxApi, gapSummary, refusalText, shownTime, type BlackboxItem, type HostAnswer } from '../lib/blackbox';
  import { BB_REFUSAL, BB_TEXT as T } from '../lib/copy/blackboxCopy';

  // Step 9, host and network, read from one Blackbox for two windows. A gap of 2 minutes or less warns and the check
  // runs on the minutes that exist. A longer gap makes the minutes inside it Cannot test yet. Missing minutes are never zero.
  let { item, initial = null }: { item: BlackboxItem; initial?: HostAnswer | null } = $props();

  const MINUTE = 60_000;
  const local = (t: number) => new Date(t).toISOString().slice(0, 16);
  const anchor = (() => {
    const t = Date.parse(item.facts?.lastSample ?? '');
    return Number.isFinite(t) ? Math.floor(t / MINUTE) * MINUTE : 0;
  })();

  let incStart = $state(anchor ? local(anchor - 30 * MINUTE) : '');
  let incEnd = $state(anchor ? local(anchor) : '');
  let refStart = $state(anchor ? local(anchor - 60 * MINUTE) : '');
  let refEnd = $state(anchor ? local(anchor - 30 * MINUTE) : '');
  // svelte-ignore state_referenced_locally
  let answer = $state<HostAnswer | null>(initial);
  let busy = $state(false);
  let problem = $state('');

  const gaps = $derived(gapSummary(answer?.gaps));
  const toIso = (v: string) => v ? v + ':00Z' : '';

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
    if (!windowsOk()) { problem = T.hostWindowProblem; return; }
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
  <h4>{T.hostTitle}</h4>
  <p class="bb-note">{T.hostQuestion}</p>

  <details class="bb-windows" open={!answer}>
    <summary>{T.hostRead}: {item.name}</summary>
    <div class="bb-grid">
      <fieldset><legend>{T.hostIncident} (UTC)</legend>
        <label class="bb-field"><span>{T.hostStart}</span><input type="datetime-local" bind:value={incStart} /></label>
        <label class="bb-field"><span>{T.hostEnd}</span><input type="datetime-local" bind:value={incEnd} /></label>
      </fieldset>
      <fieldset><legend>{T.hostReference} (UTC)</legend>
        <label class="bb-field"><span>{T.hostStart}</span><input type="datetime-local" bind:value={refStart} /></label>
        <label class="bb-field"><span>{T.hostEnd}</span><input type="datetime-local" bind:value={refEnd} /></label>
      </fieldset>
    </div>
    <button type="button" class="secondary-button" disabled={busy} onclick={read}>{busy ? T.hostRunning : T.hostRun}</button>
  </details>

  <div aria-live="polite">
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
              {#each answer.gaps as gap, i (i)}<li>{shownTime(gap.start)} to {shownTime(gap.end)}: {T.hostGapMinutes(gap.minutes)} ({gap.window})</li>{/each}
            </ul>
          </div>
        {/if}

        <h5>{T.hostChecks}</h5>
        {#if answer.step.findings?.length}
          <ul class="bb-list">{#each answer.step.findings as finding, i (i)}<li>{finding}</li>{/each}</ul>
        {/if}
        {#if answer.onset}<p class="bb-note">{T.hostOnset} {shownTime(answer.onset)}.</p>{/if}
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
  .bb-host h4 { margin: 0; font-size: 14px; font-weight: 600; }
  .bb-host h5 { margin: var(--space-2) 0 0; font-size: 13px; font-weight: 600; }
  .bb-note { margin: 0; color: var(--text-2); font-size: 12px; line-height: 1.5; overflow-wrap: anywhere; }
  .bb-windows summary { width: fit-content; color: var(--text-2); font-size: 13px; }
  .bb-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(210px, 1fr)); gap: var(--space-3); margin: var(--space-2) 0; }
  .bb-grid fieldset { display: grid; gap: var(--space-2); margin: 0; padding: 0; border: 0; min-width: 0; }
  .bb-grid legend { padding: 0 0 4px; font-size: 13px; font-weight: 500; }
  .bb-field { display: grid; gap: 2px; font-size: 12px; color: var(--text-2); }
  .bb-field input { padding: 5px 8px; font: inherit; font-size: 13px; color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); box-sizing: border-box; max-width: 100%; }
  .bb-step { display: grid; gap: var(--space-2); min-width: 0; }
  .bb-ready { margin: 0; font-size: 15px; }
  .bb-nutshell { margin: 0; padding: var(--space-2) var(--space-3); font-size: 13px; background: var(--surface-2); border-radius: var(--radius-sm); }
  .bb-label { font-weight: 600; margin-right: 4px; }
  .bb-list { margin: 0; padding-left: 18px; display: grid; gap: 3px; font-size: 13px; overflow-wrap: anywhere; }
  .bb-gap { display: grid; gap: 4px; padding: var(--space-2) var(--space-3); font-size: 13px; background: color-mix(in srgb, var(--warn) 10%, var(--surface)); border-left: 3px solid var(--warn); border-radius: var(--radius-sm); }
  .bb-gap p { margin: 0; }
</style>
