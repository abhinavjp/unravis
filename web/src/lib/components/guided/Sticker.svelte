<script lang="ts">
  import type { StickerTone } from '../../guided/stepModel';
  import { GUIDED_TEXT as T } from '../../copy/guidedCopy';

  // A status sticker: information, not a button. Soft fill, no outline. Waiting and Running are hatched, never green.
  let { word, tone, outOfDate = false }: { word: string; tone: StickerTone; outOfDate?: boolean } = $props();
</script>

<span class={'sticker tone-' + (outOfDate ? 'stale' : tone)}>{#if outOfDate}<span class="visually-hidden">{word}. </span><span class="stale">{T.outOfDate}</span>{:else}{word}{/if}</span>

<style>
  .sticker { display: inline-block; padding: 1px 8px; color: var(--text); font-size: 12px; font-weight: 600; line-height: 1.5; white-space: nowrap; border-radius: var(--radius-sm); background: var(--surface-2); }
  .tone-ready { background: var(--suspect-green-soft); }
  .tone-partly { background: var(--suspect-orange-soft); }
  .tone-cannot { background: var(--suspect-grey-soft); }
  .tone-failed { background: var(--suspect-red-soft); }
  .tone-stale { white-space: normal; background: var(--surface-2); box-shadow: inset 0 0 0 1px var(--line); }
  /* The hatch is a strip at the left edge, so the word sits on a plain fill and stays readable. */
  .tone-waiting, .tone-running { padding-left: 18px; background-color: var(--surface-2); background-image: var(--hatch); background-size: 10px 100%; background-repeat: no-repeat; color: var(--text); }
  /* The visible text of a stale sticker. margin-inline-start stays at 0: the word no longer follows another word. */
  .stale { margin-inline-start: 0; font-weight: 500; color: var(--text-2); }
</style>
