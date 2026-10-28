<script lang="ts">
  // The brand mark (three bars in a 26 px square) and the wordmark beside it.
  let { wordmark = 'Unravis' }: { wordmark?: string } = $props();
</script>

<span class="u-brand"><span class="u-mark" aria-hidden="true"><span></span><span></span><span></span></span><span class="u-word">{wordmark}</span></span>

<style>
  .u-brand { display: inline-flex; align-items: center; gap: 10px; }
  .u-mark { box-sizing: border-box; width: 26px; height: 26px; padding: 5px; display: flex; align-items: end; gap: 2px; border-radius: var(--radius-sm); background: var(--surface-2); border: 1px solid var(--line); }
  .u-mark span { display: block; flex: 1; border-radius: 1px; background: var(--accent); }
  .u-mark span:nth-child(1) { height: 7px; opacity: .55; }
  .u-mark span:nth-child(2) { height: 14px; }
  .u-mark span:nth-child(3) { height: 10px; opacity: .8; }
  .u-word { font-size: 16px; font-weight: 600; color: var(--text); }
</style>
