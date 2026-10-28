<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api, ApiError } from '../api';
  import { app, goHome, openAnalysis, openCase, openCaseImport, openConnections, openSidePanel, panels } from '../appState.svelte';
  import { believedState, caseCode, deleteRetry, getCaseView, isCaseChanged, isCaseGone, listImports, reopenCase, retryInPage, runRetry, type CaseInfo, type CaseView, type ImportRecord, type RetryItem } from '../cases';
  import { archiveButton, readsCaseData, reopenButton, retryAction } from '../caseLifecycle';
  import { archivedActions, keyEndingOf, type ArchiveDrawerKind } from '../archiveModel';
  import { ARCHIVE_TEXT, archivedNotice, filesMadeText, restoredNewNotice } from '../copy/archiveCopy';
  import ArchiveDrawers from './ArchiveDrawers.svelte';
  import { CASE_HOME_TEXT, retryKindLabel } from '../copy/caseHomeCopy';
  import { errorText, importerLabel, formatBytes } from '../copy/importCopy';
  import { fetchHealthRuns, type HealthRun } from '../health';
  import { stateLabel } from '../ledger';
  import { holdLedger, ledger, refreshLedger } from '../ledgerStore.svelte';
  import { buildStory, shortText } from '../story';
  import { GUIDED_TEXT } from '../copy/guidedCopy';
  import { guidedOpenFor } from '../guided/stepModel';
  import GuidedAnalysis from './guided/GuidedAnalysis.svelte';
  import { formatTime } from '../time';
  import { timeZone } from '../timeZone.svelte';
  import HealthPanel from './HealthPanel.svelte';
  import SourcesHub from '../../components/sources/SourcesHub.svelte';
  import { HUB_TEXT } from '../copy/sourcesCopy';

  // One page per case: the story (what happened, in order), the theories, the health checks, the analysis and the files.
  // Everything here is read from what the case already saved. Nothing runs by itself.
  const caseId = $derived(app.activeCaseId);

  let phase = $state<'loading' | 'ready' | 'gone' | 'failed'>('loading');
  let info = $state.raw<CaseView | null>(null);
  // The "needs another try" list the case keeps, the item being tried, and the notes this page shows about a move on the case.
  let reopening = $state(false);
  let changed = $state(false);
  let lifeProblem = $state('');
  let tryingId = $state(0);
  let retryNote = $state<Record<number, string>>({});
  // Where focus goes when the button that was pressed leaves the page (Reopen, or an item that is done or removed).
  let headingEl = $state<HTMLElement>();
  let retriesEl = $state<HTMLElement>();
  let reloadEl = $state<HTMLButtonElement>();
  let imports = $state.raw<ImportRecord[]>([]);
  let health = $state.raw<HealthRun[]>([]);
  let theoryProblem = $state(false);
  let sharedCount = $state(0);
  let copying = $state(false);
  let copyNote = $state('');
  let copyProblem = $state('');
  // The case the guided view was opened on. Another case starts at its own home.
  let guidedFor = $state('');
  const guidedOpen = $derived(guidedOpenFor(guidedFor, caseId));
  let controller: AbortController | null = null;
  let loadedFor = '';

  // An archived case offers no analysis, files, checks or theories (spec section 9): the page shows its record and the way back.
  const offersWork = $derived(!info || readsCaseData(info));
  const theories = $derived(offersWork ? ledger.items : []);
  const story = $derived(buildStory({ info, imports, health, theories, history: info?.history }));
  const keptFiles = $derived(imports.filter(record => record.status === 'ok'));
  const hasIis = $derived(keptFiles.some(record => record.importer === 'iis-w3c'));
  // A case that is not open (closed, archived) or is untrusted is read-only: nothing can be added, so the buttons that add say so up front.
  const readOnly = $derived(!!info && (!!info.untrusted || believedState(info) !== 'open'));
  const reopenState = $derived(info ? reopenButton(info, reopening) : 'hidden');
  // Once another tab has moved the case this page is out of date: nothing that moves the case is offered until it is reloaded.
  const stale = $derived(changed);
  const retries = $derived(info?.retries ?? []);

  // Archive, restore and free. The drawer is local to this page; the notice says what the last move made, until another move or a reload.
  let archiveDrawer = $state<ArchiveDrawerKind | null>(null);
  let archiveNote = $state('');
  const archiveState = $derived(info ? archiveButton(info) : 'hidden');
  const isArchived = $derived(!!info && believedState(info) === 'archived');
  const archivedMoves = $derived(info ? archivedActions(info) : null);
  const madeCount = $derived(info?.archive?.files?.length || info?.filesMade || 0);
  const currentFile = $derived(info?.archive?.current ?? null);
  const archivingNow = $derived(!!info && believedState(info) === 'archiving');

  function archiveMoved(kind: ArchiveDrawerKind, made: CaseInfo | null) {
    void (async () => {
      // A move changes what the page may ask for (an archived case has no files or checks to read, a restored one has them again), so the
      // whole page is read again, quietly. Restore as new leaves this case as it was.
      if (kind === 'restore-as-new') await rereadFiles(); else if (caseId) await load(caseId, true);
      if (kind === 'archive') {
        const file = info?.archive?.current;
        if (file) archiveNote = archivedNotice(file.name, file.folder, keyEndingOf(file));
      } else if (kind === 'restore-in-place') archiveNote = ARCHIVE_TEXT.restoreInPlaceDone;
      else if (kind === 'free') archiveNote = ARCHIVE_TEXT.freeDone;
      else if (made) archiveNote = restoredNewNotice(made.title || caseCode(made.id), caseCode(made.id));
    })();
  }
  // The drawer hands focus back to the button that opened it. When that button is gone (an archive finished, the page changed) the case name takes it.
  function closeArchiveDrawer() {
    archiveDrawer = null;
    void tick().then(() => { const now = document.activeElement; if (!now || now === document.body) headingEl?.focus(); });
  }
  function archiveChanged() { archiveDrawer = null; changed = true; void tick().then(() => reloadEl?.focus()); }

  // quiet: the page is read again after a move on the case (archive, restore, free), so it stays on screen and does not show the loading text.
  async function load(id: string, quiet = false) {
    controller?.abort();
    const mine = new AbortController();
    controller = mine;
    if (!quiet) phase = 'loading';
    loadedFor = id;
    copyNote = '';
    copyProblem = '';
    sharedCount = 0;
    changed = false;
    lifeProblem = '';
    retryNote = {};
    // The case is read first: an archived case answers 409 to its files, checks and theories, so those are not asked for.
    const [view] = await Promise.allSettled([getCaseView(id, mine.signal)]);
    if (mine.signal.aborted || id !== caseId) return;
    const found = view.status === 'fulfilled' ? view.value : null;
    const reads = found ? readsCaseData(found) : true;
    holdLedger(id, !reads);
    const none = <T,>(value: T): Promise<T> => Promise.resolve(value);
    const [files, runs, theory] = await Promise.allSettled([reads ? listImports(id, mine.signal) : none<ImportRecord[]>([]), reads ? fetchHealthRuns(id, mine.signal) : none<HealthRun[]>([]), reads ? refreshLedger() : none<void>(undefined)]);
    if (mine.signal.aborted || id !== caseId) return;
    if (files.status === 'rejected' && isCaseGone(files.reason)) { phase = 'gone'; return; }
    if (view.status === 'rejected' && isCaseGone(view.reason)) { phase = 'gone'; return; }
    if (view.status === 'rejected' && files.status === 'rejected') { phase = 'failed'; return; }
    info = found;
    imports = files.status === 'fulfilled' ? files.value : [];
    health = runs.status === 'fulfilled' ? runs.value : [];
    theoryProblem = theory.status === 'rejected';
    phase = 'ready';
    if (theory.status === 'fulfilled' && ledger.items.length === 0) void findShared(mine.signal);
  }

  // Theories written before cases kept their own are offered once, and only while this case has none.
  async function findShared(signal: AbortSignal) {
    try {
      const shared = (await api.ledgerSharedList()) ?? [];
      if (!signal.aborted) sharedCount = shared.length;
    } catch { /* the offer simply does not show */ }
  }

  async function copyShared() {
    const id = caseId;
    if (copying || !id) return;
    copying = true;
    copyProblem = '';
    try {
      await api.ledgerCopyShared(id);
    } catch (error) {
      // 409: another tab already put theories in this case. That is fine; they are read in below.
      if (!(error instanceof ApiError && error.status === 409)) {
        if (id === caseId) copyProblem = CASE_HOME_TEXT.copyFailed;
        copying = false;
        return;
      }
    }
    try {
      await refreshLedger();
      if (id === caseId) { sharedCount = 0; copyNote = CASE_HOME_TEXT.copyDone; }
    } catch {
      if (id === caseId) copyProblem = CASE_HOME_TEXT.copyFailed;
    } finally {
      copying = false;
    }
  }

  // Files added from the drawer show here once it closes: the list, the counts and the story are read again, without the loading screen.
  let drawerWas: string | null = null;
  async function rereadFiles() {
    const id = caseId;
    if (!id || phase !== 'ready') return;
    try {
      const view = await getCaseView(id);
      const files = readsCaseData(view) ? await listImports(id) : [];
      if (id !== caseId) return;
      info = view;
      imports = files;
    } catch { /* the page keeps the list it had */ }
  }
  $effect(() => {
    const now = panels.sidePanel;
    if (drawerWas === 'import' && now === null) void rereadFiles();
    drawerWas = now;
  });

  async function rereadHealth() {
    const id = caseId;
    if (!id) return;
    try {
      const runs = await fetchHealthRuns(id);
      if (id === caseId) health = runs;
    } catch { /* the story keeps the list it had */ }
  }

  function gone() { phase = 'gone'; }

  // A move on the case sends what this page believes about it. If another tab moved the case first, the server says so (409, M2): the page
  // shows that and offers a reload, and changes nothing by itself.
  function lifeFailed(error: unknown) {
    if (error instanceof DOMException && error.name === 'AbortError') return;
    if (isCaseGone(error)) { gone(); return; }
    if (isCaseChanged(error)) { changed = true; void tick().then(() => reloadEl?.focus()); return; }
    lifeProblem = error instanceof Error && 'status' in error ? errorText(error as never) : errorText({ status: 0, reason: 'unreachable', message: '' });
  }

  async function reopen() {
    const id = caseId;
    if (!info || reopening || stale || reopenState !== 'enabled') return;
    reopening = true;
    lifeProblem = '';
    let done = false;
    try {
      await reopenCase(id, believedState(info));
      await rereadFiles();
      done = true;
    } catch (error) { lifeFailed(error); } finally { reopening = false; }
    // The Reopen button is gone now, so focus goes to the case name instead of falling back to the top of the page.
    if (done) { await tick(); headingEl?.focus(); }
  }

  async function reload() {
    changed = false; lifeProblem = '';
    if (!caseId) return;
    await load(caseId);
    await tick();
    headingEl?.focus();
  }

  // After an item is done or removed its button is gone: focus goes to the same item if it is still there, else the next one, else the case name.
  async function focusRetry(index: number, id: number) {
    await tick();
    const rows = retriesEl ? [...retriesEl.querySelectorAll<HTMLElement>('li')] : [];
    const row = rows.find(item => item.dataset.retry === String(id)) ?? rows[Math.min(index, rows.length - 1)];
    ((row?.querySelector<HTMLButtonElement>('button:not(:disabled)')) ?? headingEl)?.focus();
  }

  async function tryAgain(item: RetryItem) {
    const id = caseId;
    if (!info || tryingId !== 0 || stale) return;
    if (retryAction(item, info) === 'add-file') { openCaseImport(); return; }
    const index = retries.findIndex(row => row.id === item.id);
    tryingId = item.id;
    retryNote = { ...retryNote, [item.id]: '' };
    try {
      await runRetry(id, item.id, believedState(info));
      await rereadFiles();
    } catch (error) {
      if (retryInPage(error)) retryNote = { ...retryNote, [item.id]: CASE_HOME_TEXT.retryInPage };
      else if (isCaseChanged(error) || isCaseGone(error)) lifeFailed(error);
      else retryNote = { ...retryNote, [item.id]: CASE_HOME_TEXT.retryFailed };
    } finally { tryingId = 0; }
    await focusRetry(index, item.id);
  }

  async function dropRetry(item: RetryItem) {
    const id = caseId;
    if (tryingId !== 0 || stale) return;
    const index = retries.findIndex(row => row.id === item.id);
    tryingId = item.id;
    try { await deleteRetry(id, item.id, info ? believedState(info) : undefined); await rereadFiles(); } catch (error) { lifeFailed(error); } finally { tryingId = 0; }
    await focusRetry(index, item.id);
  }

  $effect(() => {
    const id = caseId;
    if (id && id !== loadedFor) void load(id);
  });
  onMount(() => () => controller?.abort());

  // Every date on this page follows the UTC / Local choice in the header, like the start screen's case list.
  const when = (value: string): string => formatTime(value, { zone: timeZone.zone, year: true, precision: 'minute', fallback: value });
  const kindLabel = { case: CASE_HOME_TEXT.kindCase, file: CASE_HOME_TEXT.kindFile, health: CASE_HOME_TEXT.kindHealth, theory: CASE_HOME_TEXT.kindTheory } as const;
