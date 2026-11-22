<script lang="ts">
  import FigureRow from './FigureRow.svelte';
  import { RECORD_GROUPS } from '../../lib/blackboxCatalogue';
  import { BBW_TEXT } from '../../lib/copy/blackboxWizardCopy';

  const sqlGroups = RECORD_GROUPS.filter((group) => group.sqlHost);
  const total = sqlGroups.reduce((n, group) => n + group.figures.length, 0);
</script>

<div class="sql-step">
  <!-- The header already says these figures are recorded on their own, and that they come from Windows. -->
  <!-- The wireframe's bordered counter list. Every counter is read on its own, so each tick is shown ticked and locked. -->
  <p class="all">{BBW_TEXT['sql.allRecorded'](total)}</p>
  <div class="stats">
    {#each sqlGroups as group (group.id)}
      {#each group.figures as figure (figure.id)}
        <FigureRow {figure} checked />
      {/each}
    {/each}
  </div>

  <div class="notes">
    <p>{BBW_TEXT['sql.singleCounters']}</p>
    <p>{BBW_TEXT['sql.deeperChecks']}</p>
  </div>
</div>

<style>
  .sql-step { display: grid; gap: var(--space-3); min-width: 0; }
  p { margin: 0; color: var(--text-2); }
  p.all { color: var(--text); font-weight: 500; }
  .stats { border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface); }
  .notes { display: grid; gap: var(--space-1); }
</style>
