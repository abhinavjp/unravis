<script lang="ts">
  import { landingMayAutoRun } from '../lib/autorun';
  import { app, go, goHome, openCase, openConnections, openCreatedCase, openKeySetup, openSidePanel, restore, runAnalysis, scope, startCreate, startNewConnection } from '../lib/appState.svelte';
  import { resetWizard, wizard } from '../lib/wizard.svelte';
  import { blockers, caseMade, createNow, createView } from '../lib/wizardCreate.svelte';
  import { createButtonText } from '../lib/copy/wizardCreateCopy';
  import { requestDiscover } from '../lib/discoverIntent';
  import DiscoverEntry from './DiscoverEntry.svelte';
  import StartScreen from './StartScreen.svelte';
  import EnterCase from './EnterCase.svelte';
  import CreateWizard from './CreateWizard.svelte';
  import SourceGallery from './wizard/SourceGallery.svelte';
  import ReviewStep from './wizard/ReviewStep.svelte';
  import CreateProgress from './wizard/CreateProgress.svelte';
  import MasterKeySetup from './MasterKeySetup.svelte';
  import MasterKeyUnlock from './MasterKeyUnlock.svelte';
  import MasterKeyDamaged from './MasterKeyDamaged.svelte';
  import { CASE_RESTORE_TEXT } from '../lib/copy/caseRestoreCopy';

  // The empty and start states of the stage. 'loading': the app is starting. 'connect': no audit source yet (first run).
  // 'running': the first analysis is loading and there is nothing on screen yet. 'start': sources are ready but nothing has run.
  // 'cases': the start screen of a launch (three cards). 'unlock' and 'setup': the master key screens (see lib/screenFlow.ts).
  // 'restoring': an opened case is getting its MongoDB sources back, so the first-run connect step is not shown meanwhile.
  // 'create': the guided "create a new case" flow. TSK-18 mounts the sources step and TSK-19 the review step inside it (snippets `sources`, `review`, `finish`).
  let { mode }: { mode: 'loading' | 'connect' | 'restoring' | 'running' | 'start' | 'cases' | 'unlock' | 'setup' | 'create' } = $props();

  // "This computer" lives in the import drawer. This screen has no case, so it leaves a request and opens the drawer, which shows the panel.
  function openDiscover() { requestDiscover(); openSidePanel('import'); }
  // The case is made and kept on the server. The draft is done with, so it is cleared, then the case opens on its home page.
  function openCreated(id: string) { resetWizard(); void openCreatedCase(id); }
</script>

{#if mode === 'loading'}
  <div class="loading-state"><div class="loading-pulse"></div><strong>Starting Unravis</strong><p>Loading sources and settings.</p></div>
{:else if mode === 'connect'}
  <div class="welcome-state no-connections-state"><h3>Connect an audit source</h3><p>Load saved settings or create a MongoDB connection, then choose databases that contain API audit logs.</p><div class="empty-actions"><button class="primary-button" onclick={startNewConnection}>New connection</button><button class="secondary-button" onclick={() => openConnections('saved')}>Saved connections</button><DiscoverEntry onOpen={openDiscover} /></div></div>
{:else if mode === 'cases'}
  <StartScreen onCreate={startCreate} onOpen={openCase} onSetup={openKeySetup} onAnalysis={() => go('running')}>
    {#snippet enter()}<EnterCase onOpen={openCase} onSetup={openKeySetup} />{/snippet}
  </StartScreen>
{:else if mode === 'create'}
  <CreateWizard onLeave={goHome} busy={createView.running || caseMade()}>
    {#snippet sources()}<SourceGallery />{/snippet}
    {#snippet review()}{#if createView.running || caseMade()}<CreateProgress onOpen={openCreated} />{:else}<ReviewStep />{/if}{/snippet}
    {#snippet finish()}{#if !caseMade()}<button type="button" class="primary-button" disabled={createView.running || blockers().length > 0} onclick={() => void createNow()}>{createButtonText(wizard.sources.length, createView.running)}</button>{/if}{/snippet}
  </CreateWizard>
{:else if mode === 'unlock'}
  {#if app.lockState === 'damaged'}<MasterKeyDamaged />{:else}<MasterKeyUnlock />{/if}
{:else if mode === 'setup'}
  <MasterKeySetup />
{:else if mode === 'restoring'}
  <div class="skeleton" role="status" aria-label={CASE_RESTORE_TEXT.workingLabel}>
    <div class="skeleton-figures"><div class="skeleton-block"></div><div class="skeleton-block"></div><div class="skeleton-block"></div><div class="skeleton-block"></div><div class="skeleton-block"></div></div>
    <div class="skeleton-block skeleton-hero"></div>
    <p class="restoring-note">{CASE_RESTORE_TEXT.working} <button type="button" class="quiet-button" onclick={restore.cancel}>{CASE_RESTORE_TEXT.cancelButton}</button></p>
  </div>
{:else if mode === 'running'}
  <div class="skeleton" role="status" aria-label="Reading selected audit sources">
    <div class="skeleton-figures"><div class="skeleton-block"></div><div class="skeleton-block"></div><div class="skeleton-block"></div><div class="skeleton-block"></div><div class="skeleton-block"></div></div>
    <div class="skeleton-block skeleton-hero"></div>
  </div>
{:else}
  <div class="welcome-state">
    <h3>Start an API comparison</h3>
    {#if app.info?.demo}
      <p>Sample audit sources are ready. Choose the period and filters above, then run the analysis.</p>
    {:else}
      <p>Your sources are ready. Choose the period and filters above, then run the analysis.</p>
    {/if}
    {#if !landingMayAutoRun(scope.query)}<p>This period is longer than 24 hours, so it does not run by itself. Press Run analysis to read it, or pick a shorter range above.</p>{/if}
    {#if app.sources.length && !scope.query.sourceIds.length}<p>Choose at least one database source from the Sources button above to run the analysis.</p>{/if}
    <button class="secondary-button" disabled={!scope.query.sourceIds.length || !scope.dateInputsValid} onclick={() => runAnalysis()}>Run analysis</button>
  </div>
{/if}

<style>
  .restoring-note { margin: 0; color: var(--muted, inherit); text-align: center; }
</style>
