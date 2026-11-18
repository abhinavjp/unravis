<script lang="ts">
  import type { Figure } from '../../lib/blackboxCatalogue';
  import FigureDetails from './FigureDetails.svelte';

  let {
    figure,
    checked
  }: {
    figure: Figure;
    checked: boolean;
  } = $props();
</script>

<div class="stat">
  <label class="sl">
    <input type="checkbox" checked={checked} disabled />
    <span class="figure-copy">
      <span class="figure-title">{figure.label}</span>
      <span class="hint">{figure.hint}</span>
    </span>
  </label>
  <FigureDetails details={figure.details} label={figure.label} />
</div>

<style>
  .stat { padding: var(--space-2) var(--space-3); }
  .stat:hover { background: var(--surface-2); }
  .sl { display: grid; grid-template-columns: 20px 1fr; gap: var(--space-3); align-items: start; min-height: 36px; cursor: pointer; }
  input { width: 18px; height: 18px; margin: 2px 0 0; accent-color: var(--accent); }
  input:disabled + span { opacity: .75; }
  .figure-copy { min-width: 0; }
  .figure-title { display: block; font-size: var(--fs-4); overflow-wrap: anywhere; }
  .hint { display: block; margin: 0; color: var(--text-2); font-size: var(--fs-3); }
</style>
