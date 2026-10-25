<script lang="ts">
  import { eli10 } from '../lib/eli10.svelte';
  // The header switch for the whole help layer. It opens every "In simple words" block. Mount it once, in the header.
  // menu: inside the folded More menu it is a menu item, so arrow keys reach it and a screen reader announces it as one.
  let { menu = false }: { menu?: boolean } = $props();
</script>

<label class="eli10-toggle" class:in-bar={!menu}>
  <input type="checkbox" checked={eli10.on} role={menu ? 'menuitemcheckbox' : undefined} onchange={(event) => eli10.set(event.currentTarget.checked)} />
  <!-- In the bar below 1320px the words shrink to "Simple"; the full name stays for screen readers. -->
  <span class="full">Explain simply</span>{#if !menu}<span class="short" aria-hidden="true">Simple</span>{:else}<!-- The checkbox carries the state for assistive tech; this is the same state in words and a check mark for everyone else. -->
  <span class="state" class:on={eli10.on} aria-hidden="true">{eli10.on ? '✓ On' : 'Off'}</span>{/if}
</label>

<style>
  .eli10-toggle { display: inline-flex; flex-shrink: 0; align-items: center; gap: var(--space-2); min-height: 28px; color: var(--text-2); font-size: var(--fs-2); font-weight: 500; white-space: nowrap; cursor: pointer; }
  .eli10-toggle:hover { color: var(--text); }
  .eli10-toggle input { margin: 0; accent-color: var(--accent); cursor: pointer; }
  .eli10-toggle:has(input:focus-visible) { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius-sm); }
  .state { margin-inline-start: auto; padding-inline-start: var(--space-3); color: var(--text-2); font-weight: 500; }
  .state.on { color: var(--text); font-weight: 600; }
  .short { display: none; }
  @media (max-width: 1319px) {
    .in-bar .full { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
    .in-bar .short { display: inline; }
  }
</style>
