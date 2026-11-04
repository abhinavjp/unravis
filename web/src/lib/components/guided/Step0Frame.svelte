<script lang="ts">
  import { GUIDED_TEXT as T } from '../../copy/guidedCopy';
  import { MAX_RELEASES, frameProblems, widenIncident, windowMinutes, windowsUsable } from '../../guided/frame';
  import type { GuidedStore } from '../../guided/guided.svelte';
  import { fmtCount, fmtWhen } from '../../guided/stepModel';
  import WindowPicker from '../WindowPicker.svelte';
  import StampField from '../StampField.svelte';

  // Step 0: the question. Two windows in UTC, an optional API, an optional pool limit and the change list. The onset found by a run is shown here.
  let { store }: { store: GuidedStore } = $props();

  const problems = $derived(frameProblems(store.frame));
  // While a window is unusable the answer from an earlier run says nothing about it: only the window error is shown (no scope, onset or "out of date" text).
  const usable = $derived(windowsUsable(store.frame));
  const result = $derived(store.result);
  const onset = $derived(result?.facts.onset);
  const routes = $derived(result ? result.facts.routes.map((r) => r.route) : []);
  const scope = $derived.by(() => {
    if (!result || !usable) return '';
    const inc = windowMinutes(store.frame.incidentStart, store.frame.incidentEnd);
    const ref = windowMinutes(store.frame.referenceStart, store.frame.referenceEnd);
    return T.scope(fmtCount(result.facts.counts.used), inc ? inc + ' minutes' : 'not set', ref ? ref + ' minutes' : 'not set');
  });
  let carryOn = $state(false);

  // The quick pick that is in force: it stays pressed only while the incident window is still the one it set.
  type Pick = Parameters<GuidedStore['pick']>[0];
  let picked = $state<{ id: Pick; start: string; end: string } | null>(null);
  const pressed = (id: Pick) => picked?.id === id && picked.start === store.frame.incidentStart && picked.end === store.frame.incidentEnd;
  const quick = (id: Pick) => { store.pick(id, Date.now()); picked = { id, start: store.frame.incidentStart, end: store.frame.incidentEnd }; };

  const addChange = () => { if (store.frame.releases.length < MAX_RELEASES) store.frame.releases.push({ at: '', label: '' }); };
  const removeChange = (i: number) => { store.frame.releases.splice(i, 1); };
</script>

