<script lang="ts">
  // The Review step of the Place a Blackbox pop-up (TSK-16, contracts C-1 and C-5).
  // Every value shown here is read from buildSettings(wizard): the same payload that is validated and sent. Nothing is
  // read from a step's own copy, so Review cannot show one thing and send another. Everything is read-only; a Change
  // link goes to the step that owns the answer (changeStep) and Next there comes straight back to Review.
  import { listensOnAll, packFileName, targetNamesByKind } from '../../lib/blackbox';
  import { RECORD_GROUPS } from '../../lib/blackboxCatalogue';
  import { BBW_TEXT as W } from '../../lib/copy/blackboxWizardCopy';
  import { buildSettings, changeStep, driveOfPath, presetOf, steps } from '../../lib/blackboxWizard';
  import type { StepId, WizardState } from '../../lib/blackboxWizard';

  let {
    wizard = $bindable(),
    disabled = false,
    onChange
  }: {
    /** The one wizard state. A Change link replaces it with changeStep(wizard, id). */
    wizard: WizardState;
    /** True while the install or pack is running: the Change links are switched off. */
    disabled?: boolean;
    /** Called after a Change link moved the wizard, so the parent can focus the new step's heading. */
    onChange?: (id: StepId) => void;
  } = $props();

  const payload = $derived(buildSettings(wizard));
  const pc = $derived(wizard.where === 'pc');
  const showSql = $derived(steps(wizard).includes('sql'));
  const preset = $derived(presetOf(payload.record));
  const presetWords = $derived(preset === 'recommended' ? W['record.recommended'] : preset === 'light' ? W['record.light'] : W['record.custom']);
  const allFigures = RECORD_GROUPS.reduce((count, group) => count + group.figures.length, 0);
  const recordedFigures = $derived(RECORD_GROUPS.reduce(
    (count, group) => count + (group.toggle && !payload.record[group.toggle] ? 0 : group.figures.length), 0
  ));
  const groups = $derived(targetNamesByKind(payload.targets));
  const watchWords = $derived(payload.targets.length === 0 ? W['review.watchNone'] : W['review.watchText'](groups.appPools, groups.services));
  const fileName = $derived(packFileName(payload.name));
  const systemDrive = $derived((wizard.systemDrive ?? 'C').toUpperCase());
  const keptOnWindowsDrive = $derived(pc && wizard.confirmSystemDrive && driveOfPath(payload.folder) === systemDrive);
  const runsWords = $derived(wizard.where === 'pc' ? W['review.runsPc'] : wizard.where === 'pack' ? W['review.runsPack'] : '');

  // The three optional lists, named the way the Record step names them.
  function toggleTitle(key: 'processes' | 'logsCatalogue' | 'copyLogsOnSnapshot'): string {
    return RECORD_GROUPS.find((g) => g.toggle === key)?.title ?? key;
  }
  // The wireframe's single "Includes" row: only what is on, in one line.
  const included = $derived(
    (['processes', 'logsCatalogue', 'copyLogsOnSnapshot'] as const)
      .filter((key) => payload.record[key])
      .map((key) => toggleTitle(key))
  );

  function change(id: StepId): void {
    if (disabled) return;
    wizard = changeStep(wizard, id);
    onChange?.(id);
  }
</script>

