<script lang="ts">
  import { comparisonLabel, STARTUP_TEXT, STARTUP_TITLE } from './lib/copy/startupCopy';
  import { scopeProblem } from './lib/autorun';
  import { VALIDATION_MESSAGE } from './lib/api';
  import { WAIT_TEXT } from './lib/copy/waitCopy';
  import { app, backToPreviousWindow, closeCoverage, dropRecord, evidence, initialize, investigateWindow, navigation, openComparison, openWhere, closeGlossary, closeImportPopup, closeSidePanel, onWindowKey, openCaseHome, openCaseImport, openTracing, panels, resetInvestigation, restore, results, runAnalysis, scope, startApp } from './lib/appState.svelte';
  import { codeUi } from './lib/codeModeUi.svelte';
  import AppHeader from './components/AppHeader.svelte';
  import UpdateBanner from './components/UpdateBanner.svelte';
  import { checkWhatsNewOnStart, startUpdateWatch, updates, whatsNew } from './lib/updateState.svelte';
  import { loadNotes, normalizeVersion, parseableVersion } from './lib/updateNotes';
  import SubBar from './components/SubBar.svelte';
  import Landing from './components/Landing.svelte';
  import { zoomView } from './lib/zoomView.svelte';
  import { timeZone } from './lib/timeZone.svelte';
  import { backLabel, investigateState } from './lib/promote';
  import { lazyComponent } from './lib/lazy';
  import { readSubbarHint, writeSubbarHint } from './lib/subbarHint';
  import { setDocTitle } from './lib/docTitle';
  import { placeKey, screenTitle, settleFocus } from './lib/screenFocus';

  // The page shell. The state and the logic live in lib/appState.svelte.ts; each region of the page is its own component.
  // startApp registers the effects and the mount work (theme, first load, the glossary event) for this component.
  startApp();

  // Everything past the first screen is fetched on first use, so the code (and the copy text) of the panels, drawers and analysis views
  // stays out of the entry bundle. The shell and the Landing screens (the first paint) are static imports above.
  const CaseReconnect = lazyComponent(() => import('./components/CaseReconnect.svelte'));
  const ConnectionsDrawer = lazyComponent(() => import('./components/ConnectionsDrawer.svelte'));
  const CoverageDrawer = lazyComponent(() => import('./components/CoverageDrawer.svelte'));
  const Glossary = lazyComponent(() => import('./components/Glossary.svelte'));
  const PatternsPanel = lazyComponent(() => import('./components/PatternsPanel.svelte'));
  const LedgerPanel = lazyComponent(() => import('./components/LedgerPanel.svelte'));
  const ExportPanel = lazyComponent(() => import('./components/ExportPanel.svelte'));
  const CodeModePopup = lazyComponent(() => import('./components/CodeModePopup.svelte'));
  const ImportPopup = lazyComponent(() => import('./lib/components/importPopup/ImportPopup.svelte'));
  const BlackboxPanel = lazyComponent(() => import('./components/BlackboxPanel.svelte'));
  const ServersPanel = lazyComponent(() => import('./components/ServersPanel.svelte'));
  const McpAccessPanel = lazyComponent(() => import('./components/McpAccessPanel.svelte'));
  const UpdatesPanel = lazyComponent(() => import('./components/UpdatesPanel.svelte'));
  const WhatsNewDialog = lazyComponent(() => import('./components/WhatsNewDialog.svelte'));
  const CaseHome = lazyComponent(() => import('./lib/components/CaseHome.svelte'));
  const analysisViews = [
    () => import('./components/Pulse.svelte'), () => import('./components/RollupView.svelte'), () => import('./components/WhereView.svelte'), () => import('./components/EvidenceView.svelte'),
    () => import('./components/RecordDetail.svelte'), () => import('./components/ZoomStrip.svelte'), () => import('./components/ProvenanceNote.svelte')
  ] as const;
  const [pulseModule, rollupModule, whereModule, evidenceModule, recordModule, zoomModule, provenanceModule] = analysisViews;
  const Pulse = lazyComponent(pulseModule);
  const RollupView = lazyComponent(rollupModule);
  const WhereView = lazyComponent(whereModule);
  const EvidenceView = lazyComponent(evidenceModule);
  const RecordDetailPanel = lazyComponent(recordModule);
  const ZoomStrip = lazyComponent(zoomModule);
  const ProvenanceNote = lazyComponent(provenanceModule);

  const info = $derived(app.info);
  // Read the update status once the app is up, then every ten minutes. It is a local call; the program's own timer does the checking.
  $effect(() => { if (info) return startUpdateWatch(); });
  // After an update the tour opens once, but only when the bundled notes know the installed version. A missing notes file keeps it quiet.
  const installedVersion = $derived(updates.reply?.current ?? '');
  $effect(() => {
    const current = installedVersion;
    if (!info || !parseableVersion(current)) return;
    let live = true;
    void loadNotes().then((all) => { if (live && all.some((r) => normalizeVersion(r.version) === normalizeVersion(current))) checkWhatsNewOnStart(current); });
    return () => { live = false; };
  });
  const analysis = $derived(results.analysis);
  const loading = $derived(results.loading);
  const depth = $derived(navigation.depth);
  const pageHeading = $derived.by(() => {
    if (app.initError || app.screen === 'loading') return 'Unravis';
    if (app.screen === 'case') return 'Case home';
    return screenTitle(app.screen, depth) || 'Unravis';
  });
  // Each screen names the tab ("All cases - Unravis"); the case page names itself. When the app moves to another screen (or climbs a level of the
  // analysis) the new heading takes the keyboard focus unless a screen has already placed it, so Tab does not start again at the skip link.
  $effect(() => {
    const label = screenTitle(app.screen, depth);
    if (label) return setDocTitle(label);
  });
  let placed = '';
  $effect(() => {
    const here = placeKey(app.screen, depth);
    const before = placed;
    placed = here;
    if (before === '' || before === here || before === 'loading' || app.screen === 'loading') return;
    return settleFocus();
  });
  // Once a run starts (or an analysis is on screen) the analysis views are fetched, so they are ready by the time the figures arrive.
  $effect(() => { if (loading || analysis) for (const importView of analysisViews) void importView().catch(() => {}); });
  const zone = $derived(scope.zone);
  const sourceLabels = $derived(app.sourceLabels);
  const failingSources = $derived(results.failingSources);
  const record = $derived(evidence.record);
  // The time charts follow a view window inside the queried one. A new analysis on screen is a new top of the zoom steps.
  $effect(() => { zoomView.follow(analysis ?? null); });
  const zoomModel = $derived(zoomView.model);
  // A hand-off from the timeline explains an empty strip (SQL or IIS events but no API requests), only while the analysis shows that same bar.
  const emptyNote = $derived.by(() => {
    const handoff = app.tracingHandoff;
    const brush = zoomModel?.brush;
    return handoff && brush && Math.abs(handoff.start - brush.rootStart) < 1000 && Math.abs(handoff.end - brush.rootEnd) < 1000 ? handoff.text : '';
  });
  const zoomRead = $derived(zoomView.viewAnalysis);
  // "Investigate this window" is offered once zoomed. "Back to ..." shows while the queried window is the one the last promotion made.
  const investigate = $derived(investigateState({ isRoot: zoomModel?.isRoot ?? true, runLoading: loading, stale: results.displayedRunIsStale }));
  const windowBackLabel = $derived(results.windowBack ? backLabel(results.windowBack) : null);
  // Holds the scope bar's space while the first load runs, so the page does not jump when the bar appears.
  const subbarHint = readSubbarHint(window.innerWidth);
  const subbarShown = $derived(!!info && !app.firstRun && app.screen === 'running');
  $effect(() => {
    if (!subbarShown) return;
    const measure = () => writeSubbarHint(window.innerWidth, document.querySelector('section.scope')?.getBoundingClientRect().height ?? 0);
    const frame = requestAnimationFrame(measure);
    window.addEventListener('resize', measure);
    return () => { cancelAnimationFrame(frame); window.removeEventListener('resize', measure); };
  });
  // Opens the Import files popup, so data can be added without leaving the analysis.
  function addData() { openCaseImport(); }
  // "Open timeline" in the popup's result: the timeline lives on the case page, so the analysis screen goes there first.
  function openTimeline() { if (app.screen !== 'case') openCaseHome(); openTracing(); }
