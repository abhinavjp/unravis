<script lang="ts">
  import type { Snippet } from 'svelte';
  import Sticker from '../src/brand/components/Sticker.svelte';
  import { checkTone, checkWord, readyLine, type Check } from './logic.ts';

  // The preflight checks: what was checked, its readiness word, what was found and, when it is not ready, what to do.
  // The rows that need a look come first and stay open. The ones that are ready fold into one line ("6 more checks are ready") that opens
  // the list, so the one row that matters is not buried under seven green ones (SETUP-A11Y-19). rowNote draws something under a row,
  // such as the "Close Unravis for me" box under the "Unravis open" row it answers.
  let { checks, label = 'Checks', rowNote }: { checks: Check[]; label?: string; rowNote?: Snippet<[Check]> } = $props();
  const needs = $derived(checks.filter((check) => check.status !== 'ready'));
  const ready = $derived(checks.filter((check) => check.status === 'ready'));
</script>

{#snippet row(check: Check)}
  <li data-check={check.id}>
    <div class="row"><span class="title">{check.title}</span><Sticker tone={checkTone(check.status)} label={checkWord(check.status)} /></div>
    {#if check.detail}<p class="detail">{check.detail}</p>{/if}
    {#if check.fix && check.status !== 'ready'}<p class="fix">{check.fix}</p>{/if}
    {#if rowNote}{@render rowNote(check)}{/if}
  </li>
{/snippet}

<section class="checks" aria-label={label}>
  <h2>{label}</h2>
  {#if needs.length}<ul>{#each needs as check (check.id)}{@render row(check)}{/each}</ul>{/if}
  {#if ready.length}
    <details class="ready" data-testid="checks-ready">
      <summary>{readyLine(ready.length, needs.length > 0)}</summary>
      <ul>{#each ready as check (check.id)}{@render row(check)}{/each}</ul>
    </details>
  {/if}
</section>

<style>
  h2 { margin: 0 0 6px; font-size: 13px; font-weight: 600; color: var(--text-2); }
  ul { list-style: none; margin: 0; padding: 0; display: grid; }
  li { padding: 7px 0; border-top: 1px solid var(--line-soft); }
  li:last-child { border-bottom: 1px solid var(--line-soft); }
  .row { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
  .title { min-width: 0; overflow-wrap: anywhere; font-size: 13px; font-weight: 600; }
  p { margin: 2px 0 0; font-size: 12px; }
  .detail { color: var(--text-2); }
  .fix { color: var(--text); }
  .ready { margin-top: 6px; font-size: 13px; }
  .ready summary { box-sizing: border-box; min-height: 24px; cursor: pointer; color: var(--text-2); padding: 4px 0; }
  .ready summary:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .ready ul { margin-top: 4px; }
</style>
