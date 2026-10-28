<script module lang="ts">
  export type StickerTone = 'ok' | 'warn' | 'bad' | 'pending' | 'running' | 'neutral';
</script>

<script lang="ts">
  // A status label: a soft fill and a word, no outline, never clickable, never shaped like a button. The word carries the meaning;
  // the colour only helps. "pending" (waiting, not finished) is slate with the hatch, never green.
  let { tone = 'neutral', label }: { tone?: StickerTone; label: string } = $props();
</script>

<span class="u-sticker {tone}" data-tone={tone}>{label}</span>

<style>
  /* The inks are the setup tokens: text is at least 4.5:1 on its own fill, and the thin edge (the tone at 75 %) is at least 3:1 against
     the page, so a sticker reads in both themes. The edge is a soft border on a filled tag; a button is an outline with no fill. */
  .u-sticker {
    --tone: var(--setup-neutral);
    display: inline-block;
    position: relative;
    overflow: hidden;
    padding: 0 7px;
    border: 1px solid color-mix(in srgb, var(--tone) 75%, transparent);
    background-clip: padding-box;
    border-radius: var(--radius-sm);
    font-size: 12px;
    font-weight: 600;
    line-height: 1.5;
    white-space: nowrap;
    color: var(--tone);
    background: var(--surface-2);
  }
  .ok { --tone: var(--setup-ok); }
  .warn { --tone: var(--setup-warn); }
  .bad { --tone: var(--setup-bad); }
  .running { --tone: var(--setup-run); }
  .pending { --tone: var(--setup-pending); padding-left: 12px; }
  .ok, .warn, .bad, .running, .pending { background: color-mix(in srgb, var(--tone) 12%, var(--surface)); }
  .pending::before { content: ''; position: absolute; inset: 0 auto 0 0; width: 5px; background: var(--hatch); }
</style>
