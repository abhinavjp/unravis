<script lang="ts">
  import { onMount } from 'svelte';
  import type { Snippet } from 'svelte';
  import { CaseApiError, caseCode, clearErasedLine, eraseCase, eraseConfirmed, isCaseGone } from '../lib/cases';
  import type { CaseInfo, ErasedLine } from '../lib/cases';
  import { ERASE_TEXT } from '../lib/copy/eraseCopy';
  import { listCasesPage } from '../lib/archiveApi';
  import { ARCHIVE_TEXT, archiveReasonText, archivedToggleText } from '../lib/copy/archiveCopy';
  import { ERASE_DONE, IMPORT_TEXT, erasePrompt } from '../lib/copy/importCopy';
  import ArchivedList from '../lib/components/ArchivedList.svelte';
  import { getMasterKeyStatus } from '../lib/masterKeyApi';
  import { closesCases, offersRewrap } from '../lib/masterKey';
  import type { LockState } from '../lib/masterKey';
  import { KEY_TEXT } from '../lib/copy/masterKeyCopy';
  import { START_TEXT, oldCasesText, openLabel } from '../lib/copy/startCopy';
  import { startView } from '../lib/startScreen';
  import { app, caseErased, noteKeyStatus, noteLocked } from '../lib/appState.svelte';
  import HelpNote from './HelpNote.svelte';
  import RewrapNotice from './RewrapNotice.svelte';

  // The first screen of a launch: three cards, always shown. Use an existing case, enter an existing case (a slot for the code field), create a new one.
  // It reads the master key status first. A locked key shows no titles and no counts and hands over to the unlock screen; the case list is never asked for.
  // `onCreate` opens the create flow (the import panel until the guided flow exists), `onOpen` opens a case, `onSetup` opens the master key set-up,
  // `onAnalysis` goes on to the analysis of the connected sources.
  let { onCreate, onOpen, onSetup, onAnalysis, enter }: {
    onCreate: () => void;
    onOpen: (id: string, options?: { archived?: boolean }) => void;
    onSetup: () => void;
    onAnalysis: () => void;
    enter?: Snippet;
  } = $props();

  let phase = $state<'checking' | 'loading' | 'ready' | 'failed'>('checking');
  let lock = $state<LockState | null>(app.lockState === 'unknown' ? null : app.lockState);
  let statusFailed = $state(false);
  // Kept on once the notice has been used, so its answer stays on screen after the count drops to nothing.
  let rewrapShown = $state(false);
  let cases = $state.raw<CaseInfo[]>([]);
  let erased = $state.raw<ErasedLine[]>([]);
  let clearProblem = $state('');
  let pages = $state(1);
  let archivedTotal = $state(0);
  // Cases from before this PC was set up for everyone: they stay in the person's own folder and are not shown here (P14).
  let oldCases = $state(0);
  let showArchived = $state(false);
  let controller: AbortController | null = null;
  // Erase for a case whose working copy is gone (D-A-16): the same type-the-code step the archived list uses.
  let eraseId = $state('');
  let typed = $state('');
  let erasing = $state(false);
  let eraseProblem = $state('');
  let eraseNote = $state('');
  const confirmed = $derived(eraseId !== '' && eraseConfirmed(typed, eraseId));

  const view = $derived(startView(lock, cases, pages, erased));

  async function load() {
    const signal = controller?.signal;
    phase = 'checking';
    statusFailed = false;
    try {
      const status = await getMasterKeyStatus(signal);
      noteKeyStatus(status);
      lock = status.state;
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return;
      statusFailed = true;
    }
    if (closesCases(lock)) { noteLocked(); return; }
    phase = 'loading';
    try {
      const page = await listCasesPage(signal);
      cases = page.cases;
      archivedTotal = page.archivedTotal;
      erased = page.erased;
      oldCases = page.oldCases;
      phase = 'ready';
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return;
      phase = 'failed';
    }
  }

  // Clearing a line removes only that line: the case itself is already gone.
  async function clearLine(id: string) {
    clearProblem = '';
    try {
      await clearErasedLine(id, controller?.signal);
      erased = erased.filter((line) => line.id !== id);
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return;
      clearProblem = ERASE_TEXT.clearFailed;
    }
  }

  function askErase(id: string) { eraseId = id; typed = ''; eraseProblem = ''; eraseNote = ''; }
  function cancelErase() { eraseId = ''; typed = ''; }

  async function erase(event: SubmitEvent) {
    event.preventDefault();
    if (!confirmed || erasing) return;
    const id = eraseId;
    erasing = true; eraseProblem = '';
    try {
      await eraseCase(id);
      cases = cases.filter((c) => c.id !== id);
      eraseId = ''; typed = '';
      caseErased(id);
      eraseNote = ERASE_DONE;
    } catch (error) {
      if (isCaseGone(error)) { cases = cases.filter((c) => c.id !== id); eraseId = ''; typed = ''; }
      else eraseProblem = error instanceof CaseApiError ? archiveReasonText(error.reason, error.status) : archiveReasonText('unreachable', 0);
    } finally { erasing = false; }
  }

  onMount(() => {
    controller = new AbortController();
    void load();
    return () => controller?.abort();
  });
