<script lang="ts">
  import { stickerTone, type StateInfo } from '../lib/blackbox';

  // One A36 state as a sticker: a soft fill and the state's own word, no outline, never styled like a button.
  // The word carries the meaning; the colour only helps.
  let { info }: { info: Pick<StateInfo, 'label' | 'tone' | 'state'> } = $props();
  const tone = $derived(stickerTone(info.tone));
</script>

<span class="bb-sticker {tone}" data-state={info.state}>{info.label}</span>

<style>
  .bb-sticker { display: inline-block; padding: 2px 10px; border-radius: 999px; font-size: 12px; font-weight: 600; line-height: 1.5; white-space: nowrap; color: var(--text); background: var(--surface-2); }
  .bb-sticker.good { color: var(--tone-ok); background: color-mix(in srgb, var(--tone-ok) 10%, var(--surface)); }
  .bb-sticker.warn { color: var(--warn); background: color-mix(in srgb, var(--warn) 10%, var(--surface)); }
  .bb-sticker.bad { color: var(--bad); background: color-mix(in srgb, var(--bad) 10%, var(--surface)); }
  .bb-sticker.idle { color: var(--text-2); background: var(--surface-2); }
</style>