</script>

<svelte:head><title>Unravis</title></svelte:head>
<svelte:window onkeydown={onWindowKey} />

<div class="app-shell">
  <!-- The page heading comes first in the document (before the header and the filter panel), so the outline starts at h1 and a screen reader meets the page name first. It is hidden, so nothing moves. -->
  <h1 class="visually-hidden">{pageHeading}</h1>
  <AppHeader />
  <UpdateBanner />

  {#if app.initError}<div class="banner error-banner init-banner" role="alert"><strong>{STARTUP_TITLE}</strong><span>{app.initError}</span>{#if app.initCanRetry}<button class="secondary-button" onclick={() => { app.initError = ''; void initialize(); }}>Retry</button>{/if}</div>{/if}

  {#if app.lockError}<div class="banner error-banner" role="alert"><span>{app.lockError}</span><button class="quiet-button" onclick={() => { app.lockError = ''; }}>Dismiss</button></div>{/if}

  {#if app.workspaceNote}<div class="notice" role="status" data-testid="workspace-note"><span>{app.workspaceNote}</span> <button class="quiet-button" onclick={() => { app.workspaceNote = ''; }}>Dismiss</button></div>{/if}

  {#if subbarShown}<SubBar />{:else if (!info || app.screen === 'loading') && subbarHint > 0}<div class="subbar-reserve" style:height="{subbarHint}px" aria-hidden="true"></div>{/if}

    {#if panels.connectionsOpen}<ConnectionsDrawer />{/if}

    {#if panels.coverage}<CoverageDrawer sourceId={panels.coverage.id} sourceLabel={panels.coverage.label} start={scope.query.start} end={scope.query.end} onClose={closeCoverage} />{/if}

    {#if panels.glossaryOpen}<Glossary onClose={closeGlossary} focusTerm={panels.glossaryTerm} />{/if}
    {#if panels.sidePanel === 'patterns'}<PatternsPanel query={scope.query} {zone} {sourceLabels} onClose={closeSidePanel} />{/if}
    {#if panels.sidePanel === 'ledger'}<LedgerPanel onClose={closeSidePanel} />{/if}
    {#if panels.sidePanel === 'export'}<ExportPanel query={scope.query} {zone} {sourceLabels} onClose={closeSidePanel} />{/if}
    {#if panels.importPopup}<ImportPopup caseId={panels.importPopup.caseId} caseTitle={panels.importPopup.caseTitle} query={scope.query} {zone} retryServerName={panels.importPopup.retryServerName} onClose={() => void closeImportPopup()} onOpenTimeline={openTimeline} />{/if}
    {#if panels.sidePanel === 'blackbox'}<BlackboxPanel onClose={closeSidePanel} />{/if}
    {#if panels.sidePanel === 'servers'}<ServersPanel onClose={closeSidePanel} />{/if}
    {#if panels.sidePanel === 'mcp'}<McpAccessPanel onClose={closeSidePanel} />{/if}
    {#if panels.sidePanel === 'updates'}<UpdatesPanel onClose={closeSidePanel} />{/if}
    {#if whatsNew.open}<WhatsNewDialog current={installedVersion} />{/if}
    {#if codeUi.open}<CodeModePopup />{/if}

    <main id="main-content" tabindex="-1" class="stage" class:with-drawer={depth === 2 && analysis != null} aria-busy={loading} inert={panels.modalOpen}>
      <div class="stage-inner">
        {#if !app.initError && (!info || app.screen === 'loading')}
          <Landing mode="loading" />
        {:else if app.screen === 'unlock'}
          <Landing mode="unlock" />
        {:else if app.screen === 'setup'}
          <Landing mode="setup" />
        {:else if app.screen === 'start'}
          <Landing mode="cases" />
        {:else if app.screen === 'create'}
          <!-- The guided flow. createRoute() leads here only once WIZARD_READY is on (end of phase 05). -->
          <Landing mode="create" />
        {:else if app.screen === 'case'}
          <CaseHome />
        {:else if app.firstRun && restore.active}
          <!-- A case that used MongoDB is being reopened: reconnecting quietly, or asking for the sign-in, never the first-run connect step. -->
          {#if restore.working}<Landing mode="restoring" />{:else}<CaseReconnect />{/if}
        {:else if app.firstRun}
          <Landing mode="connect" />
        {:else if info}
          {#if restore.needsAttention || restore.working}<CaseReconnect compact />{/if}
          {#if results.error}<div class="banner error-banner" role="alert"><strong>{comparisonLabel(results.error, [VALIDATION_MESSAGE, scopeProblem(scope.query, scope.dateInputsValid)])}</strong><span>{results.error}</span>{#if results.error === WAIT_TEXT.timeout && !loading}<button type="button" class="secondary-button" title={WAIT_TEXT.retryTitle} onclick={() => runAnalysis()}>{WAIT_TEXT.retry}</button>{/if}</div>{/if}
          {#if analysis}
            {#if failingSources.length}
              <div class="banner source-failure" role="alert"><strong>{failingSources.length === analysis.sources.length ? 'No sources returned data' : 'Some sources failed'}</strong>
                {#each failingSources as failed}<span><b>{sourceLabels.get(failed.sourceId) ?? failed.sourceId}:</b> {failed.error || failed.indexWarning || 'Source unavailable'}</span>{/each}
              </div>
            {/if}
            {#if zoomModel && depth !== 3}
              <ZoomStrip model={zoomModel} {emptyNote} zone={timeZone.zone} announcement={zoomView.announcement} {investigate} backLabel={windowBackLabel} onInvestigate={investigateWindow} onWindowBack={backToPreviousWindow} onBrush={win => zoomView.setWindow(win)} onBack={() => zoomView.back()} onEarlier={() => zoomView.earlier()} onLater={() => zoomView.later()} onZoomIn={() => zoomView.zoomIn()} onZoomOut={() => zoomView.zoomOut()} onReset={() => zoomView.reset()} onJump={index => zoomView.jumpTo(index)} />
              {#if zoomRead}<ProvenanceNote provenance={zoomRead.provenance} {sourceLabels} />{/if}
            {/if}
            <div class="stage-views" inert={loading}>
              <!-- D0 Pulse -->
              <div class="view" class:off={depth > 0}>
                <Pulse {analysis} {zone} onOpen={openWhere} />
                <div class="pulse-actions"><button type="button" class="primary-button" onclick={() => openWhere(navigation.lens)}>Where is it?</button><button type="button" class="secondary-button" onclick={openComparison}>All operations</button><button type="button" class="secondary-button" aria-haspopup="dialog" onclick={addData}>Add files</button><p>Tap a figure, or open the ranked list of operations. Add files to bring in SQL, Redis, IIS or server logs for the same period.</p></div>
                <RollupView />
              </div>

              <!-- D1 Where -->
              <WhereView {analysis} />

              <!-- D3 Record -->
              <div class="view record-view" class:off={depth !== 3}>
                <button type="button" class="secondary-button back-button" onclick={() => dropRecord()}>&larr; Back to the evidence</button>
                {#if evidence.recordError}<div class="inline-error" role="alert">{evidence.recordError}</div>{/if}
                {#if record}<RecordDetailPanel detail={record} sourceLabel={sourceLabels.get(record.sourceId) ?? record.sourceId} onClose={() => dropRecord()} />{/if}
              </div>
            </div>

            <!-- D2 Evidence -->
            <EvidenceView {analysis} />
          {:else if loading}
            <Landing mode="running" />
          {:else}
            <Landing mode="start" />
          {/if}
          <div class="notice frozen-run-note" role="status">{scope.restoredInTab ? 'Restored this tab’s investigation filters. ' : ''}{scope.investigationNotice} <button class="quiet-button" onclick={resetInvestigation}>Reset investigation</button></div>
        {:else if app.initError}
          <div class="welcome-state init-empty" data-testid="init-empty"><p>{STARTUP_TEXT.empty}</p></div>
        {/if}
      </div>
    </main>
</div>

<style>
  .subbar-reserve { flex: 0 0 auto; }
  /* The start error lines up with the page gutter (24 px, 16 px on a phone), not the window edge (CASES-27). */
  .init-banner { align-items: center; margin: var(--space-3) 24px 0; }
  @media (max-width: 720px) { .init-banner { margin-inline: 16px; } }
  /* Back is a real button (B8): it sits at the left of the record rather than stretching across it. */
  .record-view > .back-button { justify-self: start; align-self: flex-start; }
</style>