{#snippet changeLink(id: StepId, what: string)}
  <button type="button" class="change" {disabled} onclick={() => change(id)}>
    {W['review.change']}<span class="visually-hidden"> {what}</span>
  </button>
{/snippet}

<div class="cards">
  <section class="card" aria-labelledby="rv-basics">
    <header><h3 id="rv-basics">{W['review.basics']}</h3>{@render changeLink('name', W['rail.name'])}</header>
    <dl>
      <dt>{W['review.name']}</dt>
      <dd>{payload.name}</dd>
      <dt>{W['review.runs']}</dt>
      <dd class="with-change"><span class="value">{runsWords}</span>{@render changeLink('where', W['rail.where'])}</dd>
    </dl>
  </section>

  <section class="card" aria-labelledby="rv-record">
    <header><h3 id="rv-record">{W['review.record']}</h3>{@render changeLink('record', W['review.record'])}</header>
    <dl>
      <dt>{W['review.set']}</dt>
      <dd>{W['review.setText'](presetWords, recordedFigures, allFigures)}</dd>
      <dt>{W['review.includes']}</dt>
      <dd>{included.length > 0 ? included.join(', ') : W['review.includesNone']}</dd>
      {#if payload.record.logsCatalogue}
        <dt>{W['record.logFolders']}</dt>
        <dd>
          {#if payload.logPaths.length > 0}
            <ul class="paths">{#each payload.logPaths as path, i (i)}<li><code>{path}</code></li>{/each}</ul>
          {:else}
            <span class="value">{W['review.off']}</span>
          {/if}
        </dd>
      {/if}
      <dt>{W['review.every']}</dt>
      <dd>{payload.intervalSeconds} {W['review.seconds']}</dd>
    </dl>
  </section>

  <section class="card" aria-labelledby="rv-watch">
    <header><h3 id="rv-watch">{W['review.watch']}</h3>{@render changeLink('watch', W['review.watch'])}</header>
    <dl>
      <dt>{W['review.watching']}</dt>
      <dd>{watchWords}</dd>
    </dl>
  </section>

  {#if showSql}
    <section class="card" aria-labelledby="rv-sql">
      <header><h3 id="rv-sql">{W['review.sql']}</h3>{@render changeLink('sql', W['review.sql'])}</header>
      <dl>
        <dt>{W['review.counters']}</dt>
        <dd>{W['review.sqlAutomatic']}</dd>
        <dt>{W['review.safety']}</dt>
        <dd>{W['sql.deeperChecks']}</dd>
      </dl>
    </section>
  {/if}

  <section class="card" aria-labelledby="rv-keep">
    <header><h3 id="rv-keep">{W['review.keep']}</h3>{@render changeLink('keep', W['review.keep'])}</header>
    <dl>
      <dt>{W['review.keeps']}</dt>
      <dd>{W['review.keepText'](payload.keepDays, payload.maxGB, payload.minFreePercent)}</dd>
    </dl>
  </section>

  <section class="card" aria-labelledby="rv-folder">
    <header><h3 id="rv-folder">{W['review.folder']}</h3>{@render changeLink('folder', W['review.folder'])}</header>
    <dl>
      <dt>{W['review.folderLabel']}</dt>
      <dd>
        <code>{payload.folder}</code>
        {#if keptOnWindowsDrive}<span class="tag">{W['review.windowsDriveConfirmed']}</span>{/if}
      </dd>
      <dt>{W['review.port']}</dt>
      <dd>{payload.port} {W['review.onWord']} {payload.bind}</dd>
    </dl>
  </section>
</div>

<div class="notes">
  {#if listensOnAll(payload.bind)}<p class="note warn">{W['review.bindWarning']}</p>{/if}
  <p class="note">{W['review.readOnly']}</p>
  {#if pc}
    <p class="note">{W['review.windowsAsks']}</p>
  {:else if wizard.where === 'pack'}
    <p class="note">{W['review.packWill'](fileName)}</p>
  {/if}
</div>

<style>
  .cards { display: grid; gap: var(--space-3); min-width: 0; }
  .card { display: grid; gap: var(--space-2); min-width: 0; padding: var(--space-4); border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface); }
  header { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); min-width: 0; }
  h3 { margin: 0; font-size: var(--fs-4); font-weight: 600; }
  dl { display: grid; grid-template-columns: 120px minmax(0, 1fr); gap: var(--space-1) var(--space-3); margin: 0; font-size: 13px; }
  dt { color: var(--text-2); }
  dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
  .with-change { display: flex; align-items: baseline; justify-content: space-between; gap: var(--space-3); }
  .value { min-width: 0; overflow-wrap: anywhere; }
  .paths { margin: 0; padding: 0; list-style: none; display: grid; gap: var(--space-1); }
  code { font-family: var(--font-mono, ui-monospace, monospace); font-size: 12.5px; overflow-wrap: anywhere; }
  .with-change .change { min-height: 0; padding: 0 var(--space-1); }
  .tag { display: inline-block; margin-left: var(--space-2); padding: 1px var(--space-2); border: 1px solid var(--warn); border-radius: var(--radius-sm); color: var(--warn); font-size: var(--fs-2); white-space: nowrap; }
  .change { flex: 0 0 auto; display: inline-flex; align-items: center; justify-content: center; min-width: 40px; min-height: 32px; padding: var(--space-2) var(--space-1); border: 0; background: transparent; color: var(--text-2); font: inherit; text-decoration: underline; text-underline-offset: 3px; cursor: pointer; }
  .change:hover:not(:disabled) { color: var(--text); }
  .change:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius-sm); }
  .change:disabled { color: var(--text-2); cursor: not-allowed; text-decoration: none; }
  .notes { display: grid; gap: var(--space-2); }
  .note { margin: 0; color: var(--text-2); font-size: 13px; overflow-wrap: anywhere; }
  .note.warn { color: var(--text); border-left: 3px solid var(--warn); padding-left: var(--space-2); }
  @media (max-width: 480px) {
    dl { grid-template-columns: minmax(0, 1fr); gap: var(--space-1); }
    dd { margin-bottom: var(--space-2); }
  }
</style>
