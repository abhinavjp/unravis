<script lang="ts">
  import { onMount } from 'svelte';
  import type { Snippet } from 'svelte';
  import { listCases } from '../lib/cases';
  import type { CaseInfo } from '../lib/cases';
  import { getMasterKeyStatus } from '../lib/masterKeyApi';
  import { closesCases } from '../lib/masterKey';
  import type { LockState } from '../lib/masterKey';
  import { KEY_TEXT } from '../lib/copy/masterKeyCopy';
  import { START_TEXT, openLabel } from '../lib/copy/startCopy';
  import { startView } from '../lib/startScreen';
  import { app, noteKeyStatus, noteLocked } from '../lib/appState.svelte';
  import HelpNote from './HelpNote.svelte';

  // The first screen of a launch: three cards, always shown. Use an existing case, enter an existing case (a slot for the code field), create a new one.
  // It reads the master key status first. A locked key shows no titles and no counts and hands over to the unlock screen; the case list is never asked for.
  // `onCreate` opens the create flow (the import panel until the guided flow exists), `onOpen` opens a case, `onSetup` opens the master key set-up,
  // `onAnalysis` goes on to the analysis of the connected sources.
  let { onCreate, onOpen, onSetup, onAnalysis, enter }: {
    onCreate: () => void;
    onOpen: (id: string) => void;
    onSetup: () => void;
    onAnalysis: () => void;
    enter?: Snippet;
  } = $props();

  let phase = $state<'checking' | 'loading' | 'ready' | 'failed'>('checking');
  let lock = $state<LockState | null>(app.lockState === 'unknown' ? null : app.lockState);
  let statusFailed = $state(false);
  let cases = $state.raw<CaseInfo[]>([]);
  let pages = $state(1);
  let controller: AbortController | null = null;

  const view = $derived(startView(lock, cases, pages));

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
      cases = await listCases(signal);
      phase = 'ready';
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return;
      phase = 'failed';
    }
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
        {#if phase === 'ready' && view.rows.length}
          <ul class="cases">
            {#each view.rows as row (row.id)}
              <li class="case-row">
                <div class="case-info">
                  <strong class="case-title" title={row.fullTitle}>{row.title}</strong>
                  <span class="case-meta">{row.created}{row.created ? ' · ' : ''}{row.counts}</span>
                  {#if row.closed}<span class="case-closed">{row.closedLabel}</span>{/if}
                </div>
                <button type="button" class="secondary-button" aria-label={openLabel(row.fullTitle)} onclick={() => onOpen(row.id)}>{START_TEXT.open}</button>
              </li>
            {/each}
          </ul>
          {#if view.hidden > 0}
            <p class="card-note">{view.shown}</p>
            <button type="button" class="quiet-button" onclick={() => (pages += 1)}>{START_TEXT.showMore}</button>
          {/if}
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
  .case-row { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); min-width: 0; padding: var(--space-2) var(--space-3); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
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
