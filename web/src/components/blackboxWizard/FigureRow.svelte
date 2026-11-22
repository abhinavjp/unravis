<script lang="ts">
  import type { Figure } from '../../lib/blackboxCatalogue';
  import FigureDetails from './FigureDetails.svelte';
  import { BBW_TEXT as W } from '../../lib/copy/blackboxWizardCopy';

  let {
    figure,
    checked
  }: {
    figure: Figure;
    checked: boolean;
  } = $props();
</script>

<div class="stat">
  <div class="sl">
    <!-- BLACKBOX-12: these figures are recorded on their own and cannot be changed, so they get a mark and not a (greyed) checkbox. -->
    <span class="mark" class:off={!checked} aria-hidden="true">{checked ? '✓' : ''}</span>
    <span class="figure-copy">
      <span class="figure-title">{figure.label}<span class="visually-hidden">, {checked ? W['figure.recorded'] : W['figure.notRecorded']}</span></span>
      <span class="hint">{figure.hint}</span>
    </span>
  </div>
  <FigureDetails details={figure.details} label={figure.label} />
</div>

<style>
  .stat { padding: var(--space-2) var(--space-3); }
  .stat:hover { background: var(--surface-2); }
  .sl { display: grid; grid-template-columns: 20px 1fr; gap: var(--space-3); align-items: start; min-height: 36px; }
  .mark { display: grid; place-items: center; width: 18px; height: 18px; margin: 2px 0 0; color: var(--tone-ok); font-size: var(--fs-4); font-weight: 700; line-height: 1; }
  .mark.off { border: 1px dashed var(--control-line); border-radius: 3px; }
  .figure-copy { min-width: 0; }
  .figure-title { display: block; font-size: var(--fs-4); overflow-wrap: anywhere; }
  .hint { display: block; margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .visually-hidden { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; border: 0; }
</style>