<div class="frame">
  <p class="lead">{T.frameLead}</p>

  <div class="pair windows">
    <div class="col">
      <fieldset>
        <legend>{T.incidentWindow}</legend>
        <WindowPicker variant="inline" label={T.incidentWindow} bind:start={store.frame.incidentStart} bind:end={store.frame.incidentEnd} />
      </fieldset>
      <div class="picks" role="group" aria-label={T.quickPick}>
        <span class="note" aria-hidden="true">{T.quickPick}</span>
        <button type="button" class="secondary-button" aria-pressed={pressed('last-hour')} onclick={() => quick('last-hour')}>{T.lastHour}</button>
        <button type="button" class="secondary-button" aria-pressed={pressed('today')} onclick={() => quick('today')}>{T.today}</button>
        <button type="button" class="secondary-button" aria-pressed={pressed('yesterday')} onclick={() => quick('yesterday')}>{T.yesterday}</button>
      </div>
      <p class="note">{T.quickNote} {T.utcNote}</p>
    </div>
    <div class="col">
      <fieldset>
        <legend>{T.referenceWindow}</legend>
        <WindowPicker variant="inline" label={T.referenceWindow} bind:start={store.frame.referenceStart} bind:end={store.frame.referenceEnd} hint={T.referenceNote} />
      </fieldset>
    </div>
  </div>

  <div class="pair">
    <label>{T.focus}
      <input type="text" list="guided-routes" placeholder={T.allApis} maxlength="256" bind:value={store.frame.route} />
      <datalist id="guided-routes">{#each routes as route (route)}<option value={route}></option>{/each}</datalist>
    </label>
    <label>{T.poolLimit}
      <input type="text" inputmode="numeric" maxlength="6" bind:value={store.frame.poolLimit} />
    </label>
  </div>
  <p class="note">{T.focusNote} {T.poolNote}</p>

  <fieldset>
    <legend>{T.changeList}</legend>
    <p class="note">{T.changeNote}</p>
    {#if store.frame.releases.length === 0}<p class="note">{T.noChanges}</p>{/if}
    {#each store.frame.releases as release, i (i)}
      <div class="change">
        <div class="stampbox" role="group" aria-label={T.changeAt}><span class="lbl">{T.changeAt}</span><StampField bind:value={release.at} label={T.changeAt} /></div>
        <label>{T.changeLabel}<input type="text" maxlength="200" bind:value={release.label} /></label>
        <button type="button" class="quiet-button" onclick={() => removeChange(i)}>{T.removeChange}</button>
      </div>
    {/each}
    <div class="row">
      <button type="button" class="secondary-button" disabled={store.frame.releases.length >= MAX_RELEASES} onclick={addChange}>+ {T.addChange}</button>
      <label class="check"><input type="checkbox" bind:checked={store.frame.releasesEntered} /> {T.changesEntered}</label>
    </div>
    {#if store.frame.releases.length >= MAX_RELEASES}<p class="note" role="status">{T.changesFull}</p>{/if}
    {#if store.frame.releases.length > 0 && !store.frame.releasesEntered}<p class="note" role="status">{T.changesUnticked}</p>{/if}
    <p class="note">{T.changesEnteredNote}</p>
  </fieldset>

  <div class="onset">
    <strong>{T.onset}</strong>
    {#if !usable}
      <!-- Nothing here while a window is unusable: the window error below is the only thing said. -->
    {:else if !result}
      <p class="note">{T.onsetNotRun}</p>
    {:else if onset?.status === 'found'}
      <p>{T.onsetFound(fmtWhen(onset.t0))}</p>
      {#if store.myOnset}<p class="note">{T.myOnset}: {store.myOnset.replace('T', ' ')}. {T.myOnsetNote}</p>{/if}
    {:else}
      <p>{onset?.status === 'not found' ? T.onsetNone : T.onsetCannot}</p>
      {#if onset?.why}<p class="note">{onset.why}</p>{/if}
      {#if carryOn}<p class="note" role="status">{T.carryOnNote}</p>{/if}
      <div class="row">
        <button type="button" class="secondary-button" onclick={() => { store.frame = widenIncident(store.frame, 60); }}>{T.widen}</button>
        <button type="button" class="secondary-button" aria-pressed={carryOn} onclick={() => { carryOn = true; store.goTo(1); }}>{T.carryOn}</button>
      </div>
    {/if}
    <div class="stampbox" role="group" aria-label={T.myOnset}><span class="lbl">{T.myOnset}</span><StampField bind:value={store.myOnset} label={T.myOnset} /></div>
    {#if scope}<p class="note">{scope}</p>{/if}
  </div>

  {#if problems.length > 0}
    <div role="alert"><ul class="problems">{#each problems as problem (problem)}<li>{problem}</li>{/each}</ul></div>
    <p class="note">{T.fixWindows}</p>
  {/if}
  <p class="note">{T.windowsKept}</p>
</div>

<style>
  .frame { display: grid; gap: 12px; min-width: 0; }
  .lead { margin: 0; font-size: 14px; }
  .note { margin: 0; color: var(--text-2); font-size: 12px; }
  fieldset { display: grid; gap: 8px; margin: 0; padding: 12px; border: 1px solid var(--line); border-radius: var(--radius-sm); min-width: 0; }
  legend { padding: 0 6px; font-size: 13px; font-weight: 600; }
  .pair { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 180px), 1fr)); gap: 12px; }
  .pair.windows { grid-template-columns: repeat(auto-fit, minmax(min(100%, 340px), 1fr)); align-items: start; }
  .col { display: grid; gap: 8px; min-width: 0; }
  .stampbox { display: grid; gap: 4px; min-width: 0; }
  .lbl { color: var(--text-2); font-size: 12px; font-weight: 500; }
  label { display: grid; gap: 4px; color: var(--text-2); font-size: 12px; font-weight: 500; min-width: 0; }
  label input[type='text'] { width: 100%; font-variant-numeric: tabular-nums; }
  .picks, .row { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
  .change { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 160px), 1fr)) auto; align-items: end; gap: 8px; }
  .check { display: flex; align-items: center; gap: 6px; }
  .onset { display: grid; gap: 6px; padding: 12px; background: var(--surface-2); border-radius: var(--radius-sm); }
  .onset p { margin: 0; font-size: 13px; }
  .problems { margin: 0; padding: 8px 12px 8px 28px; color: var(--text); font-size: 13px; border: 1px solid var(--bad); border-radius: var(--radius-sm); background: color-mix(in srgb, var(--bad) 10%, var(--surface)); }
</style>
