<script lang="ts">
  import { onMount } from 'svelte';
  import { caseErased } from '../appState.svelte';
  import { CaseApiError, caseCode, closeCase, createCase, eraseCase, eraseConfirmed, isCaseGone, listCases, type CaseInfo } from '../cases';
  import { ERASE_DONE, IMPORT_TEXT, caseStats, errorText, erasePrompt } from '../copy/importCopy';

  // The case list: pick a case, create one, close one, or erase one. Erase asks for the case code to be typed, then shows the one storage sentence.
  // The parent owns the list and the choice (both bindable) and calls refresh() after an import changes a case's size.
  // `onClosed` runs after a case is closed, so the app can go back to the start screen, where the closed case is labelled.
  let { cases = $bindable<CaseInfo[]>([]), selectedId = $bindable(''), onChanged, onClosed }: { cases?: CaseInfo[]; selectedId?: string; onChanged?: () => void; onClosed?: () => void } = $props();

  let loading = $state(true);
  let loadFailed = $state(false);
  let busy = $state(false);
  let title = $state('');
  let problem = $state('');
  let erased = $state(false);
  let eraseId = $state('');
  let typed = $state('');
  let controller: AbortController | null = null;

  const nameOf = (c: CaseInfo) => c.title || 'Case ' + caseCode(c.id);
  const confirmed = $derived(eraseId !== '' && eraseConfirmed(typed, eraseId));

  function fail(error: unknown) {
    if (error instanceof DOMException && error.name === 'AbortError') return;
    if (isCaseGone(error)) { void noteGone(); return; }
    problem = error instanceof CaseApiError ? errorText(error) : errorText({ status: 0, reason: 'unreachable', message: '' });
  }

  /** The case was erased elsewhere: reload the list so it stops offering that case, and say why it vanished. */
  export async function noteGone() {
    await refresh();
    erased = false; eraseId = ''; typed = '';
    problem = errorText({ status: 404, reason: 'case-not-found', message: '' });
  }

  export async function refresh() {
    try {
      cases = await listCases(controller?.signal);
      if (selectedId && !cases.some((c) => c.id === selectedId)) selectedId = '';
      loadFailed = false;
    } catch (error) { loadFailed = true; fail(error); }
  }

  onMount(() => {
    controller = new AbortController();
    refresh().finally(() => { loading = false; });
    return () => controller?.abort();
  });

  async function create(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    if (!title.trim()) { problem = IMPORT_TEXT.titleMissing; return; }
    busy = true; problem = ''; erased = false;
    try {
      const made = await createCase(title, controller?.signal);
      title = '';
      await refresh();
      selectedId = made.id;
      onChanged?.();
    } catch (error) { fail(error); } finally { busy = false; }
  }

  async function close(c: CaseInfo) {
    if (busy) return;
    busy = true; problem = ''; erased = false;
    try { await closeCase(c.id, controller?.signal); await refresh(); onChanged?.(); onClosed?.(); } catch (error) { fail(error); } finally { busy = false; }
  }

  function askErase(c: CaseInfo) { eraseId = c.id; typed = ''; problem = ''; erased = false; }
  function cancelErase() { eraseId = ''; typed = ''; }

  async function erase(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !confirmed) return;
    const id = eraseId;
    busy = true; problem = '';
    try {
      await eraseCase(id, controller?.signal);
      eraseId = ''; typed = '';
      if (selectedId === id) selectedId = '';
      caseErased(id);
      erased = true;
      await refresh();
      onChanged?.();
    } catch (error) { fail(error); } finally { busy = false; }
  }
</script>

