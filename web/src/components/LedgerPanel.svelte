<script lang="ts">
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api';
  import type { Hypothesis } from '../lib/types';
  import {
    ALT_STATUS_LABEL, ALT_STATUS_ORDER, STATE_LABEL, STATE_ORDER, appendEvidence, buildInput, editorFromHypothesis, historyLine,
    SETTLING_MAX, identifierWarning, isDirty, joinProblem, ledgerCopy as copy, numberProblem, presenceText, rateProblem, stateLabel,
    strongProgress, strongReasons
  } from '../lib/ledger';
  import { theoriesCopy } from '../lib/copy/theoriesCopy';
  import { theoryRows } from '../lib/theories';
  import { ledger, refreshLedger } from '../lib/ledgerStore.svelte';
  import Drawer from './Drawer.svelte';
  import HelpNote from './HelpNote.svelte';

  // The Ledger drawer: what we believe, what supports it, what contradicts it, what is not yet known.
  // The analyst sets every state. Guards can only block "strong correlation"; the app never suggests or computes a state.
  // Esc closes the drawer and Tab stays inside it, like the Glossary drawer.
  // The entries, the drafts, the open entry and the new-hypothesis text live in the shared ledger store, so closing the drawer keeps them.
  let { onClose }: { onClose: () => void } = $props();
  let loading = $state(!ledger.loaded);
  let loadError = $state('');
  let adding = $state(false);
  let addNote = $state('');
  let attached = $state<string | null>(null);
  let busy = $state<Record<string, 'save' | 'reuse' | 'delete' | undefined>>({});
  let problem = $state<Record<string, string>>({});
  let blocked = $state<Record<string, string[]>>({});
  let confirming = $state<string | null>(null);
  let addField = $state<HTMLInputElement>();

  const newWarning = $derived(identifierWarning(ledger.newText));
  // The matrix reads the saved entries, not the drafts, so it never shows a half-typed edit as if it were kept.
  const theories = $derived(theoryRows(ledger.items));
  const messageOf = (error: unknown) => (error instanceof Error ? error.message : 'Something went wrong.');

  function adopt(h: Hypothesis) {
    ledger.editors[h.id] = editorFromHypothesis(h);
    delete blocked[h.id];
    delete problem[h.id];
  }
  function replace(h: Hypothesis) {
    const at = ledger.items.findIndex((x) => x.id === h.id);
    if (at >= 0) ledger.items[at] = h; else ledger.items.unshift(h);
    adopt(h);
  }
  async function load() {
    loading = !ledger.loaded; loadError = '';
    try { await refreshLedger(); }
    catch (error) { loadError = messageOf(error); }
    finally { loading = false; }
  }
  onMount(() => { void load(); addField?.focus(); });

  async function add(event: Event) {
    event.preventDefault();
    const text = ledger.newText.trim();
    if (!text) { addNote = copy.addEmpty; return; }
    if (adding) return;
    adding = true; loadError = '';
    const cid = ledger.caseId;
    try {
      const created = await api.ledgerCreate(text, cid);
      // The person moved to another case while this was saving: the new entry belongs to the old one.
      if (ledger.caseId !== cid) return;
      replace(created);
      ledger.newText = '';
      ledger.openId = created.id;
    } catch (error) { loadError = messageOf(error); }
    finally { adding = false; }
  }
  // The Add button is off while the box is empty, so Enter is the place to say why nothing happened.
  function addKey(event: KeyboardEvent) {
    if (event.key === 'Enter' && ledger.newText.trim() === '') { event.preventDefault(); addNote = copy.addEmpty; }
  }
  function discard(h: Hypothesis) { adopt(h); }
  function attachCited(id: string) {
    const editor = ledger.editors[id];
    if (!editor || !ledger.cited.length) return;
    editor.evidenceText = appendEvidence(editor.evidenceText, ledger.cited.map((c) => c.id));
    ledger.cited = [];
    attached = id;
  }
  async function save(h: Hypothesis) {
    const editor = ledger.editors[h.id];
    if (!editor || busy[h.id]) return;
    busy[h.id] = 'save'; delete problem[h.id]; delete blocked[h.id];
    const cid = ledger.caseId;
    try {
      const saved = await api.ledgerUpdate(h.id, buildInput(editor), cid);
      if (ledger.caseId === cid) replace(saved);
    } catch (error) {
      if (ledger.caseId !== cid) return;
      if (error instanceof ApiError && error.code === 'guard_blocked') {
        blocked[h.id] = error.reasons ?? [];
        editor.state = h.state; // the previous state stays
      } else problem[h.id] = messageOf(error);
    } finally { busy[h.id] = undefined; }
  }
  async function reuse(h: Hypothesis) {
    if (busy[h.id]) return;
    busy[h.id] = 'reuse'; delete problem[h.id];
    const cid = ledger.caseId;
    try {
      const fresh = await api.ledgerReuse(h.id, cid);
      if (ledger.caseId !== cid) return;
      replace(fresh);
      ledger.openId = fresh.id;
    } catch (error) { problem[h.id] = messageOf(error); }
    finally { busy[h.id] = undefined; }
  }
  async function remove(h: Hypothesis) {
    if (busy[h.id]) return;
    busy[h.id] = 'delete'; delete problem[h.id];
    const cid = ledger.caseId;
    try {
      await api.ledgerDelete(h.id, cid);
      if (ledger.caseId !== cid) return;
      ledger.items = ledger.items.filter((x) => x.id !== h.id);
      delete ledger.editors[h.id];
      if (ledger.openId === h.id) ledger.openId = null;
      confirming = null;
    } catch (error) { problem[h.id] = messageOf(error); }
    finally { busy[h.id] = undefined; }
  }
  function toggle(id: string) { ledger.openId = ledger.openId === id ? null : id; confirming = null; attached = null; }

