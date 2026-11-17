<script lang="ts">
  import FigureDetails from './FigureDetails.svelte';
  import { RECORD_GROUPS } from '../../lib/blackboxCatalogue';
  import { BBW_TEXT } from '../../lib/copy/blackboxWizardCopy';

  const sqlGroups = RECORD_GROUPS.filter((group) => group.sqlHost);
</script>

<div class="sql-step">
  <!-- The header already says these figures are recorded on their own, and that they come from Windows. -->
  {#each sqlGroups as group (group.id)}
    <section>
      <h3>{group.title}</h3>
      <ul>
        {#each group.figures as figure (figure.id)}
          <li>
            <span class="figure-label">{figure.label}</span>
            <FigureDetails details={figure.details} label={figure.label} />
          </li>
        {/each}
      </ul>
    </section>
  {/each}

  <div class="notes">
    <p>{BBW_TEXT['sql.singleCounters']}</p>
    <p>{BBW_TEXT['sql.deeperChecks']}</p>
  </div>
</div>

<style>
  .sql-step { display: grid; gap: var(--space-3); min-width: 0; }
  p { margin: 0; color: var(--text-2); }
  h3 { margin: 0 0 var(--space-2); color: var(--text); font-size: var(--fs-5); }
  ul { margin: 0; padding: 0; list-style: none; }
  li { padding: var(--space-2) 0; border-top: var(--hairline-width) solid var(--hairline); }
  .figure-label { color: var(--text); font-weight: 500; overflow-wrap: anywhere; }
  .notes { display: grid; gap: var(--space-1); }
</style>
