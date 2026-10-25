<script lang="ts">
  import { eli10 } from '../lib/eli10.svelte';
  // The header switch for the whole help layer. It opens every "In simple words" block. Mount it once, in the header.
  // menu: inside the folded More menu it is a menu item, so arrow keys reach it and a screen reader announces it as one.
  let { menu = false }: { menu?: boolean } = $props();
</script>

<label class="eli10-toggle">
  <input type="checkbox" checked={eli10.on} role={menu ? 'menuitemcheckbox' : undefined} onchange={(event) => eli10.set(event.currentTarget.checked)} />
  <span>Explain simply</span>
</label>

<style>
  .eli10-toggle { display: inline-flex; flex-shrink: 0; align-items: center; gap: var(--space-2); min-height: 28px; color: var(--text-2); font-size: var(--fs-2); font-weight: 500; white-space: nowrap; cursor: pointer; }
  .eli10-toggle:hover { color: var(--text); }
  .eli10-toggle input { margin: 0; accent-color: var(--accent); cursor: pointer; }
  .eli10-toggle:has(input:focus-visible) { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius-sm); }
</style>
