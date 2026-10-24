<script lang="ts">
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api';
  import type { Hypothesis } from '../lib/types';
  import {
    ALT_STATUS_LABEL, ALT_STATUS_ORDER, STATE_LABEL, STATE_ORDER, buildInput, editorFromHypothesis, historyLine,
    joinProblem, ledgerCopy as copy, presenceText, rateProblem, stateLabel, type EditorState
  } from '../lib/ledger';

  // The Ledger drawer: what we believe, what supports it, what contradicts it, what is not yet known.
  // The analyst sets every state. Guards can only block "strong correlation"; the app never suggests or computes a state.
  // Esc closes the drawer and Tab stays inside it, like the Glossary drawer.
  let { onClose }: { onClose: () => void } = $props();
  let items = $state<Hypothesis[]>([]);
  let loading = $state(true);
  let loadError = $state('');
  let newText = $state('');
  let adding = $state(false);
  let openId = $state<string | null>(null);
  let editors = $state<Record<string, EditorState>>({});
  let busy = $state<Record<string, 'save' | 'reuse' | 'delete' | undefined>>({});
  let problem = $state<Record<string, string>>({});
  let blocked = $state<Record<string, string[]>>({});
  let confirming = $state<string | null>(null);
  let drawer = $state<HTMLElement>();
  let addField = $state<HTMLInputElement>();

  const messageOf = (error: unknown) => (error instanceof Error ? error.message : 'Something went wrong.');

  function adopt(h: Hypothesis) {
    editors[h.id] = editorFromHypothesis(h);
    delete blocked[h.id];
    delete problem[h.id];
  }
  function replace(h: Hypothesis) {
    const at = items.findIndex((x) => x.id === h.id);
    if (at >= 0) items[at] = h; else items.unshift(h);
    adopt(h);
  }
  async function load() {
    loading = true; loadError = '';
    try {
      items = (await api.ledgerList()) ?? [];
      for (const h of items) if (!editors[h.id]) adopt(h);
    } catch (error) { loadError = messageOf(error); }
    finally { loading = false; }
  }
  onMount(() => { void load(); addField?.focus(); });

  async function add(event: Event) {
    event.preventDefault();
    const text = newText.trim();
    if (!text || adding) return;
    adding = true; loadError = '';
    try {
      const created = await api.ledgerCreate(text);
      replace(created);
      newText = '';
      openId = created.id;
    } catch (error) { loadError = messageOf(error); }
    finally { adding = false; }
  }
  async function save(h: Hypothesis) {
    const editor = editors[h.id];
    if (!editor || busy[h.id]) return;
    busy[h.id] = 'save'; delete problem[h.id]; delete blocked[h.id];
    try {
      replace(await api.ledgerUpdate(h.id, buildInput(editor)));
    } catch (error) {
      if (error instanceof ApiError && error.code === 'guard_blocked') {
        blocked[h.id] = error.reasons ?? [];
        editor.state = h.state; // the previous state stays
      } else problem[h.id] = messageOf(error);
    } finally { busy[h.id] = undefined; }
  }
  async function reuse(h: Hypothesis) {
    if (busy[h.id]) return;
    busy[h.id] = 'reuse'; delete problem[h.id];
    try {
      const fresh = await api.ledgerReuse(h.id);
      replace(fresh);
      openId = fresh.id;
    } catch (error) { problem[h.id] = messageOf(error); }
    finally { busy[h.id] = undefined; }
  }
  async function remove(h: Hypothesis) {
    if (busy[h.id]) return;
    busy[h.id] = 'delete'; delete problem[h.id];
    try {
      await api.ledgerDelete(h.id);
      items = items.filter((x) => x.id !== h.id);
      delete editors[h.id];
      if (openId === h.id) openId = null;
      confirming = null;
    } catch (error) { problem[h.id] = messageOf(error); }
    finally { busy[h.id] = undefined; }
  }
  function toggle(id: string) { openId = openId === id ? null : id; confirming = null; }

  function onKey(event: KeyboardEvent) {
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); onClose(); return; }
    if (event.key !== 'Tab' || !drawer) return;
    const focusable = [...drawer.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [href], [tabindex]:not([tabindex="-1"])')];
    if (!focusable.length) return;
    const first = focusable[0], last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }
</script>

