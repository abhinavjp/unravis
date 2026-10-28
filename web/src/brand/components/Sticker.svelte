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
  .u-sticker {
    --tone: var(--text-2);
    display: inline-block;
    position: relative;
    overflow: hidden;
    padding: 1px 8px;
    border-radius: var(--radius-sm);
    font-size: 12px;
    font-weight: 600;
    line-height: 1.5;
    white-space: nowrap;
    color: var(--text);
    background: var(--surface-2);
  }
  .ok { --tone: var(--tone-ok); }
  .warn { --tone: var(--warn); }
  .bad { --tone: var(--bad); }
  .running { --tone: var(--accent); }
  .ok, .warn, .bad, .running { color: var(--tone); background: color-mix(in srgb, var(--tone) 14%, var(--surface)); }
  .pending { color: var(--text-2); background: color-mix(in srgb, var(--pending) 14%, var(--surface)); padding-left: 12px; }
  .pending::before { content: ''; position: absolute; inset: 0 auto 0 0; width: 5px; background: var(--hatch); }
</style>
