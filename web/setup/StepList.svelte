<script lang="ts">
  import Button from '../src/brand/components/Button.svelte';
  import Sticker from '../src/brand/components/Sticker.svelte';
  import { stepTone, stepWord, type StepEvent } from './logic.ts';

  // Each action of a run with its status sticker and a mono line of detail. A failed step shows the fix in plain words and,
  // when the run can be tried again from here, a Retry button.
  let { rows, fix = '', onretry }: { rows: StepEvent[]; fix?: string; onretry?: () => void } = $props();
</script>

<ol class="steps" aria-label="Steps">
  {#each rows as row (row.id)}
    <li data-step={row.id} data-state={row.state}>
      <div class="row"><span class="title">{row.title}</span><Sticker tone={stepTone(row.state)} label={stepWord(row.state)} /></div>
      {#if row.detail}<p class="detail mono">{row.detail}</p>{/if}
      {#if row.state === 'failed' && (fix || onretry)}
        <div class="fix">
          {#if fix}<p>{fix}</p>{/if}
          {#if onretry}<Button onclick={onretry}>Retry</Button>{/if}
        </div>
      {/if}
    </li>
  {/each}
</ol>

<style>
  ol { list-style: none; margin: 0; padding: 0; display: grid; }
  li { padding: 6px 0; border-top: 1px solid var(--line-soft); }
  li:last-child { border-bottom: 1px solid var(--line-soft); }
  .row { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
  .title { font-size: 13px; }
  li[data-state='waiting'] .title, li[data-state='skipped'] .title { color: var(--text-2); }
  .detail { margin: 2px 0 0; font-size: 12px; color: var(--text-2); overflow-wrap: anywhere; }
  .fix { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-top: 4px; }
  .fix p { margin: 0; font-size: 12px; }
</style>
