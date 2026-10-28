<script lang="ts">
  import { app } from '../lib/appState.svelte';
  import { caseUse, stopUsingInCase, useInCase, type CaseUse } from '../lib/blackboxCase';
  import { caseUseApi } from '../lib/blackboxCaseApi';
  import { type BlackboxItem } from '../lib/blackbox';
  import { BB_TEXT as T } from '../lib/copy/blackboxCopy';
  import { onMount } from 'svelte';

  // Add this Blackbox to the case being worked in, so Step 9 reads it. The tag holds the Blackbox name and nothing else.
  let { item }: { item: BlackboxItem } = $props();

  const caseId = $derived(app.activeCaseId);
  // Not placed, Not found and Unreachable read nothing now, so adding them to a case would only give an empty Step 9.
  const placed = $derived(item.state.state !== 'not-placed');
  const readable = $derived(placed && item.state.state !== 'unreachable' && item.state.state !== 'not-found');
  let use = $state<CaseUse | null>(null);
  let busy = $state('');
  let note = $state('');
  let problem = $state('');

  onMount(() => { void refresh(); });

  async function refresh() {
    if (!caseId) { use = null; return; }
    busy = T.caseUseChecking;
    try {
      use = caseUse(await caseUseApi.listTags(caseId), item.name);
    } catch (error) {
      use = null;
      problem = error instanceof Error && error.message ? error.message : T.caseUseFailed;
    } finally {
      busy = '';
    }
  }
  async function run(change: () => Promise<CaseUse>, done: string) {
    if (busy || !caseId) return;
    busy = T.caseUseChecking;
    note = '';
    problem = '';
    try {
      use = await change();
      note = done;
    } catch (error) {
      problem = error instanceof Error && error.message ? error.message : T.caseUseFailed;
    } finally {
      busy = '';
    }
  }
  const add = () => run(() => useInCase(caseUseApi, caseId, item.name), T.caseUseAdded);
  const remove = () => run(() => stopUsingInCase(caseUseApi, caseId, item.name), T.caseUseRemoved);
</script>

<section class="bb-case" aria-label={T.caseUseTitle}>
  <h4>{T.caseUseTitle}</h4>
  <p class="bb-note">{T.caseUseIntro}</p>
  {#if !caseId}
    <p class="bb-note">{T.caseUseNoCase}</p>
  {:else if !placed}
    <p class="bb-note">{T.caseUseNotPlaced}</p>
  {:else if !readable && use?.state !== 'this'}
    <p class="bb-note">{T.caseUseNotReadable}</p>
  {:else if use?.state === 'this'}
    <p>{T.caseUseOn}</p>
  {:else if use?.state === 'other'}
    <p>{T.caseUseOther(use.name)}</p>
  {/if}
  <div aria-live="polite">
    {#if busy}<p class="bb-note" role="status">{busy}</p>{/if}
    {#if note}<p class="bb-ok" role="status">{note}</p>{/if}
    {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
  </div>
  <div class="bb-actions">
    {#if use?.state === 'this'}
      <button type="button" class="secondary-button" disabled={!!busy} onclick={remove}>{T.caseUseStop}</button>
    {:else}
      <button type="button" class="secondary-button" disabled={!!busy || !caseId || !readable || use?.state === 'other'} onclick={add}>{T.caseUseButton}</button>
    {/if}
  </div>
</section>

<style>
  .bb-case { display: grid; gap: var(--space-2); padding-top: var(--space-3); border-top: 1px solid var(--line); min-width: 0; }
  .bb-case h4 { margin: 0; font-size: 14px; font-weight: 600; }
  .bb-case p { margin: 0; font-size: 13px; line-height: 1.5; overflow-wrap: anywhere; }
  .bb-note { color: var(--text-2); font-size: 12px !important; }
  .bb-ok { color: var(--tone-ok); }
  .bb-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
</style>