<section class="cases" aria-labelledby="case-heading">
  <h3 id="case-heading">{IMPORT_TEXT.caseHeading}</h3>

  {#if loading}
    <p class="note" role="status">{IMPORT_TEXT.loadingCases}</p>
  {:else if cases.length === 0}
    <!-- A list that failed to load is not an empty list; the error below says why. -->
    {#if !loadFailed}<p class="note">{IMPORT_TEXT.noCases}</p>{/if}
  {:else}
    <ul class="case-list">
      {#each cases as c (c.id)}
        <li class:on={c.id === selectedId}>
          <div class="case-row">
            <div class="case-main">
              <strong>{nameOf(c)}</strong>
              <span class="meta"><code>{caseCode(c.id)}</code> · {caseStats(c)}{#if c.closed} · <span class="badge">{IMPORT_TEXT.closedBadge}</span>{/if}</span>
            </div>
            <div class="case-actions">
              {#if c.id === selectedId}
                <span class="in-use">{IMPORT_TEXT.inUse}</span>
              {:else}
                <button type="button" class="secondary-button" disabled={busy} aria-label={IMPORT_TEXT.useCase + ': ' + nameOf(c)} onclick={() => { selectedId = c.id; erased = false; }}>{IMPORT_TEXT.useCase}</button>
              {/if}
              {#if !c.closed}
                <button type="button" class="secondary-button" disabled={busy} aria-label={IMPORT_TEXT.closeCase + ': ' + nameOf(c)} onclick={() => close(c)}>{IMPORT_TEXT.closeCase}</button>
              {/if}
              <button type="button" class="secondary-button danger" disabled={busy} aria-label={IMPORT_TEXT.eraseCase + ': ' + nameOf(c)} aria-expanded={eraseId === c.id} onclick={() => askErase(c)}>{IMPORT_TEXT.eraseCase}</button>
            </div>
          </div>
          {#if eraseId === c.id}
            <form class="erase" onsubmit={erase}>
              <p>{IMPORT_TEXT.eraseWarning}</p>
              <label for="erase-typed">{erasePrompt(caseCode(c.id))}</label>
              <input id="erase-typed" type="text" bind:value={typed} autocomplete="off" spellcheck="false" />
              <div class="erase-actions">
                <button type="submit" class="primary-button danger-fill" disabled={busy || !confirmed}>{busy ? IMPORT_TEXT.erasing : IMPORT_TEXT.eraseConfirmButton}</button>
                <button type="button" class="secondary-button" disabled={busy} onclick={cancelErase}>{IMPORT_TEXT.eraseCancel}</button>
              </div>
            </form>
          {/if}
        </li>
      {/each}
    </ul>
    {#if cases.some((c) => c.closed)}<p class="note">{IMPORT_TEXT.closeNote}</p>{/if}
  {/if}

  <div aria-live="polite">
    {#if erased}<p class="erased" role="status">{ERASE_DONE}</p>{/if}
    {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
  </div>

  <form class="new-case" onsubmit={create}>
    <label for="new-case-title">{IMPORT_TEXT.newCaseLabel}</label>
    <div class="new-case-row">
      <input id="new-case-title" type="text" bind:value={title} placeholder={IMPORT_TEXT.newCasePlaceholder} maxlength="120" autocomplete="off" />
      <button type="submit" class="primary-button" disabled={busy}>{busy && !eraseId ? IMPORT_TEXT.creating : IMPORT_TEXT.createCase}</button>
    </div>
  </form>
</section>

<style>
  .cases { display: grid; gap: var(--space-3); min-width: 0; }
  h3 { margin: 0; font-size: var(--fs-4); font-weight: 600; letter-spacing: -.01em; }
  .note { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; }
  .case-list { display: grid; gap: 0; margin: 0; padding: 0; list-style: none; }
  .case-list li { min-width: 0; margin-inline: calc(var(--space-2) * -1); padding: var(--space-3) var(--space-2); background: transparent; border: 0; border-top: 1px solid var(--line-soft); border-radius: 0; }
  .case-list li.on { background: var(--surface-2); box-shadow: inset 0 0 0 1px var(--accent); }
  .case-row { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--space-2) var(--space-3); }
  .case-main { display: grid; gap: 2px; min-width: 0; }
  .case-main strong { font-size: var(--fs-3); overflow-wrap: anywhere; }
  .meta { color: var(--text-2); font-size: var(--fs-2); }
  .meta code { font-family: var(--mono); }
  .badge, .in-use { padding: 1px 7px; color: var(--text-2); border: 1px solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-2); font-weight: 500; }
  .in-use { color: var(--text); border-color: var(--accent); }
  .case-actions { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .case-actions :global(button) { min-height: 30px; padding: 4px 10px; }
  .danger { color: var(--bad); border-color: var(--bad); }
  .danger-fill { background: var(--bad); border-color: var(--bad); color: var(--bg); }
  .danger-fill:disabled { opacity: .5; }
  .erase { display: grid; gap: var(--space-2); margin-top: var(--space-3); padding-top: var(--space-3); border-top: 1px solid var(--line); }
  .erase p { margin: 0; font-size: var(--fs-3); line-height: 1.5; }
  .erase label, .new-case label { color: var(--text-2); font-size: var(--fs-2); font-weight: 500; }
  .erase input { width: 100%; font-family: var(--mono); }
  .erase-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .erased { margin: 0; padding: 8px 0; border: 0; border-top: 1px solid var(--line); border-radius: 0; background: transparent; font-size: var(--fs-3); }
  .new-case { display: grid; gap: 4px; }
  .new-case-row { display: flex; gap: var(--space-2); }
  .new-case-row input { flex: 1; min-width: 0; }
</style>
