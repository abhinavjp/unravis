<script lang="ts">
  import { untrack } from 'svelte';
  import { GUIDED_TEXT as T } from '../../copy/guidedCopy';
  import { SHELL_TEXT as S } from '../../copy/guidedShellCopy';
  import type { GuidedStore } from '../../guided/guided.svelte';
  import { fmtCount } from '../../guided/stepModel';
  import {
    applyViewToFrame, itemTooLong, NEED_WORD, readViewSettings, slowestApiRows, toolById, toolSticker, toolState, TOOLS, type SourceNeed, type ToolDef, type ToolId
  } from '../../guided/tools';
  import type { SavedView } from '../../guided/types';
  import AfterFix from './AfterFix.svelte';
  import NearbyActivity from './NearbyActivity.svelte';
  import RunBar from './RunBar.svelte';
  import SavedViews from './SavedViews.svelte';
  import SlowCalls from './SlowCalls.svelte';
  import Step0Frame from './Step0Frame.svelte';
  import Step10Origin from './Step10Origin.svelte';
  import StepFacts from './StepFacts.svelte';
  import Sticker from './Sticker.svelte';

  // Manual tools: the same facts as the steps, one tool at a time, in any order. A tool whose source is missing says so and shows nothing.
  let { store, sourceIds, readOnly, have, onAddData, onOpenChecklist }: {
    store: GuidedStore; sourceIds: string[]; readOnly: boolean; have: ReadonlySet<SourceNeed>; onAddData: () => void; onOpenChecklist: () => void;
  } = $props();

  const stateOf = (tool: ToolDef) => toolState(tool, { have, result: store.result });
  // The same flag the run bar and the step stickers read, so a card says Out of date when the windows, sources or lists have changed.
  const stale = $derived(store.outOfDate(sourceIds));

  // The windows start open while empty and stay as the person left them; a bound
  // `open={frame.incidentStart === ''}` would fold the panel on the first keystroke.
  let windowsOpen = $state(untrack(() => store.frame.incidentStart === ''));
  let minCalls = $state(20);
  let match = $state('');
  let noted = $state(false);

  const open = $derived(store.tool ? (toolById(store.tool) ?? null) : null);
  const openState = $derived(open ? stateOf(open) : null);
  const openSticker = $derived(openState ? toolSticker(openState, stale) : null);
  const apiRows = $derived(slowestApiRows(store.result, Number(minCalls), match));
  // A text that matches no API at all is a different problem from a minimum that is too high.
  const noMatch = $derived(match.trim() !== '' && slowestApiRows(store.result, 0, match).length === 0);
  const noteOver = $derived(itemTooLong(store.noteDraft));

  const openTool = (id: ToolId) => { store.tool = id; noted = false; };
  const back = () => { store.tool = ''; };

  function openView(view: SavedView) {
    const settings = readViewSettings(view.settings);
    store.frame = applyViewToFrame(store.frame, settings);
    if (typeof settings.minCalls === 'number') minCalls = settings.minCalls;
    if (typeof settings.match === 'string') match = settings.match;
    store.tool = toolById(view.tool) ? (view.tool as ToolId) : '';
  }

  async function addNote() {
    if (await store.addItem('learned', store.noteDraft)) { store.noteDraft = ''; noted = true; }
  }
</script>

