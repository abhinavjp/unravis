<script module lang="ts">
  export interface RailStep { id: string; label: string }
</script>

<script lang="ts">
  import Sticker from './Sticker.svelte';

  // A left rail of steps. The current step has a 2 px accent marker and aria-current="step"; a finished step shows a Done sticker;
  // a step that was not needed says so; a step still ahead is hatched.
  let { steps, current, done = [], skipped = [], label = 'Steps' }: { steps: RailStep[]; current: string; done?: string[]; skipped?: string[]; label?: string } = $props();
</script>

<nav class="u-rail" aria-label={label}>
  <ol>
    {#each steps as step (step.id)}
      {@const isCurrent = step.id === current}
      {@const isDone = !isCurrent && done.includes(step.id)}
      {@const isSkipped = !isCurrent && !isDone && skipped.includes(step.id)}
      <li class:current={isCurrent} class:ahead={!isCurrent && !isDone && !isSkipped} aria-current={isCurrent ? 'step' : undefined}>
        <span class="u-rail-label">{step.label}</span>
        {#if isDone}<Sticker tone="ok" label="Done" />{:else if isSkipped}<Sticker label="Not needed" />{/if}
      </li>
    {/each}
  </ol>
</nav>

<style>
  .u-rail ol { list-style: none; margin: 0; padding: 0; display: grid; gap: 2px; }
  .u-rail li { position: relative; display: flex; align-items: center; justify-content: space-between; gap: 8px; min-height: 32px; padding: 4px 10px 4px 14px; color: var(--text-2); font-size: 13px; }
  .u-rail li::before { content: ''; position: absolute; left: 0; top: 6px; bottom: 6px; width: 2px; background: transparent; }
  .u-rail li.current { color: var(--text); font-weight: 600; background: var(--surface-2); }
  .u-rail li.current::before { background: var(--accent); }
  .u-rail li.ahead::before { background: var(--hatch); width: 4px; opacity: .7; }
</style>
