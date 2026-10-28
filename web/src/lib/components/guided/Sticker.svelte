<script lang="ts">
  import type { StickerTone } from '../../guided/stepModel';

  // A status sticker: information, not a button. Soft fill, no outline. Waiting and Running are hatched, never green.
  let { word, tone, outOfDate = false }: { word: string; tone: StickerTone; outOfDate?: boolean } = $props();
</script>

<span class={'sticker tone-' + tone}>{word}{#if outOfDate}<span class="stale">· Out of date</span>{/if}</span>

<style>
  .sticker { display: inline-block; padding: 1px 8px; color: var(--text); font-size: 12px; font-weight: 600; line-height: 1.5; white-space: nowrap; border-radius: var(--radius-sm); background: var(--surface-2); }
  .tone-ready { background: var(--suspect-green-soft); }
  .tone-partly { background: var(--suspect-orange-soft); }
  .tone-cannot { background: var(--suspect-grey-soft); }
  .tone-failed { background: var(--suspect-red-soft); }
  /* The hatch is a strip at the left edge, so the word sits on a plain fill and stays readable. */
  .tone-waiting, .tone-running { padding-left: 18px; background-color: var(--surface-2); background-image: var(--hatch); background-size: 10px 100%; background-repeat: no-repeat; color: var(--text); }
  .stale { margin-inline-start: 0.4em; font-weight: 500; color: var(--text-2); }
</style>
