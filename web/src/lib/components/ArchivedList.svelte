<script lang="ts">
  import { onMount } from 'svelte';
  import { listArchivedCases } from '../archiveApi';
  import { archivedActions, archivedChips, keyEndingOf, type ArchiveDrawerKind } from '../archiveModel';
  import { caseErased } from '../appState.svelte';
  import { CaseApiError, believedState, caseCode, eraseCase, eraseConfirmed, isCaseGone, type CaseInfo } from '../cases';
  import { ARCHIVE_TEXT, archiveCounts, archiveReasonText, archivedShownText, keyEndingText } from '../copy/archiveCopy';
  import { ERASE_DONE, IMPORT_TEXT, erasePrompt } from '../copy/importCopy';
  import { caseRow } from '../startScreen';
  import { formatTime } from '../time';
  import ArchiveDrawers from './ArchiveDrawers.svelte';

  // The archived cases, 50 at a time, apart from the working list. Each row says how it is kept (working copy kept, space freed, or missing) and offers
  // the ways back. Erase asks for the case code, as everywhere. Nothing here deletes a case file.
  let { total, onOpen, onBack }: {
    /** How many archived cases there are, from the working list's answer; the list replaces it once it has loaded. */
    total: number;
    onOpen: (id: string) => void;
    onBack: () => void;
  } = $props();

  let phase = $state<'loading' | 'ready' | 'failed'>('loading');
  let rows = $state.raw<CaseInfo[]>([]);
  let count = $state(0);
  let loadingMore = $state(false);
  let moreFailed = $state(false);
  let drawer = $state<ArchiveDrawerKind | null>(null);
  let drawerCase = $state.raw<CaseInfo | null>(null);
  let eraseId = $state('');
  let typed = $state('');
  let erasing = $state(false);
  let problem = $state('');
  let note = $state('');
  let controller: AbortController | null = null;

  const confirmed = $derived(eraseId !== '' && eraseConfirmed(typed, eraseId));

  // 'first' shows the loading line; 'more' adds the next page; 'again' reads the first page again without hiding the list (after a move).
  async function load(kind: 'first' | 'more' | 'again' = 'first') {
    const more = kind === 'more';
    controller?.abort();
    const mine = new AbortController();
    controller = mine;
    if (more) { loadingMore = true; moreFailed = false; } else if (kind === 'first') { phase = 'loading'; }
    try {
      const page = await listArchivedCases(more ? rows.length : 0, mine.signal);
      if (mine.signal.aborted) return;
      rows = more ? [...rows, ...page.cases.filter((c) => !rows.some((r) => r.id === c.id))] : page.cases;
      count = Math.max(page.archivedTotal, rows.length);
      phase = 'ready';
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return;
      if (more) moreFailed = true; else if (kind === 'first') phase = 'failed';
    } finally { if (controller === mine) loadingMore = false; }
  }

  onMount(() => {
    count = total;
    void load();
    return () => controller?.abort();
  });

  const when = (value: string): string => formatTime(value, { year: true, precision: 'minute', fallback: '' });
  const archivedOn = (c: CaseInfo): string => when(c.archive?.at ?? '');
  const countsOf = (c: CaseInfo): string => {
    const counts = c.archive?.current?.counts;
    return counts ? archiveCounts(counts.imports, counts.sources) : '';
  };
  const chipText = { archived: ARCHIVE_TEXT.chipArchived, kept: ARCHIVE_TEXT.chipKept, freed: ARCHIVE_TEXT.chipFreed, missing: ARCHIVE_TEXT.chipMissing } as const;

  function open(kind: ArchiveDrawerKind, c: CaseInfo, menu?: HTMLElement | null) {
    menu?.closest('details')?.removeAttribute('open');
    drawerCase = c;
    drawer = kind;
  }
  function restore(c: CaseInfo, menu?: HTMLElement | null) { open(archivedActions(c).restoreInPlace ? 'restore-in-place' : 'restore-as-new', c, menu); }
  function closeDrawer() { drawer = null; drawerCase = null; void load('again'); }
  function askErase(c: CaseInfo, menu?: HTMLElement | null) {
    menu?.closest('details')?.removeAttribute('open');
    eraseId = c.id; typed = ''; problem = ''; note = '';
  }
  function cancelErase() { eraseId = ''; typed = ''; }
  // One More menu at a time (D-A-13): opening one closes the others in this list.
  function moreToggled(event: Event) {
    const opened = event.currentTarget as HTMLDetailsElement;
    if (!opened.open) return;
    for (const other of document.querySelectorAll<HTMLDetailsElement>('.archived details.more[open]')) {
      if (other !== opened) other.open = false;
    }
  }
  // Escape closes the open menu and puts focus back on its More button, while focus is inside this list.
  function closeMoreOnEscape(event: KeyboardEvent) {
    if (event.key !== 'Escape') return;
    const menu = document.querySelector<HTMLDetailsElement>('.archived details.more[open]');
    if (!menu || !(document.activeElement as HTMLElement | null)?.closest('.archived')) return;
    event.preventDefault();
    menu.open = false;
    menu.querySelector('summary')?.focus();
  }

  async function erase(event: SubmitEvent) {
    event.preventDefault();
    if (!confirmed || erasing) return;
    const id = eraseId;
    erasing = true; problem = '';
    try {
      const row = rows.find((r) => r.id === id);
      await eraseCase(id, undefined, row ? believedState(row) : undefined);
      rows = rows.filter((r) => r.id !== id);
      count = Math.max(0, count - 1);
      eraseId = ''; typed = '';
      caseErased(id);
      note = ERASE_DONE;
    } catch (error) {
      if (isCaseGone(error)) { rows = rows.filter((r) => r.id !== id); eraseId = ''; typed = ''; count = Math.max(0, count - 1); }
      else problem = error instanceof CaseApiError ? archiveReasonText(error.reason, error.status) : archiveReasonText('unreachable', 0);
    } finally { erasing = false; }
  }

  // A move that changed the case behind this page (another tab) or erased it: the list is read again.
  function reread() { drawer = null; drawerCase = null; void load('again'); }
  function gone() { reread(); }