</script>

<div class="case-home">
  <div class="top">
    <button type="button" class="secondary-button" aria-label={CASE_HOME_TEXT.backLabel} onclick={goHome}>&larr; {CASE_HOME_TEXT.back}</button>
  </div>

  {#if !caseId || phase === 'gone'}
    <div class="notice" role="alert">{CASE_HOME_TEXT.gone}</div>
  {:else if phase === 'loading'}
    <div class="loading-state" role="status"><div class="loading-pulse"></div><p>{CASE_HOME_TEXT.loading}</p></div>
  {:else if phase === 'failed'}
    <div class="inline-error" role="alert">{CASE_HOME_TEXT.failed}</div>
    <div><button type="button" class="secondary-button" onclick={() => void load(caseId)}>{CASE_HOME_TEXT.retry}</button></div>
  {:else if guidedOpen}
    {#key caseId}
      <GuidedAnalysis {caseId} closed={!!info?.closed} {readOnly} {hasIis} onAddData={openCaseImport} onClose={() => (guidedFor = '')} />
    {/key}
  {:else}
    <header class="head">
      <p class="eyebrow">{CASE_HOME_TEXT.heading} · {caseCode(caseId)}</p>
      <h2 tabindex="-1" bind:this={headingEl}>{info?.title || caseCode(caseId)}</h2>
      <p class="note">{CASE_HOME_TEXT.intro}</p>
      <dl class="counts">
        {#if info?.created}<div><dt>{CASE_HOME_TEXT.created}</dt><dd>{when(info.created)}</dd></div>{/if}
        <div><dt>{CASE_HOME_TEXT.filesCount}</dt><dd>{keptFiles.length}</dd></div>
        <div><dt>{CASE_HOME_TEXT.theoriesCount}</dt><dd>{theories.length}</dd></div>
        <div><dt>{CASE_HOME_TEXT.checksCount}</dt><dd>{health.length}</dd></div>
        {#if retries.length > 0}<div><dt>{CASE_HOME_TEXT.retryCount}</dt><dd>{retries.length}</dd></div>{/if}
      </dl>
      {#if madeCount > 0}<p class="note files-made">{filesMadeText(madeCount)}</p>{/if}
      {#if info?.untrusted}<p class="notice" role="status">{CASE_HOME_TEXT.untrusted}</p>
      {:else if isArchived}
        <p class="notice" role="status">{info?.missing ? ARCHIVE_TEXT.workingCopyMissing : info?.archive?.freed ? ARCHIVE_TEXT.freedNote : ARCHIVE_TEXT.archivedNote}</p>
        <p class="note">{ARCHIVE_TEXT.archivedWork}</p>
        {#if currentFile}<p class="note">{ARCHIVE_TEXT.caseFileLine}: {currentFile.name}{currentFile.folder ? ' ' + ARCHIVE_TEXT.inFolder + ' ' + currentFile.folder : ''}{currentFile.keyEnding ? ' · ' + ARCHIVE_TEXT.keyEnding + ' ' + currentFile.keyEnding : ''}</p>{/if}
      {:else if archivingNow}<p class="notice" role="status">{ARCHIVE_TEXT.chipArchiving}{info?.percent !== undefined ? ' ' + info.percent + '%' : ''}</p>
      {:else if info && believedState(info) === 'closed'}<p class="notice" role="status">{CASE_HOME_TEXT.closedNote}</p>{/if}
      {#if archiveNote}<p class="notice" role="status">{archiveNote}</p>{/if}
      {#if archiveState !== 'hidden' || isArchived}
        <div class="actions">
          {#if archiveState !== 'hidden'}<button type="button" class="secondary-button" aria-haspopup="dialog" disabled={archiveState !== 'enabled' || stale} onclick={() => { archiveNote = ''; archiveDrawer = 'archive'; }}>{ARCHIVE_TEXT.archiveButton}</button>{/if}
          {#if archivedMoves?.restoreInPlace}<button type="button" class="secondary-button" aria-haspopup="dialog" disabled={stale} onclick={() => { archiveNote = ''; archiveDrawer = 'restore-in-place'; }}>{ARCHIVE_TEXT.restore}</button>{/if}
          {#if archivedMoves?.restoreAsNew}<button type="button" class="secondary-button" aria-haspopup="dialog" disabled={stale} onclick={() => { archiveNote = ''; archiveDrawer = 'restore-as-new'; }}>{ARCHIVE_TEXT.restoreAsNewMenu}</button>{/if}
          {#if archivedMoves?.free}<button type="button" class="secondary-button" aria-haspopup="dialog" disabled={stale} onclick={() => { archiveNote = ''; archiveDrawer = 'free'; }}>{ARCHIVE_TEXT.freeMenu}</button>{/if}
        </div>
      {/if}
      {#if reopenState !== 'hidden'}
        <div class="actions">
          <button type="button" class="secondary-button" disabled={reopenState !== 'enabled' || stale} aria-busy={reopenState === 'busy'} onclick={() => void reopen()}>{reopenState === 'busy' ? CASE_HOME_TEXT.reopening : CASE_HOME_TEXT.reopenButton}</button>
        </div>
        {#if reopenState === 'enabled' && !stale}<p class="note">{CASE_HOME_TEXT.reopenHint}</p>{/if}
      {/if}
      <div aria-live="polite">
        {#if changed}
          <div class="inline-error" role="alert">{CASE_HOME_TEXT.changedElsewhere} <button type="button" class="secondary-button" bind:this={reloadEl} onclick={() => void reload()}>{CASE_HOME_TEXT.reload}</button></div>
        {/if}
        {#if lifeProblem}<div class="inline-error" role="alert">{lifeProblem}</div>{/if}
      </div>
    </header>

    {#if retries.length > 0}
      <section class="block" aria-labelledby="home-retries">
        <h3 id="home-retries">{CASE_HOME_TEXT.retryHeading}</h3>
        <p class="note">{CASE_HOME_TEXT.retryIntro}</p>
        <ul class="retries" bind:this={retriesEl}>
          {#each retries as item (item.id)}
            {@const action = info ? retryAction(item, info) : 'hidden'}
            <li data-retry={item.id}>
              <div class="retry-main">
                <span class="state">{retryKindLabel(item.kind)}</span>
                {#if item.label}<strong>{item.label}</strong>{/if}
                {#if retryNote[item.id]}<p class="note" role="status">{retryNote[item.id]}</p>{/if}
                {#if action === 'reopen-first'}<p class="note">{CASE_HOME_TEXT.retryClosed}</p>{/if}
                {#if action === 'in-wizard'}<p class="note">{CASE_HOME_TEXT.retryInPage}</p>{/if}
              </div>
              {#if action !== 'hidden'}
                <div class="actions">
                  {#if action === 'retry' || action === 'add-file'}
                    <button type="button" class="secondary-button" disabled={tryingId !== 0 || stale} onclick={() => void tryAgain(item)}>{tryingId === item.id ? CASE_HOME_TEXT.retrying : action === 'add-file' ? CASE_HOME_TEXT.retryAddFile : CASE_HOME_TEXT.retryButton}</button>
                  {/if}
                  <button type="button" class="secondary-button" disabled={tryingId !== 0 || stale || action === 'reopen-first'} onclick={() => void dropRetry(item)}>{CASE_HOME_TEXT.retryDrop}</button>
                </div>
              {/if}
            </li>
          {/each}
        </ul>
      </section>
    {/if}

    {#if offersWork}
    <section class="block" aria-labelledby="home-analysis">
      <h3 id="home-analysis">{CASE_HOME_TEXT.analysisHeading}</h3>
      <p class="note">{CASE_HOME_TEXT.analysisText}</p>
      <div><button type="button" class="primary-button" onclick={openAnalysis}>{CASE_HOME_TEXT.analysisButton}</button></div>
      <p class="note">{CASE_HOME_TEXT.analysisHint}</p>
    </section>

    <section class="block" aria-labelledby="home-guided">
      <h3 id="home-guided">{GUIDED_TEXT.heading}</h3>
      <p class="note">{GUIDED_TEXT.openGuidedNote}</p>
      <div><button type="button" class="secondary-button" onclick={() => (guidedFor = caseId)}>{GUIDED_TEXT.openGuided}</button></div>
    </section>

    {/if}

    <section class="block" aria-labelledby="home-story">
      <h3 id="home-story">{CASE_HOME_TEXT.storyHeading}</h3>
      <p class="note">{CASE_HOME_TEXT.storyIntro}</p>
      {#if story.length === 0}
        <p class="empty">{CASE_HOME_TEXT.storyNone}</p>
      {:else}
        <ol class="story">
          {#each story as event (event.id)}
            <li class={'tone-' + event.tone}>
              <time datetime={event.at}>{when(event.at)}</time>
              <div>
                <p class="title"><span class="kind">{kindLabel[event.kind]}</span> {event.title}</p>
                {#if event.detail}<p class="detail">{event.detail}</p>{/if}
              </div>
            </li>
          {/each}
        </ol>
      {/if}
      <p class="note gap">{CASE_HOME_TEXT.storyGap}</p>
    </section>

    {#if offersWork}
    <section class="block" aria-labelledby="home-theories">
      <h3 id="home-theories">{CASE_HOME_TEXT.theoriesHeading}</h3>
      <p class="note">{CASE_HOME_TEXT.theoriesIntro}</p>
      <div aria-live="polite">
        {#if theoryProblem}<div class="inline-error" role="alert">{CASE_HOME_TEXT.theoriesFailed}</div>{/if}
        {#if copyProblem}<div class="inline-error" role="alert">{copyProblem}</div>{/if}
        {#if copyNote}<p class="note" role="status">{copyNote}</p>{/if}
      </div>
      {#if theories.length === 0}
        <p class="empty">{CASE_HOME_TEXT.theoriesNone}</p>
      {:else}
        <ul class="theories">
          {#each theories as theory (theory.id)}
            <li><span class="state">{stateLabel(theory.state)}</span> {shortText(theory.text, 160)}</li>
          {/each}
        </ul>
      {/if}
      {#if sharedCount > 0 && theories.length === 0}
        <div class="offer">
          <strong>{CASE_HOME_TEXT.copyHeading}</strong>
          <p class="note">{CASE_HOME_TEXT.copyText}</p>
          <div><button type="button" class="secondary-button" disabled={copying || readOnly} onclick={() => void copyShared()}>{copying ? CASE_HOME_TEXT.copying : CASE_HOME_TEXT.copyButton}</button></div>
        </div>
      {/if}
      <div class="actions">
        <button type="button" class="secondary-button" aria-haspopup="dialog" disabled={readOnly && theories.length === 0} onclick={() => openSidePanel('ledger')}>{theories.length ? CASE_HOME_TEXT.theoriesOpen : CASE_HOME_TEXT.theoriesAdd}</button>
      </div>
    </section>

    <section class="block" aria-labelledby="home-health">
      <h3 id="home-health">{CASE_HOME_TEXT.healthHeading}</h3>
      <p class="note">{CASE_HOME_TEXT.healthText}</p>
      <HealthPanel {caseId} onGone={gone} onRan={() => void rereadHealth()} />
    </section>

    <section class="block" aria-labelledby="home-sources">
      <h3 id="home-sources">{HUB_TEXT.heading}</h3>
      <p class="note">{HUB_TEXT.intro}</p>
      <SourcesHub {caseId} {readOnly} onGone={gone} onAddFiles={openCaseImport} onAddBlackbox={() => openSidePanel('blackbox')} onAddMongo={() => void openConnections('new')} />
    </section>

    <section class="block" aria-labelledby="home-files">
      <h3 id="home-files">{CASE_HOME_TEXT.filesHeading}</h3>
      {#if keptFiles.length === 0}
        <p class="empty">{CASE_HOME_TEXT.filesNone}</p>
      {:else}
        <ul class="files">
          {#each keptFiles as file (file.id)}
            <li><strong>{file.fileName}</strong> <span>{importerLabel(file.importer)} · {file.rows} {CASE_HOME_TEXT.filesRows}{file.rejected > 0 ? ' · ' + file.rejected + ' ' + CASE_HOME_TEXT.filesSkipped : ''} · {formatBytes(file.bytes)}</span></li>
          {/each}
        </ul>
      {/if}
      <div class="actions"><button type="button" class="secondary-button" aria-haspopup="dialog" disabled={readOnly} onclick={openCaseImport}>{CASE_HOME_TEXT.filesAdd}</button></div>
    </section>
    {/if}
  {/if}
</div>

<ArchiveDrawers drawer={archiveDrawer} {info} onClose={closeArchiveDrawer} onChanged={archiveChanged} onGone={() => { archiveDrawer = null; gone(); }} onOpenNew={(id) => { archiveDrawer = null; openCase(id); }} onDone={archiveMoved} />

<style>
  .case-home { display: grid; gap: 20px; max-width: 880px; margin: 0 auto; padding: 8px 0 48px; }
  .top { display: flex; }
  .head h2 { margin: 2px 0 6px; font-size: 24px; overflow-wrap: anywhere; }
  .eyebrow { margin: 0; color: var(--text-2); font-size: 12px; letter-spacing: 0.04em; text-transform: uppercase; }
  .counts { display: flex; flex-wrap: wrap; gap: 8px 28px; margin: 14px 0 0; }
  .counts div { display: grid; gap: 2px; }
  .counts dt { color: var(--text-2); font-size: 12px; }
  .counts dd { margin: 0; font-size: 15px; }
  .block { display: grid; gap: 10px; padding: 16px 18px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-md); }
  .block h3 { margin: 0; font-size: 16px; }
  .note, .empty { margin: 0; color: var(--text-2); font-size: 14px; }
  .gap { font-size: 13px; }
  .actions { display: flex; flex-wrap: wrap; gap: 8px; }
  .story { list-style: none; margin: 0; padding: 0; display: grid; gap: 0; border-left: 2px solid var(--line); }
  .story li { position: relative; display: grid; grid-template-columns: 170px 1fr; gap: 12px; padding: 8px 0 8px 16px; }
  .story li::before { content: ''; position: absolute; left: -6px; top: 14px; width: 10px; height: 10px; border-radius: 50%; background: var(--text-2); }
  .story li.tone-ok::before { background: var(--tone-ok); }
  .story li.tone-warn::before { background: var(--warn); }
  .story li.tone-bad::before { background: var(--bad); }
  .story time { color: var(--text-2); font-size: 12px; padding-top: 2px; }
  .story .title { margin: 0; font-size: 14px; }
  .story .kind { display: inline-block; margin-right: 6px; padding: 0 6px; color: var(--text-2); font-size: 11px; text-transform: uppercase; letter-spacing: 0.04em; border: 1px solid var(--line); border-radius: 4px; }
  .story .detail { margin: 2px 0 0; color: var(--text-2); font-size: 13px; overflow-wrap: anywhere; }
  .theories, .files { list-style: none; margin: 0; padding: 0; display: grid; gap: 6px; }
  .theories li, .files li { font-size: 14px; overflow-wrap: anywhere; }
  .theories .state { margin-right: 6px; padding: 0 6px; color: var(--text-2); font-size: 12px; border: 1px solid var(--line); border-radius: 4px; }
  .files li span { color: var(--text-2); font-size: 13px; }
  .retries { list-style: none; margin: 0; padding: 0; display: grid; gap: 0; }
  .retries li { padding: 10px 0; border-top: 1px solid var(--line-soft); display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px 12px; font-size: 14px; }
  .retry-main { display: grid; gap: 4px; min-width: 0; overflow-wrap: anywhere; }
  .retries .state { justify-self: start; padding: 0 6px; color: var(--text-2); font-size: 12px; border: 1px solid var(--line); border-radius: 4px; }
  .offer { display: grid; gap: 6px; padding: 10px 12px; border: 1px dashed var(--line); border-radius: var(--radius-sm); }
  @media (max-width: 640px) {
    .story li { grid-template-columns: 1fr; gap: 2px; }
  }
</style>