<button type="button" class="drawer-scrim" aria-label="Close ledger" onclick={onClose}></button>
<div class="connections-drawer ledger-drawer" bind:this={drawer} onkeydown={onKey} role="dialog" aria-modal="true" aria-labelledby="ledger-title" tabindex="-1">
  <div class="drawer-header"><div><h2 id="ledger-title">{copy.title}</h2><p>{copy.intro}</p></div><button type="button" class="icon-button drawer-close" aria-label="Close ledger" onclick={onClose}>×</button></div>
  <p class="demo-note">{copy.notice}</p>

  {#if loadError}<p class="inline-error" role="alert">{loadError}</p>{/if}

  {#if loading}
    <p class="field-note" role="status">{copy.loading}</p>
  {:else if items.length === 0}
    <p class="ledger-empty">{copy.empty}</p>
  {/if}

  <ul class="ledger-list">
    {#each items as h (h.id)}
      {@const editor = editors[h.id]}
      {@const open = openId === h.id}
      {@const rate = presenceText(h.facts.presentK, h.facts.totalN)}
      <li class="ledger-item">
        <button type="button" class="ledger-summary" aria-expanded={open} aria-controls={'ledger-body-' + h.id} onclick={() => toggle(h.id)}>
          <span class="ledger-text">{h.text}</span>
          <span class="ledger-meta">
            <span class="saved-badge ledger-badge" class:ledger-strong={h.state === 'supported_by_strong_correlation'}>{stateLabel(h.state)}</span>
            {#if rate}<span class="ledger-rate">{rate}</span>{/if}
          </span>
        </button>
        {#if open && editor}
          {@const rateError = rateProblem(editor)}
          {@const joinError = joinProblem(editor)}
          <div class="ledger-body source-form" id={'ledger-body-' + h.id}>
            <label for={'l-text-' + h.id}>{copy.textLabel}</label>
            <textarea id={'l-text-' + h.id} rows="2" bind:value={editor.text}></textarea>

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
            {:else if h.guards?.length}
              <div class="connection-status warning">
                <strong>{copy.guardLead}</strong>
                <ul class="ledger-reasons">{#each h.guards as reason (reason)}<li>{reason}</li>{/each}</ul>
              </div>
            {/if}

            <label for={'l-evidence-' + h.id}>{copy.evidenceLabel}</label>
            <textarea id={'l-evidence-' + h.id} rows="2" spellcheck="false" bind:value={editor.evidenceText} aria-describedby={'l-evidence-hint-' + h.id}></textarea>
            <p class="field-note" id={'l-evidence-hint-' + h.id}>{copy.evidenceHint}</p>

            <div class="field-grid">
              <div>
                <label for={'l-total-' + h.id}>{copy.examinedLabel}</label>
                <input id={'l-total-' + h.id} type="text" inputmode="numeric" autocomplete="off" bind:value={editor.totalN} />
              </div>
              <div>
                <label for={'l-present-' + h.id}>{copy.presentLabel}</label>
                <input id={'l-present-' + h.id} type="text" inputmode="numeric" autocomplete="off" bind:value={editor.presentK} aria-invalid={rateError ? 'true' : undefined} />
              </div>
            </div>
            <p class="field-note" role="status">{presenceText(Number(editor.presentK), Number(editor.totalN)) || copy.noRate}</p>
            {#if rateError}<p class="warning-text" role="alert">{rateError}</p>{/if}

            <label for={'l-counter-' + h.id}>{copy.counterLabel}</label>
            <textarea id={'l-counter-' + h.id} rows="2" bind:value={editor.counterText} aria-describedby={'l-counter-hint-' + h.id}></textarea>
            <p class="field-note" id={'l-counter-hint-' + h.id}>{copy.counterHint}</p>

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

            {#if h.history?.length}
              <div class="ledger-history">
                <span class="field-label">{copy.historyLabel}</span>
                <p class="field-note">{copy.historyNote}</p>
                <ul>{#each h.history as entry, i (i)}<li>{historyLine(entry)}</li>{/each}</ul>
              </div>
            {/if}

            {#if problem[h.id]}<p class="inline-error" role="alert">{problem[h.id]}</p>{/if}

            <div class="ledger-actions">
              <button type="button" class="primary-button" disabled={!!busy[h.id] || !!rateError || !!joinError || editor.text.trim() === ''} onclick={() => save(h)}>{busy[h.id] === 'save' ? copy.saving : copy.save}</button>
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
            <p class="field-note">{copy.reuseNote}</p>
          </div>
        {/if}
      </li>
    {/each}
  </ul>

  <form class="ledger-add" onsubmit={add}>
    <label for="ledger-new">{copy.addLabel}</label>
    <div class="ledger-add-row">
      <input id="ledger-new" type="text" bind:this={addField} bind:value={newText} placeholder={copy.addPlaceholder} autocomplete="off" maxlength="2000" />
      <button type="submit" class="primary-button" disabled={adding || newText.trim() === ''}>{copy.addButton}</button>
    </div>
  </form>
</div>

<style>
  .ledger-drawer { overflow-x: hidden; }
  .ledger-empty { margin: 14px 0 0; color: var(--text-2); font-size: 13px; }
  .ledger-list { display: grid; margin: 12px 0 0; padding: 0; list-style: none; }
  .ledger-item { border-bottom: 1px solid var(--line-soft); }
  .ledger-summary { display: grid; gap: 5px; width: 100%; padding: 11px 2px; color: var(--text); background: transparent; border: 0; text-align: left; }
  .ledger-summary:hover { background: var(--surface-2); }
  .ledger-text { font-size: 14px; font-weight: 600; overflow-wrap: anywhere; }
  .ledger-meta { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
  .ledger-badge { margin: 0; font-weight: 650; color: var(--text); }
  .ledger-strong { border-color: var(--accent); }
  .ledger-rate { color: var(--text-2); font-size: 12px; font-variant-numeric: tabular-nums; }
  .ledger-body { padding: 4px 2px 16px; }
  .ledger-body textarea { width: 100%; min-width: 0; padding: 7px 9px; color: var(--text); background: var(--surface); border: 1px solid var(--control-line); border-radius: var(--radius); font: inherit; font-size: 14px; line-height: 1.45; resize: vertical; }
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
  .ledger-add label { color: var(--text-2); font-size: 12px; font-weight: 650; }
  .ledger-add-row { display: flex; gap: 8px; }
  .ledger-add-row input { flex: 1; min-height: 38px; font-size: 14px; }
</style>
