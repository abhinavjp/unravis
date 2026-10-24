<script lang="ts">
  import { landingMayAutoRun } from '../lib/autorun';
  import { app, openConnections, runAnalysis, scope, startNewConnection } from '../lib/appState.svelte';

  // The empty and start states of the stage. 'loading': the app is starting. 'connect': no audit source yet (first run).
  // 'running': the first analysis is loading and there is nothing on screen yet. 'start': sources are ready but nothing has run.
  let { mode }: { mode: 'loading' | 'connect' | 'running' | 'start' } = $props();
</script>

{#if mode === 'loading'}
  <div class="loading-state"><div class="loading-pulse"></div><strong>Starting Unravis</strong><p>Loading sources and settings.</p></div>
{:else if mode === 'connect'}
  <div class="welcome-state no-connections-state"><h3>Connect an audit source</h3><p>Load saved settings or create a MongoDB connection, then choose databases that contain API audit logs.</p><div class="empty-actions"><button class="primary-button" onclick={startNewConnection}>New connection</button><button class="secondary-button" onclick={() => openConnections('saved')}>Saved connections</button></div></div>
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
    <button class="primary-button" disabled={!scope.query.sourceIds.length || !scope.dateInputsValid} onclick={() => runAnalysis()}>Run analysis</button>
  </div>
{/if}