<section class="tools" aria-labelledby="tools-title">
  {#if !open || !openState}
    <header><h3 id="tools-title">{T.toolsHeading}</h3></header>
    <p class="lead">{T.toolsLead}</p>
    <ul class="cards">
      {#each TOOLS as tool (tool.id)}
        {@const st = stateOf(tool)}
        {@const sk = toolSticker(st, stale)}
        <li class="card">
          <div class="top"><h4>{tool.title}</h4><Sticker word={sk.word} tone={sk.tone} outOfDate={sk.outOfDate} /></div>
          <p class="blurb">{tool.blurb}</p>
          <p class="src">{T.sourcesLabel}: {tool.sources}</p>
          {#if st.word === 'Cannot test yet' && st.missing.length > 0}<p class="src">{T.toolNeedsHint}</p>{/if}
          {#if st.word === 'Waiting'}<p class="src">{S.toolWaitingHint}</p>{/if}
          <div><button type="button" class="secondary-button" onclick={() => openTool(tool.id)}>{T.openTool}: {tool.title}</button></div>
        </li>
      {/each}
    </ul>
    <SavedViews {store} {readOnly} onOpen={openView} />
  {:else}
    <header class="head">
      <button type="button" class="quiet-button" onclick={back}>&larr; {T.backToTools}</button>
      <h3 id="tools-title">{open.title}</h3>
      {#if openSticker}<Sticker word={openSticker.word} tone={openSticker.tone} outOfDate={openSticker.outOfDate} />{/if}
    </header>
    <p class="lead">{open.blurb}</p>

    {#if openState.missing.length > 0}
      <div class="missing">
        <h4>{T.missingTitle}</h4>
        <p>{T.missingLead(NEED_WORD[openState.missing[0]])}</p>
        <div><button type="button" class="secondary-button" disabled={readOnly} onclick={onAddData}>{T.addByLocation}</button></div>
      </div>
    {:else}
      <details class="windows" bind:open={windowsOpen}>
        <summary>{T.incidentWindow} and {T.referenceWindow}</summary>
        <Step0Frame {store} />
      </details>
      {#if open.id !== 'slowest-calls' && open.id !== 'nearby-activity'}
        <RunBar {store} {sourceIds} {readOnly} {onAddData} />
      {/if}

      {#if open.id === 'suspect-summary'}
        <Step10Origin {store} {sourceIds} {readOnly} {onOpenChecklist} />
      {:else if open.id === 'slowest-apis'}
        <div class="api">
          <div class="form">
            <label>{T.minCalls}<input type="number" min="0" step="1" bind:value={minCalls} /></label>
            <label>{T.onlyMatching}<input type="text" maxlength="100" bind:value={match} /></label>
          </div>
          {#if !store.result}
            <p class="note">{T.jumpedAhead}</p>
          {:else if store.result.facts.routes.length === 0}
            <p class="note">{T.noRecords}</p>
          {:else if apiRows.length === 0}
            <p class="note">{noMatch ? S.noApiMatch(match.trim()) : T.noApiRows(Number(minCalls))}</p>
          {:else}
            <div class="table-scroll">
              <table>
                <thead><tr><th scope="col">{T.apiCol}</th><th scope="col">{T.callsCol}</th><th scope="col">{T.p95Col}</th><th scope="col">{T.refP95Col}</th><th scope="col">{T.changeCol}</th></tr></thead>
                <tbody>
                  {#each apiRows as row (row.route)}
                    {@const mark = row.mark}
                    <tr><td>{row.route}</td><td>{fmtCount(row.calls)}</td><td>{row.p95}</td><td>{row.reference}</td><td><span class={'mark ' + mark.tone}><span aria-hidden="true">{mark.symbol}</span> {mark.word}</span> {row.change}</td></tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        </div>
      {:else if open.id === 'slowest-calls'}
        <SlowCalls {store} {sourceIds} />
      {:else if open.id === 'nearby-activity'}
        <NearbyActivity {store} {sourceIds} />
      {:else if open.id === 'after-fix'}
        <AfterFix {store} {sourceIds} {readOnly} />
      {:else if open.step !== undefined}
        <StepFacts {store} {sourceIds} step={open.step} {readOnly} {onAddData} />
      {/if}
    {/if}

    {#if !readOnly}
      <form class="note-form" onsubmit={(e) => { e.preventDefault(); void addNote(); }}>
        <label>{T.addNote}<input type="text" bind:value={store.noteDraft} aria-invalid={noteOver} /></label>
        {#if noteOver}<p class="note" role="status">{T.itemTooLong}</p>{/if}
        <div class="row">
          <button type="submit" class="secondary-button" disabled={store.noteDraft.trim() === '' || noteOver}>{T.itemSave}</button>
          {#if noted}<span class="note" role="status">{T.noteAdded}</span>{/if}
        </div>
      </form>
    {/if}

    <SavedViews {store} {readOnly} tool={open.id} extra={{ minCalls: open.id === 'slowest-apis' ? Number(minCalls) : undefined, match: open.id === 'slowest-apis' ? match : undefined }} onOpen={openView} />
    <div><button type="button" class="quiet-button" onclick={() => store.setMode('guided')}>{T.runGuided}</button></div>
  {/if}
</section>

<style>
  .tools { display: grid; gap: 12px; min-width: 0; }
  h3 { margin: 0; font-size: 16px; overflow-wrap: anywhere; }
  h4 { margin: 0; font-size: 14px; }
  .lead, .note { margin: 0; font-size: 13px; }
  .note { color: var(--text-2); font-size: 12px; }
  .head { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; }
  .cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 260px), 1fr)); gap: 12px; margin: 0; padding: 0; list-style: none; }
  .card { display: grid; align-content: start; gap: 8px; padding: 12px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); min-width: 0; }
  .top { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 4px 8px; }
  .blurb { margin: 0; font-size: 13px; }
  .src { margin: 0; color: var(--text-2); font-size: 12px; }
  .missing { display: grid; gap: 8px; padding: 12px; border: 1px solid var(--line); border-left: 4px solid var(--suspect-grey); border-radius: var(--radius-sm); background: var(--surface); }
  .missing p { margin: 0; font-size: 13px; }
  .windows { padding: 8px 12px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .windows summary { cursor: pointer; font-size: 13px; font-weight: 600; }
  .api { display: grid; gap: 12px; padding: 16px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .form { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 1fr)); gap: 8px; }
  .form label, .note-form label { display: grid; gap: 4px; font-size: 13px; }
  .note-form { display: grid; gap: 8px; }
  .row { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
  table { white-space: normal; }
  .mark { font-weight: 600; white-space: nowrap; }
  .mark.bad { color: var(--suspect-red); }
  .mark.ok { color: var(--suspect-green); }
  .mark.grey { color: var(--text-2); }
</style>