</script>

<svelte:window onkeydown={closeMoreOnEscape} />
<section class="archived" aria-labelledby="archived-title">
  <h3 id="archived-title">{ARCHIVE_TEXT.listHeading} ({count})</h3>
  <div aria-live="polite">
    {#if phase === 'loading'}
      <p class="card-note">{ARCHIVE_TEXT.listLoading}</p>
    {:else if phase === 'failed'}
      <p class="inline-error" role="alert">{ARCHIVE_TEXT.listFailed}</p>
      <button type="button" class="secondary-button" onclick={() => void load()}>{ARCHIVE_TEXT.listRetry}</button>
    {:else if rows.length === 0}
      <p class="card-note">{ARCHIVE_TEXT.listEmpty}</p>
    {/if}
    {#if note}<p class="card-note" role="status">{note}</p>{/if}
  </div>

  {#if phase === 'ready' && rows.length}
    <ul class="cases">
      {#each rows as c (c.id)}
        {@const actions = archivedActions(c)}
        {@const row = caseRow(c)}
        <li class="case-row">
          <div class="line">
            <div class="case-info">
              <strong class="case-title" title={row.fullTitle}>{row.title}</strong>
              <span class="case-meta">{ARCHIVE_TEXT.archivedOn} {archivedOn(c)}{countsOf(c) ? ' · ' + countsOf(c) : ''} · {row.codeText}</span>
              <span class="chips">
                {#each archivedChips(c) as chip (chip)}
                  {#if chip === 'key'}<span class="chip">{keyEndingText(keyEndingOf(c.archive?.current))}</span>
                  {:else}<span class="chip" class:warn={chip === 'missing'}>{chipText[chip]}</span>{/if}
                {/each}
              </span>
              {#if c.missing}<span class="case-meta">{ARCHIVE_TEXT.workingCopyMissing}</span>{/if}
            </div>
            <div class="buttons">
              {#if !c.missing}<button type="button" class="secondary-button" aria-label={'Open case ' + row.fullTitle} onclick={() => onOpen(c.id)}>Open</button>{/if}
              {#if actions.restoreInPlace || actions.restoreAsNew}
                <button type="button" class="secondary-button" aria-label={ARCHIVE_TEXT.restore + ': ' + row.fullTitle} onclick={(e) => restore(c, e.currentTarget)}>{ARCHIVE_TEXT.restore}</button>
              {/if}
              <details class="more" ontoggle={moreToggled}>
                <summary aria-label={ARCHIVE_TEXT.moreLabel + ': ' + row.fullTitle}>{ARCHIVE_TEXT.more}</summary>
                <div class="menu">
                  {#if actions.restoreInPlace && actions.restoreAsNew}<button type="button" class="quiet-button" onclick={(e) => open('restore-as-new', c, e.currentTarget)}>{ARCHIVE_TEXT.restoreAsNewMenu}</button>{/if}
                  {#if actions.free}<button type="button" class="quiet-button" onclick={(e) => open('free', c, e.currentTarget)}>{ARCHIVE_TEXT.freeMenu}</button>{/if}
                  <button type="button" class="quiet-button" onclick={(e) => askErase(c, e.currentTarget)}>{ARCHIVE_TEXT.erase}</button>
                </div>
              </details>
            </div>
          </div>
          {#if eraseId === c.id}
            <form class="erase" onsubmit={erase}>
              <p>{ARCHIVE_TEXT.eraseWarning}</p>
              <label for={'erase-' + c.id}>{erasePrompt(caseCode(c.id))}</label>
              <input id={'erase-' + c.id} type="text" bind:value={typed} autocomplete="off" spellcheck="false" />
              <div class="buttons">
                <button type="submit" class="primary-button danger-fill" disabled={erasing || !confirmed}>{erasing ? IMPORT_TEXT.erasing : ARCHIVE_TEXT.eraseConfirmButton}</button>
                <button type="button" class="secondary-button" disabled={erasing} onclick={cancelErase}>{ARCHIVE_TEXT.eraseCancel}</button>
              </div>
              {#if problem}<p class="inline-error" role="alert">{problem}</p>{/if}
            </form>
          {/if}
        </li>
      {/each}
    </ul>
    <p class="card-note">{archivedShownText(rows.length, count)}</p>
    {#if rows.length < count}
      {#if moreFailed}<p class="inline-error" role="alert">{ARCHIVE_TEXT.listFailed}</p>{/if}
      <button type="button" class="quiet-button" disabled={loadingMore} onclick={() => void load('more')}>{ARCHIVE_TEXT.listMore}</button>
    {/if}
  {/if}
  <div><button type="button" class="quiet-button" onclick={onBack}>{ARCHIVE_TEXT.listBack}</button></div>
</section>

<ArchiveDrawers {drawer} info={drawerCase} onClose={closeDrawer} onChanged={reread} onGone={gone} onOpenNew={(id) => { drawer = null; drawerCase = null; onOpen(id); }} onDone={() => void load('again')} />

<style>
  .archived { display: grid; gap: var(--space-2); min-width: 0; }
  .archived h3 { margin: 0; font-size: 15px; font-weight: 600; }
  .card-note { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .cases { display: grid; gap: var(--space-2); margin: var(--space-2) 0 0; padding: 0; list-style: none; }
  .case-row { position: relative; display: grid; gap: var(--space-2); min-width: 0; padding: var(--space-2) var(--space-3); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .line { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--space-3); min-width: 0; flex-wrap: wrap; }
  .case-info { display: grid; gap: 2px; min-width: 0; }
  .case-title { font-size: var(--fs-3); font-weight: 600; overflow-wrap: anywhere; }
  .case-meta { color: var(--text-2); font-size: var(--fs-2); }
  .chips { display: flex; flex-wrap: wrap; gap: var(--space-1); }
  .chip { padding: 0 var(--space-1); color: var(--text-2); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font-size: var(--fs-2); }
  .chip.warn { color: var(--warn); border-color: var(--warn); }
  .buttons { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  /* The menu hangs from the card, not from the small More button, so it stays inside the card at any width. */
  .more { position: static; }
  .more summary { cursor: pointer; padding: 0 var(--space-2); color: var(--text-2); font-size: var(--fs-3); list-style: none; }
  .more summary:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .menu { position: absolute; right: var(--space-3); z-index: 2; display: grid; gap: var(--space-1); min-width: min(220px, calc(100% - 2 * var(--space-3))); margin-top: var(--space-1); padding: var(--space-2); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .menu :global(.quiet-button) { justify-self: start; text-align: left; }
  .erase { display: grid; gap: var(--space-2); padding-top: var(--space-2); border-top: 1px solid var(--line); }
  .erase p { margin: 0; font-size: var(--fs-3); line-height: 1.5; }
  .erase label { color: var(--text-2); font-size: var(--fs-2); font-weight: 500; }
  .erase input { width: 100%; font-family: var(--mono); }
  .danger-fill { background: var(--bad); border-color: var(--bad); color: var(--bg); }
  .danger-fill:disabled { opacity: .5; }
</style>