</script>

<Drawer class="ledger-drawer" title={copy.title} titleId="ledger-title" intro={copy.intro} closeLabel="Close ledger" focusable={'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [href], [tabindex]:not([tabindex="-1"])'} {onClose}>
<HelpNote id="ledger" />
  <p class="demo-note">{copy.notice}</p>

  {#if loadError}<p class="inline-error" role="alert">{loadError}</p>{/if}

  {#if loading}
    <p class="field-note" role="status">{copy.loading}</p>
  {:else if ledger.items.length === 0}
    <p class="ledger-empty">{copy.empty}</p>
  {/if}

  {#if ledger.cited.length}
    <section class="ledger-cited" aria-labelledby="ledger-cited-title">
      <h3 id="ledger-cited-title">{copy.citedLead}</h3>
      <p class="field-note">{copy.citedHint}</p>
      <ul>
        {#each ledger.cited as c (c.id)}
          <li>
            <span>{c.label}</span>
            <button type="button" class="quiet-button" aria-label={copy.citedRemove + ': ' + c.label} onclick={() => (ledger.cited = ledger.cited.filter((x) => x.id !== c.id))}>{copy.removeAlternative}</button>
          </li>
        {/each}
      </ul>
    </section>
  {/if}

  {#if !loading && ledger.items.length > 0}
    <details class="ledger-compare">
      <summary>{theoriesCopy.compareToggle}</summary>
      <p class="field-note">{theoriesCopy.compareIntro}</p>
      <table class="theory-matrix" aria-label={theoriesCopy.tableLabel}>
        <thead>
          <tr>
            <th scope="col">{theoriesCopy.columnTheory}</th>
            <th scope="col">{theoriesCopy.columnSupports}</th>
            <th scope="col">{theoriesCopy.columnCannotExplain}</th>
            <th scope="col">{theoriesCopy.columnSettle}</th>
          </tr>
        </thead>
        <tbody>
          {#each theories as row (row.id)}
            <tr>
              <th scope="row" data-label={theoriesCopy.columnTheory}>
                <span class="theory-text">{row.text}</span>
                <span class="theory-state">{row.state}</span>
              </th>
              <td data-label={theoriesCopy.columnSupports}>{row.supports}</td>
              <td data-label={theoriesCopy.columnCannotExplain}>
                {row.cannotExplain}
                {#if row.firstCounter}<span class="theory-first">{theoriesCopy.firstPrefix}{row.firstCounter}</span>{/if}
              </td>
              <td data-label={theoriesCopy.columnSettle}>
                {#if row.settle}{row.settle}{:else}<span class="theory-muted">{theoriesCopy.notWritten}</span>{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </details>
  {/if}

  <ul class="ledger-list">
    {#each ledger.items as h (h.id)}
      {@const editor = ledger.editors[h.id]}
      {@const open = ledger.openId === h.id}
      {@const dirty = !!editor && isDirty(h, editor)}
      {@const rate = presenceText(h.facts.presentK, h.facts.totalN)}
      <li class="ledger-item">
        <button type="button" class="ledger-summary" aria-expanded={open} aria-controls={'ledger-body-' + h.id} onclick={() => toggle(h.id)}>
          <span class="ledger-text">{h.text}</span>
          <span class="ledger-meta">
            <span class="saved-badge ledger-badge" class:ledger-strong={h.state === 'supported_by_strong_correlation'}>{stateLabel(h.state)}</span>
            {#if rate}<span class="ledger-rate">{rate}</span>{/if}
            {#if dirty}<span class="ledger-unsaved">{copy.unsaved}</span>{/if}
          </span>
        </button>
        {#if open && editor}
          {@const rateError = rateProblem(editor)}
          {@const joinError = joinProblem(editor)}
          {@const totalError = numberProblem(editor.totalN)}
          {@const presentError = numberProblem(editor.presentK)}
          {@const strong = editor.state === 'supported_by_strong_correlation'}
          {@const reasons = strong ? strongReasons(editor) : []}
          {@const strongBlocked = reasons.length > 0}
          {@const textWarning = identifierWarning(editor.text)}
          {@const counterWarning = identifierWarning(editor.counterText)}
          {@const notesWarning = identifierWarning(editor.notes)}
          {@const settlingWarning = identifierWarning(editor.settling)}
          <div class="ledger-body source-form" id={'ledger-body-' + h.id}>
            <label for={'l-text-' + h.id}>{copy.textLabel}</label>
            <textarea id={'l-text-' + h.id} rows="2" bind:value={editor.text} aria-describedby={textWarning ? 'l-text-warn-' + h.id : undefined}></textarea>
            {#if textWarning}<p class="warning-text" id={'l-text-warn-' + h.id} role="status">{textWarning}</p>{/if}

            <label for={'l-settling-' + h.id}>{theoriesCopy.settlingLabel}</label>
            <textarea id={'l-settling-' + h.id} rows="2" maxlength={SETTLING_MAX} placeholder={theoriesCopy.settlingPlaceholder} bind:value={editor.settling} aria-describedby={'l-settling-hint-' + h.id}></textarea>
            <p class="field-note" id={'l-settling-hint-' + h.id}>{theoriesCopy.settlingHint} {theoriesCopy.settlingCount(editor.settling.length, SETTLING_MAX)}</p>
            {#if settlingWarning}<p class="warning-text" role="status">{settlingWarning}</p>{/if}

            <label for={'l-state-' + h.id}>{copy.stateLabel}</label>
            <select id={'l-state-' + h.id} bind:value={editor.state} aria-describedby={'l-state-note-' + h.id} onchange={() => delete blocked[h.id]}>
              {#each STATE_ORDER as s (s)}<option value={s}>{STATE_LABEL[s]}</option>{/each}
            </select>
            <p class="field-note" id={'l-state-note-' + h.id}>{copy.stateNote}</p>
            {#if blocked[h.id]}
              <div class="inline-error" role="alert">
                <strong>{copy.blockedLead}</strong>
                <ul class="ledger-reasons">{#each blocked[h.id] as reason (reason)}<li>{reason}</li>{/each}</ul>
                <p class="ledger-blocked-note">{copy.blockedNote}</p>
              </div>
            {:else if strongBlocked}
              <div class="connection-status warning" id={'l-strong-' + h.id}>
                <strong>{copy.guardLead}</strong>
                <ul class="ledger-reasons">{#each reasons as reason (reason)}<li>{reason}</li>{/each}</ul>
              </div>
            {:else if !strong}
              <p class="field-note">{strongProgress(editor)} {copy.strongHint}</p>
            {/if}

            <label for={'l-evidence-' + h.id}>{copy.evidenceLabel}</label>
            <textarea id={'l-evidence-' + h.id} rows="2" spellcheck="false" bind:value={editor.evidenceText} aria-describedby={'l-evidence-hint-' + h.id}></textarea>
            <p class="field-note" id={'l-evidence-hint-' + h.id}>{copy.evidenceHint}</p>
            {#if ledger.cited.length}
              <button type="button" class="quiet-button" onclick={() => attachCited(h.id)}>{copy.citedAdd} ({ledger.cited.length})</button>
            {/if}
            {#if attached === h.id}<p class="field-note" role="status">{copy.citedAdded}</p>{/if}

            <div class="field-grid">
              <div>
                <label for={'l-total-' + h.id}>{copy.examinedLabel}</label>
                <input id={'l-total-' + h.id} type="text" inputmode="numeric" pattern="[0-9]*" autocomplete="off" bind:value={editor.totalN} aria-invalid={totalError ? 'true' : undefined} aria-describedby={totalError ? 'l-numbers-error-' + h.id : undefined} />
              </div>
              <div>
                <label for={'l-present-' + h.id}>{copy.presentLabel}</label>
                <input id={'l-present-' + h.id} type="text" inputmode="numeric" pattern="[0-9]*" autocomplete="off" bind:value={editor.presentK} aria-invalid={presentError || rateError ? 'true' : undefined} aria-describedby={presentError ? 'l-numbers-error-' + h.id : undefined} />
              </div>
            </div>
            <p class="field-note" role="status">{presenceText(Number(editor.presentK), Number(editor.totalN)) || copy.noRate}</p>
            {#if totalError || presentError}
              <div class="warning-text" id={'l-numbers-error-' + h.id} role="alert">
                {#if totalError}<p>{copy.examinedLabel}: {totalError}</p>{/if}
                {#if presentError}<p>{copy.presentLabel}: {presentError}</p>{/if}
              </div>
            {/if}
            {#if rateError}<p class="warning-text" role="alert">{rateError}</p>{/if}

            <label for={'l-counter-' + h.id}>{copy.counterLabel}</label>
            <textarea id={'l-counter-' + h.id} rows="2" bind:value={editor.counterText} aria-describedby={'l-counter-hint-' + h.id}></textarea>
            <p class="field-note" id={'l-counter-hint-' + h.id}>{copy.counterHint}</p>
            {#if counterWarning}<p class="warning-text" role="status">{counterWarning}</p>{/if}

            <span class="field-label" id={'l-alt-label-' + h.id}>{copy.alternativesLabel}</span>
            <div class="ledger-alts" role="group" aria-labelledby={'l-alt-label-' + h.id}>
              {#each editor.alternatives as alt, i (i)}
                <div class="ledger-alt">
                  <input type="text" aria-label={copy.alternativeText} bind:value={alt.text} autocomplete="off" />
                  <select aria-label={copy.alternativeStatus} bind:value={alt.status}>
                    {#each ALT_STATUS_ORDER as s (s)}<option value={s}>{ALT_STATUS_LABEL[s]}</option>{/each}
                  </select>
                  <button type="button" class="quiet-button" onclick={() => editor.alternatives.splice(i, 1)}>{copy.removeAlternative}</button>
                </div>
              {/each}
              <button type="button" class="quiet-button clear-connection" onclick={() => editor.alternatives.push({ text: '', status: 'open' })}>{copy.addAlternative}</button>
            </div>

            <label for={'l-join-' + h.id}>{copy.joinLabel}</label>
            <input id={'l-join-' + h.id} type="text" inputmode="decimal" autocomplete="off" bind:value={editor.joinPct} aria-describedby={'l-join-hint-' + h.id} aria-invalid={joinError ? 'true' : undefined} />
            <p class="field-note" id={'l-join-hint-' + h.id}>{copy.joinHint}</p>
            {#if joinError}<p class="warning-text" role="alert">{joinError}</p>{/if}

            <div class="ledger-checks">
              <label><input type="checkbox" bind:checked={editor.timezoneRecorded} /><span>{copy.timezone}</span></label>
              <label><input type="checkbox" bind:checked={editor.confounderOverlap} /><span>{copy.confounder}</span></label>
              <label><input type="checkbox" bind:checked={editor.coverageGap} /><span>{copy.coverageGap}</span></label>
              <label><input type="checkbox" bind:checked={editor.overlapOnly} /><span>{copy.overlapOnly}</span></label>
            </div>

            <label for={'l-notes-' + h.id}>{copy.notesLabel}</label>
            <textarea id={'l-notes-' + h.id} rows="3" bind:value={editor.notes}></textarea>
            {#if notesWarning}<p class="warning-text" role="status">{notesWarning}</p>{/if}

            {#if h.history?.length}
              <div class="ledger-history">
                <span class="field-label">{copy.historyLabel}</span>
                <p class="field-note">{copy.historyNote}</p>
                <ul>{#each h.history as entry, i (i)}<li>{historyLine(entry)}</li>{/each}</ul>
              </div>
            {/if}

            {#if problem[h.id]}<p class="inline-error" role="alert">{problem[h.id]}</p>{/if}

            {#if dirty}<p class="field-note" role="status">{copy.draftNote}</p>{/if}

            <div class="ledger-actions">
              <button type="button" class="primary-button" disabled={!!busy[h.id] || !!rateError || !!joinError || !!totalError || !!presentError || strongBlocked || editor.text.trim() === ''} aria-describedby={strongBlocked ? 'l-strong-' + h.id + ' l-save-note-' + h.id : undefined} onclick={() => save(h)}>{busy[h.id] === 'save' ? copy.saving : copy.save}</button>
              {#if dirty}<button type="button" class="secondary-button" disabled={!!busy[h.id]} onclick={() => discard(h)}>{copy.discard}</button>{/if}
              <button type="button" class="secondary-button" disabled={!!busy[h.id]} onclick={() => reuse(h)}>{copy.reuse}</button>
              {#if confirming === h.id}
                <span class="ledger-confirm" role="group" aria-label={copy.removeConfirm}>
                  <span>{copy.removeConfirm}</span>
                  <button type="button" class="secondary-button" disabled={!!busy[h.id]} onclick={() => remove(h)}>{copy.removeYes}</button>
                  <button type="button" class="quiet-button" onclick={() => (confirming = null)}>{copy.removeNo}</button>
                </span>
              {:else}
                <button type="button" class="quiet-button" disabled={!!busy[h.id]} onclick={() => (confirming = h.id)}>{copy.remove}</button>
              {/if}
            </div>
            {#if strongBlocked}<p class="field-note" id={'l-save-note-' + h.id}>{copy.saveBlocked}</p>{/if}
            <p class="field-note">{copy.reuseNote}</p>
          </div>
        {/if}
      </li>
    {/each}
  </ul>

  <form class="ledger-add" onsubmit={add}>
    <label for="ledger-new">{copy.addLabel}</label>
    <div class="ledger-add-row">
      <input id="ledger-new" type="text" bind:this={addField} bind:value={ledger.newText} placeholder={copy.addPlaceholder} autocomplete="off" maxlength="2000" oninput={() => (addNote = '')} onkeydown={addKey} aria-describedby={addNote ? 'ledger-new-note' : newWarning ? 'ledger-new-warn' : undefined} />
      <button type="submit" class="primary-button" disabled={adding || ledger.newText.trim() === ''}>{copy.addButton}</button>
    </div>
    {#if addNote}<p class="field-note" id="ledger-new-note" role="status">{addNote}</p>{/if}
    {#if newWarning}<p class="warning-text" id="ledger-new-warn" role="status">{newWarning}</p>{/if}
  </form>
</Drawer>

<style>
  :global(.connections-drawer.ledger-drawer) { overflow-x: hidden; }
  .ledger-compare { margin-top: var(--space-3); padding-top: var(--space-3); border-top: var(--hairline-width) solid var(--hairline); }
  .ledger-compare > summary { color: var(--text); font-size: var(--fs-3); font-weight: 650; }
  .theory-matrix { width: 100%; margin-top: var(--space-2); table-layout: fixed; font-size: 12px; white-space: normal; }
  .theory-matrix :is(th, td) { padding: 6px 6px 6px 0; overflow-wrap: anywhere; text-align: left; vertical-align: top; }
  .theory-matrix thead th { color: var(--text-2); font-weight: 500; border-bottom: 1px solid var(--line); box-shadow: none; background: transparent; }
  .theory-matrix tbody :is(th, td) { border-bottom: 1px solid var(--line-soft); font-weight: 450; }
  .theory-text { display: block; font-weight: 600; }
  .theory-state, .theory-first { display: block; color: var(--text-2); font-size: 11px; }
  .theory-muted { color: var(--text-2); font-style: italic; }
  @media (max-width: 560px) {
    .theory-matrix, .theory-matrix tbody, .theory-matrix tr, .theory-matrix tbody :is(th, td) { display: block; width: 100%; }
    .theory-matrix thead { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
    .theory-matrix tr { padding: 6px 0; border-bottom: 1px solid var(--line-soft); }
    .theory-matrix tbody :is(th, td) { padding: 2px 0; border-bottom: 0; }
    .theory-matrix tbody td::before { content: attr(data-label) ': '; color: var(--text-2); font-weight: 600; }
  }
  .ledger-empty { margin: 14px 0 0; color: var(--text-2); font-size: 13px; }
  .ledger-list { display: grid; margin: 12px 0 0; padding: 0; list-style: none; }
  .ledger-item { border-bottom: 1px solid var(--line-soft); }
  .ledger-summary { display: grid; gap: 5px; width: 100%; padding: 11px 2px; color: var(--text); background: transparent; border: 0; text-align: left; }
  .ledger-summary:hover { background: var(--surface-2); }
  .ledger-text { font-size: 14px; font-weight: 600; overflow-wrap: anywhere; }
  .ledger-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
  .ledger-badge { margin: 0; font-weight: 600; color: var(--text); }
  .ledger-strong { border-color: var(--accent); }
  .ledger-rate { color: var(--text-2); font-size: 12px; font-variant-numeric: tabular-nums; }
  .ledger-unsaved { color: var(--text-2); font-size: var(--fs-2); }
  .ledger-cited { margin-top: var(--space-3); padding-top: var(--space-3); border-top: var(--hairline-width) solid var(--hairline); }
  .ledger-cited h3 { margin: 0; font-size: var(--fs-3); }
  .ledger-cited ul { display: grid; gap: var(--space-1); margin: var(--space-2) 0 0; padding: 0; list-style: none; }
  .ledger-cited li { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--space-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .ledger-body { padding: 4px 2px 16px; }
  .ledger-body textarea { width: 100%; min-width: 0; padding: 7px 9px; color: var(--text); background: var(--surface); border: 1px solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: 14px; line-height: 1.45; resize: vertical; }
  .ledger-body .field-grid > div { display: grid; gap: 4px; min-width: 0; }
  .ledger-reasons { margin: 4px 0 0; padding-left: 18px; font-size: 12px; }
  .ledger-blocked-note { margin: 6px 0 0; font-size: 12px; }
  .ledger-alts { display: grid; gap: 6px; }
  .ledger-alt { display: grid; grid-template-columns: minmax(0, 1fr) auto auto; gap: 6px; align-items: center; }
  .ledger-alt select { width: auto !important; }
  .ledger-checks { display: grid; gap: 4px; margin: 8px 0 4px; }
  .ledger-checks label { display: flex !important; align-items: center; gap: 8px; margin: 0 !important; color: var(--text) !important; font-weight: 450 !important; }
  .ledger-checks input { width: 15px !important; height: 15px; min-height: 0 !important; flex: 0 0 auto; margin: 0; }
  .ledger-history ul { margin: 2px 0 0; padding-left: 18px; color: var(--text-2); font-size: 12px; }
  .ledger-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; margin-top: 10px; }
  .ledger-confirm { display: inline-flex; flex-wrap: wrap; align-items: center; gap: 6px; font-size: 13px; }
  .ledger-add { display: grid; gap: 4px; margin-top: 16px; padding-top: 12px; border-top: 1px solid var(--line); }
  .ledger-add label { color: var(--text-2); font-size: 12px; font-weight: 500; }
  .ledger-add-row { display: flex; gap: 8px; }
  .ledger-add-row input { flex: 1; min-height: 38px; font-size: 14px; }
</style>
