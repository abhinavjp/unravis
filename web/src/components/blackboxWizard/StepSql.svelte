<script lang="ts">
  import FigureRow from './FigureRow.svelte';
  import SegmentedControl from './SegmentedControl.svelte';
  import { RECORD_GROUPS } from '../../lib/blackboxCatalogue';
  import { SQL_PROFILES, type SqlProfile } from '../../lib/blackboxSql';
  import { setSqlProfile, setSqlText, type WizardState } from '../../lib/blackboxWizard';
  import { BBW_TEXT } from '../../lib/copy/blackboxWizardCopy';

  let { wizard = $bindable() }: { wizard: WizardState } = $props();

  const sqlGroups = RECORD_GROUPS.filter((group) => group.sqlHost);
  const total = sqlGroups.reduce((n, group) => n + group.figures.length, 0);
  const options = SQL_PROFILES.map((value) => ({ value, label: BBW_TEXT[('sql.profile' + value[0].toUpperCase() + value.slice(1)) as 'sql.profileStandard'] }));
  const deep = $derived(wizard.sqlChoice.profile === 'deep');

  function pick(value: SqlProfile | null): void {
    if (value) wizard = setSqlProfile(wizard, value);
  }
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

  <SegmentedControl legend={BBW_TEXT['sql.startHeading']} {options} bind:value={() => wizard.sqlChoice.profile, pick} />
  <p class="hint">{BBW_TEXT['sql.startHint']}</p>
  {#if deep}
    <p class="hint">{BBW_TEXT['sql.deepNote']}</p>
    <label class="tick">
      <input type="checkbox" checked={wizard.sqlChoice.text} onchange={(e) => { wizard = setSqlText(wizard, e.currentTarget.checked); }} />
      <span><b>{BBW_TEXT['sql.textTick']}</b><span class="warn">{BBW_TEXT['sql.textWarn']}</span></span>
    </label>
  {:else}
    <p class="hint">{BBW_TEXT['sql.textDeepOnly']}</p>
  {/if}

  <div class="notes">
    <p>{BBW_TEXT['sql.singleCounters']}</p>
    <p>{BBW_TEXT['sql.deeperChecks']}</p>
  </div>
</div>

<style>
  .sql-step { display: grid; gap: var(--space-3); min-width: 0; }
  p { margin: 0; color: var(--text-2); }
  p.all { color: var(--text); font-weight: 500; }
  .hint { font-size: var(--fs-3); }
  .stats { border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface); }
  .notes { display: grid; gap: var(--space-1); }
  .tick { display: grid; grid-template-columns: 20px 1fr; gap: var(--space-3); align-items: start; }
  .tick span { display: grid; gap: 2px; }
  .warn { color: var(--text-2); font-size: var(--fs-3); }
</style>