</script>

{#if !view.locked}
  <section class="start" aria-labelledby="start-title">
    <header class="start-head">
      <h2 id="start-title">{START_TEXT.title}</h2>
      <p>{START_TEXT.intro}</p>
      <HelpNote id="start" />
    </header>

    {#if statusFailed}<p class="start-note" role="status">{START_TEXT.statusFailed}</p>{/if}
    {#if offersRewrap(lock, app.dpapiCases) || rewrapShown}<RewrapNotice count={app.dpapiCases} onChange={(left) => { rewrapShown = true; noteKeyStatus({ state: 'unlocked', dpapiCases: left }); }} />{/if}

    <div class="cards">
      <section class="card" aria-labelledby="card-existing">
        <h3 id="card-existing">{START_TEXT.existingTitle}</h3>
        <p class="card-text">{START_TEXT.existingText}</p>
        <div aria-live="polite">
          {#if phase === 'checking' || phase === 'loading'}
            <p class="card-note">{START_TEXT.existingLoading}</p>
          {:else if phase === 'failed'}
            <p class="inline-error" role="alert">{START_TEXT.existingFailed}</p>
            <button type="button" class="secondary-button" onclick={() => void load()}>{START_TEXT.retry}</button>
          {:else if view.empty}
            <p class="card-note">{view.empty}</p>
          {/if}
        </div>
        {#if showArchived}
          <ArchivedList total={archivedTotal} onOpen={(id) => onOpen(id, { archived: true })} onBack={() => { showArchived = false; void load(); }} />
        {:else if phase === 'ready' && view.rows.length}
          <ul class="cases">
            {#each view.rows as row (row.id)}
              <li class="case-row">
                <div class="case-info">
                  <strong class="case-title" title={row.fullTitle}>{row.title}</strong>
                  <span class="case-meta">{row.created}{row.created ? ' · ' : ''}{row.counts} · {row.codeText}</span>
                  {#if row.closed}<span class="case-closed">{row.closedLabel}</span>{/if}
                  {#if row.keyEnding}<span class="case-closed">{row.keyEnding}</span>{/if}
                  {#if row.untrusted}<span class="case-closed" role="note">{row.untrustedLabel}</span>{/if}
                </div>
                {#if row.archiving}
                  <span class="case-closed" role="note">{row.archivingLabel}</span>
                {:else if row.missing}
                  <span class="case-closed" role="note">{row.missingLabel}</span>
                  <button type="button" class="secondary-button" aria-label={ARCHIVE_TEXT.erase + ': ' + row.fullTitle} onclick={() => askErase(row.id)}>{ARCHIVE_TEXT.erase}</button>
                {:else}
                  <button type="button" class="secondary-button" aria-label={openLabel(row.fullTitle)} onclick={() => onOpen(row.id)}>{START_TEXT.open}</button>
                {/if}
                {#if eraseId === row.id}
                  <form class="erase" onsubmit={erase}>
                    <p>{ARCHIVE_TEXT.eraseWarning}</p>
                    <label for={'erase-' + row.id}>{erasePrompt(caseCode(row.id))}</label>
                    <input id={'erase-' + row.id} type="text" bind:value={typed} autocomplete="off" spellcheck="false" />
                    <div class="buttons">
                      <button type="submit" class="primary-button danger-fill" disabled={erasing || !confirmed}>{erasing ? IMPORT_TEXT.erasing : ARCHIVE_TEXT.eraseConfirmButton}</button>
                      <button type="button" class="secondary-button" disabled={erasing} onclick={cancelErase}>{ARCHIVE_TEXT.eraseCancel}</button>
                    </div>
                    {#if eraseProblem}<p class="inline-error" role="alert">{eraseProblem}</p>{/if}
                  </form>
                {/if}
              </li>
            {/each}
          </ul>
          {#if view.hidden > 0}
            <p class="card-note">{view.shown}</p>
            <button type="button" class="quiet-button" onclick={() => (pages += 1)}>{START_TEXT.showMore}</button>
          {/if}
        {/if}
        {#if eraseNote && !showArchived}<p class="card-note" role="status">{eraseNote}</p>{/if}
        {#if phase === 'ready' && oldCases > 0}<p class="card-note" role="note" data-old-cases>{oldCasesText(oldCases)}</p>{/if}
        {#if phase === 'ready' && !showArchived && archivedTotal > 0}
          <button type="button" class="quiet-button archived-toggle" onclick={() => (showArchived = true)}>{archivedToggleText(archivedTotal)}</button>
        {/if}
        {#if phase === 'ready' && view.erased.length}
          <ul class="cases erased" aria-label={ERASE_TEXT.erasedSticker}>
            {#each view.erased as line (line.id)}
              <li class="case-row">
                <div class="case-info">
                  <span class="case-closed">{line.sticker}</span>
                  <span class="case-meta">{line.text}</span>
                </div>
                <button type="button" class="secondary-button" aria-label={ERASE_TEXT.clear + ' ' + line.code} onclick={() => void clearLine(line.id)}>{ERASE_TEXT.clear}</button>
              </li>
            {/each}
          </ul>
          {#if clearProblem}<p class="inline-error" role="alert">{clearProblem}</p>{/if}
        {/if}
      </section>

      <section class="card" aria-labelledby="card-enter">
        <h3 id="card-enter">{START_TEXT.enterTitle}</h3>
        <p class="card-text">{START_TEXT.enterText}</p>
        {#if enter}{@render enter()}{:else}<p class="card-note">{START_TEXT.enterSoon}</p>{/if}
      </section>

      <section class="card" aria-labelledby="card-create">
        <h3 id="card-create">{START_TEXT.createTitle}</h3>
        <p class="card-text">{START_TEXT.createText}</p>
        <button type="button" class="primary-button" onclick={onCreate}>{START_TEXT.createButton}</button>
      </section>
    </div>

    <footer class="start-foot">
      {#if view.offerSetup}<button type="button" class="link-button" onclick={onSetup}>{KEY_TEXT.setupLink}</button>{/if}
      <button type="button" class="quiet-button" onclick={onAnalysis}>{START_TEXT.analysisLink}</button>
    </footer>
  </section>
{/if}

<style>
  .start { display: grid; gap: var(--space-4); min-width: 0; font-family: var(--mono); }
  .start-head { display: grid; gap: var(--space-2); max-width: 64ch; }
  .start-head h2 { margin: 0; font-size: 20px; font-weight: 600; }
  .start-head p { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .start-note { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
  .cards { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: var(--space-3); align-items: start; }
  .card { display: grid; gap: var(--space-2); align-content: start; min-width: 0; padding: var(--space-4); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .card h3 { margin: 0; font-size: 15px; font-weight: 600; }
  .card-text, .card-note { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .card :global(.primary-button), .card :global(.secondary-button) { justify-self: start; }
  .cases { display: grid; gap: var(--space-2); margin: var(--space-2) 0 0; padding: 0; list-style: none; }
  .case-row { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--space-3); min-width: 0; padding: var(--space-2) var(--space-3); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .erase { display: grid; flex-basis: 100%; gap: var(--space-2); padding-top: var(--space-2); border-top: 1px solid var(--line); }
  .erase p { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .buttons { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .case-info { display: grid; gap: 2px; min-width: 0; }
  .case-title { font-size: var(--fs-3); font-weight: 600; overflow-wrap: anywhere; }
  .case-meta { color: var(--text-2); font-size: var(--fs-2); }
  .case-closed { justify-self: start; padding: 0 var(--space-1); color: var(--text-2); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font-size: var(--fs-2); }
  .case-row :global(.secondary-button) { flex-shrink: 0; }
  .start-foot { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); }
  .link-button { padding: 0; color: var(--accent); background: transparent; border: 0; font: inherit; font-size: var(--fs-3); text-decoration: underline; cursor: pointer; }
  .link-button:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  @media (max-width: 960px) { .cards { grid-template-columns: minmax(0, 1fr); } }
</style>
