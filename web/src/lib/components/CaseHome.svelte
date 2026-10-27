<script lang="ts">
  import { onMount } from 'svelte';
  import { api, ApiError } from '../api';
  import { app, goHome, openAnalysis, openCaseImport, openSidePanel, panels } from '../appState.svelte';
  import { believedState, caseCode, deleteRetry, getCaseView, isCaseChanged, isCaseGone, listImports, reopenCase, retryInPage, runRetry, type CaseView, type ImportRecord, type RetryItem } from '../cases';
  import { reopenButton, retryAction } from '../caseLifecycle';
  import { CASE_HOME_TEXT, retryKindLabel } from '../copy/caseHomeCopy';
  import { errorText, importerLabel, formatBytes } from '../copy/importCopy';
  import { fetchHealthRuns, type HealthRun } from '../health';
  import { stateLabel } from '../ledger';
  import { ledger, refreshLedger } from '../ledgerStore.svelte';
  import { buildStory, shortText } from '../story';
  import HealthPanel from './HealthPanel.svelte';

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
  let imports = $state.raw<ImportRecord[]>([]);
  let health = $state.raw<HealthRun[]>([]);
  let theoryProblem = $state(false);
  let sharedCount = $state(0);
  let copying = $state(false);
  let copyNote = $state('');
  let copyProblem = $state('');
  let controller: AbortController | null = null;
  let loadedFor = '';

  const story = $derived(buildStory({ info, imports, health, theories: ledger.items, history: info?.history }));
  const theories = $derived(ledger.items);
  const keptFiles = $derived(imports.filter(record => record.status === 'ok'));
  // A case that is not open (closed, archived) or is untrusted is read-only: nothing can be added, so the buttons that add say so up front.
  const readOnly = $derived(!!info && (!!info.untrusted || believedState(info) !== 'open'));
  const reopenState = $derived(info ? reopenButton(info, reopening) : 'hidden');
  const retries = $derived(info?.retries ?? []);

  async function load(id: string) {
    controller?.abort();
    const mine = new AbortController();
    controller = mine;
    phase = 'loading';
    loadedFor = id;
    copyNote = '';
    copyProblem = '';
    sharedCount = 0;
    changed = false;
    lifeProblem = '';
    retryNote = {};
    const [view, files, runs, theory] = await Promise.allSettled([getCaseView(id, mine.signal), listImports(id, mine.signal), fetchHealthRuns(id, mine.signal), refreshLedger()]);
    if (mine.signal.aborted || id !== caseId) return;
    const found = view.status === 'fulfilled' ? view.value : null;
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
      const [view, files] = await Promise.all([getCaseView(id), listImports(id)]);
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
    if (isCaseChanged(error)) { changed = true; return; }
    lifeProblem = error instanceof Error && 'status' in error ? errorText(error as never) : errorText({ status: 0, reason: 'unreachable', message: '' });
  }

  async function reopen() {
    const id = caseId;
    if (!info || reopening || reopenState !== 'enabled') return;
    reopening = true;
    lifeProblem = '';
    try {
      await reopenCase(id, believedState(info));
      await rereadFiles();
    } catch (error) { lifeFailed(error); } finally { reopening = false; }
  }

  function reload() { changed = false; lifeProblem = ''; if (caseId) void load(caseId); }

  async function tryAgain(item: RetryItem) {
    const id = caseId;
    if (!info || tryingId !== 0) return;
    if (retryAction(item, info) === 'add-file') { openCaseImport(); return; }
    tryingId = item.id;
    retryNote = { ...retryNote, [item.id]: '' };
    try {
      await runRetry(id, item.id);
      await rereadFiles();
    } catch (error) {
      if (retryInPage(error)) retryNote = { ...retryNote, [item.id]: CASE_HOME_TEXT.retryInPage };
      else if (isCaseChanged(error) || isCaseGone(error)) lifeFailed(error);
      else retryNote = { ...retryNote, [item.id]: CASE_HOME_TEXT.retryFailed };
    } finally { tryingId = 0; }
  }

  async function dropRetry(item: RetryItem) {
    const id = caseId;
    if (tryingId !== 0) return;
    tryingId = item.id;
    try { await deleteRetry(id, item.id); await rereadFiles(); } catch (error) { lifeFailed(error); } finally { tryingId = 0; }
  }

  $effect(() => {
    const id = caseId;
    if (id && id !== loadedFor) void load(id);
  });
  onMount(() => () => controller?.abort());

  const when = (value: string): string => {
    const ms = Date.parse(value);
    return Number.isFinite(ms) ? new Date(ms).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' }) : value;
  };
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
  {:else}
    <header class="head">
      <p class="eyebrow">{CASE_HOME_TEXT.heading} · {caseCode(caseId)}</p>
      <h2>{info?.title || caseCode(caseId)}</h2>
      <p class="note">{CASE_HOME_TEXT.intro}</p>
      <dl class="counts">
        {#if info?.created}<div><dt>{CASE_HOME_TEXT.created}</dt><dd>{when(info.created)}</dd></div>{/if}
        <div><dt>{CASE_HOME_TEXT.filesCount}</dt><dd>{keptFiles.length}</dd></div>
        <div><dt>{CASE_HOME_TEXT.theoriesCount}</dt><dd>{theories.length}</dd></div>
        <div><dt>{CASE_HOME_TEXT.checksCount}</dt><dd>{health.length}</dd></div>
      </dl>
      {#if info?.untrusted}<p class="notice" role="status">{CASE_HOME_TEXT.untrusted}</p>
      {:else if info && believedState(info) === 'closed'}<p class="notice" role="status">{CASE_HOME_TEXT.closedNote}</p>{/if}
      {#if reopenState !== 'hidden'}
        <div class="actions">
          <button type="button" class="secondary-button" disabled={reopenState !== 'enabled'} aria-busy={reopenState === 'busy'} onclick={() => void reopen()}>{reopenState === 'busy' ? CASE_HOME_TEXT.reopening : CASE_HOME_TEXT.reopenButton}</button>
        </div>
        {#if reopenState === 'enabled'}<p class="note">{CASE_HOME_TEXT.reopenHint}</p>{/if}
      {/if}
      <div aria-live="polite">
        {#if changed}
          <div class="inline-error" role="alert">{CASE_HOME_TEXT.changedElsewhere} <button type="button" class="secondary-button" onclick={reload}>{CASE_HOME_TEXT.reload}</button></div>
        {/if}
        {#if lifeProblem}<div class="inline-error" role="alert">{lifeProblem}</div>{/if}
      </div>
    </header>

    <section class="block" aria-labelledby="home-analysis">
      <h3 id="home-analysis">{CASE_HOME_TEXT.analysisHeading}</h3>
      <p class="note">{CASE_HOME_TEXT.analysisText}</p>
      <div><button type="button" class="primary-button" onclick={openAnalysis}>{CASE_HOME_TEXT.analysisButton}</button></div>
      <p class="note">{CASE_HOME_TEXT.analysisHint}</p>
    </section>

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

    {#if retries.length > 0}
      <section class="block" aria-labelledby="home-retries">
        <h3 id="home-retries">{CASE_HOME_TEXT.retryHeading}</h3>
        <p class="note">{CASE_HOME_TEXT.retryIntro}</p>
        <ul class="retries">
          {#each retries as item (item.id)}
            {@const action = info ? retryAction(item, info) : 'hidden'}
            <li>
              <div class="retry-main">
                <span class="state">{retryKindLabel(item.kind)}</span>
                {#if item.label}<strong>{item.label}</strong>{/if}
                {#if retryNote[item.id]}<p class="note" role="status">{retryNote[item.id]}</p>{/if}
                {#if action === 'reopen-first'}<p class="note">{CASE_HOME_TEXT.retryClosed}</p>{/if}
              </div>
              {#if action !== 'hidden'}
                <div class="actions">
                  {#if action !== 'reopen-first'}
                    <button type="button" class="secondary-button" disabled={tryingId !== 0} onclick={() => void tryAgain(item)}>{tryingId === item.id ? CASE_HOME_TEXT.retrying : action === 'add-file' ? CASE_HOME_TEXT.retryAddFile : CASE_HOME_TEXT.retryButton}</button>
                  {/if}
                  <button type="button" class="secondary-button" disabled={tryingId !== 0 || action === 'reopen-first'} onclick={() => void dropRetry(item)}>{CASE_HOME_TEXT.retryDrop}</button>
                </div>
              {/if}
            </li>
          {/each}
        </ul>
      </section>
    {/if}

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
</div>

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
  .story li { position: relative; display: grid; grid-template-columns: 150px 1fr; gap: 12px; padding: 8px 0 8px 16px; }
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
  .retries { list-style: none; margin: 0; padding: 0; display: grid; gap: 10px; }
  .retries li { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 8px 12px; font-size: 14px; }
  .retry-main { display: grid; gap: 4px; min-width: 0; overflow-wrap: anywhere; }
  .retries .state { justify-self: start; padding: 0 6px; color: var(--text-2); font-size: 12px; border: 1px solid var(--line); border-radius: 4px; }
  .offer { display: grid; gap: 6px; padding: 10px 12px; border: 1px dashed var(--line); border-radius: var(--radius-sm); }
  @media (max-width: 640px) {
    .story li { grid-template-columns: 1fr; gap: 2px; }
  }
</style>
