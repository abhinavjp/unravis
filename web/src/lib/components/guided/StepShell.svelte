<script lang="ts">
  import type { Snippet } from 'svelte';
  import { GUIDED_TEXT } from '../../copy/guidedCopy';
  import { STEPS, type Sticker as StickerModel } from '../../guided/stepModel';
  import Sticker from './Sticker.svelte';

  // The frame of one step page: title, status sticker, the "In a nutshell" sentence, what could not be tested, the body and Previous / Next.
  let { step, sticker, nutshell = '', reasons = [], onGo, nav = true, children }: {
    step: number; sticker: StickerModel; nutshell?: string; reasons?: string[]; onGo: (step: number) => void; nav?: boolean; children: Snippet;
  } = $props();
  const def = $derived(STEPS[step]);
</script>

<section class="shell" aria-labelledby={'step-title-' + step}>
  <header>
    <h3 id={'step-title-' + step}>Step {step}. {def?.title ?? ''}</h3>
    <Sticker word={sticker.word} tone={sticker.tone} outOfDate={sticker.outOfDate} />
  </header>
  {#if nutshell}
    <div class="nutshell"><p class="label">{GUIDED_TEXT.nutshell}</p><p class="text">{nutshell}</p></div>
  {/if}
  {#if reasons.length > 0}
    <div class="reasons">
      <p class="label">{GUIDED_TEXT.reasonsHeading}</p>
      <ul>{#each reasons as reason (reason)}<li>{reason}</li>{/each}</ul>
    </div>
  {/if}
  <div class="body">{@render children()}</div>
  {#if nav}<footer>
    <button type="button" class="secondary-button" disabled={step <= 0} onclick={() => onGo(step - 1)}>&larr; {GUIDED_TEXT.previousStep}</button>
    <button type="button" class="secondary-button" disabled={step >= STEPS.length - 1} onclick={() => onGo(step + 1)}>{GUIDED_TEXT.nextStep} &rarr;</button>
  </footer>{/if}
</section>

<style>
  .shell { display: grid; gap: 12px; min-width: 0; padding: 16px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  header { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; }
  h3 { margin: 0; font-size: 16px; overflow-wrap: anywhere; }
  .nutshell { display: grid; gap: 2px; padding: 8px 12px; background: var(--surface-2); border-radius: var(--radius-sm); }
  .label { margin: 0; color: var(--text-2); font-size: 12px; font-weight: 600; letter-spacing: 0.04em; text-transform: uppercase; }
  .text { margin: 0; font-size: 14px; overflow-wrap: anywhere; }
  .reasons { display: grid; gap: 4px; }
  .reasons ul { margin: 0; padding-left: 18px; color: var(--text-2); font-size: 13px; }
  .body { display: grid; gap: 12px; min-width: 0; }
  footer { display: flex; flex-wrap: wrap; justify-content: space-between; gap: 8px; padding-top: 8px; border-top: 1px solid var(--line-soft); }
</style>
