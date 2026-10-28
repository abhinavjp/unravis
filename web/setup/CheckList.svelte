<script lang="ts">
  import Sticker from '../src/brand/components/Sticker.svelte';
  import { checkTone, checkWord, type Check } from './logic.ts';

  // The preflight checks: what was checked, its readiness word, what was found and, when it is not ready, what to do.
  let { checks, label = 'Checks' }: { checks: Check[]; label?: string } = $props();
</script>

<section class="checks" aria-label={label}>
  <h2>{label}</h2>
  <ul>
    {#each checks as check (check.id)}
      <li data-check={check.id}>
        <div class="row"><span class="title">{check.title}</span><Sticker tone={checkTone(check.status)} label={checkWord(check.status)} /></div>
        {#if check.detail}<p class="detail">{check.detail}</p>{/if}
        {#if check.fix && check.status !== 'ready'}<p class="fix">{check.fix}</p>{/if}
      </li>
    {/each}
  </ul>
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
</style>
