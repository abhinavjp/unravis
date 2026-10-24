<script lang="ts">
  import { timeZone } from '../lib/timeZone.svelte';
  // One switch for every timestamp and chart axis: UTC or the browser's own time zone. Mount it once, in the header.
  const options = [{ id: 'utc', label: 'UTC' }, { id: 'local', label: 'Local' }] as const;
</script>

<div class="zone-toggle" role="group" aria-label="Show times in">
  {#each options as option (option.id)}
    <button type="button" aria-pressed={timeZone.choice === option.id}
      title={option.id === 'utc' ? 'Show every time in UTC' : 'Show every time in your browser time zone, ' + timeZone.browserZone}
      onclick={() => timeZone.set(option.id)}>{option.label}{#if option.id === 'local'}<span class="zone-name"> ({timeZone.browserZone})</span>{/if}</button>
  {/each}
</div>

<style>
  .zone-toggle { display: inline-flex; flex-shrink: 0; border: var(--hairline-width) solid var(--hairline); border-radius: var(--radius-sm); overflow: hidden; }
  .zone-toggle button { min-height: 30px; padding: var(--space-1) var(--space-3); border: 0; border-radius: 0; background: transparent; color: var(--text-2); font: inherit; font-size: var(--fs-2); font-weight: 550; white-space: nowrap; cursor: pointer; }
  .zone-toggle button + button { border-inline-start: var(--hairline-width) solid var(--hairline); }
  .zone-toggle button:hover { color: var(--text); background: var(--surface-2); }
  .zone-toggle button[aria-pressed='true'] { color: var(--text); background: var(--surface-2); box-shadow: inset 0 -2px 0 var(--accent); }
  .zone-toggle button:focus-visible { outline: 2px solid var(--focus); outline-offset: -2px; }
  @media (max-width: 1100px) { .zone-name { display: none; } }
</style>
