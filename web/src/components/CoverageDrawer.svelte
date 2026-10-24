<script lang="ts">
  import { COVERAGE_TEXT } from '../lib/provenance';
  import CoveragePanel from './CoveragePanel.svelte';
  import Drawer from './Drawer.svelte';

  // One source's data coverage, in a drawer of its own so the table has room. The check still starts only from its own button.
  // The page hands focus back to the Source button when it closes (see closeCoverage in lib/appState.svelte.ts).
  let { sourceId, sourceLabel, start, end, onClose }: { sourceId: string; sourceLabel: string; start: string; end: string; onClose: () => void } = $props();
</script>

<Drawer class="coverage-drawer" title={COVERAGE_TEXT.heading} titleId="coverage-title" closeLabel="Close data coverage" {onClose}>
  {#key sourceId}<CoveragePanel {sourceId} {sourceLabel} {start} {end} autofocus />{/key}
</Drawer>

<style>
  :global(.connections-drawer.coverage-drawer) { width: min(720px, calc(100vw - 24px)); }
</style>
