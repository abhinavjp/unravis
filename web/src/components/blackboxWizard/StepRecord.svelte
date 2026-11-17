<script lang="ts">
  import { INTERVALS, settingsProblem } from '../../lib/blackbox';
  import { RECORD_GROUPS } from '../../lib/blackboxCatalogue';
  import {
    applyPreset,
    buildSettings,
    presetOf,
    stepProblem,
    steps
  } from '../../lib/blackboxWizard';
  import type { PresetId, WizardState } from '../../lib/blackboxWizard';
  import { BBW_TEXT } from '../../lib/copy/blackboxWizardCopy';
  import FigureDetails from './FigureDetails.svelte';
  import SegmentedControl from './SegmentedControl.svelte';

  type BasePreset = Exclude<PresetId, 'custom'>;
  type RecordKey = keyof WizardState['settings']['record'];

  const RECORD_KEYS = ['processes', 'logsCatalogue', 'copyLogsOnSnapshot'] as const satisfies readonly RecordKey[];
  const PRESET_OPTIONS: { value: PresetId; label: string }[] = [
    { value: 'recommended', label: BBW_TEXT['record.recommended'] },
    { value: 'light', label: BBW_TEXT['record.light'] },
    { value: 'custom', label: BBW_TEXT['record.custom'] }
  ];
  const INTERVAL_OPTIONS: { value: number; label: string }[] = INTERVALS.map((value) => ({
    value,
    label: value + ' ' + BBW_TEXT['record.seconds']
  }));

  let { wizard = $bindable() }: { wizard: WizardState } = $props();

  const id = $props.id();
  const recordPreset = $derived(presetOf(wizard.settings.record));
  let customPinned = $state(false);
  const preset = $derived(customPinned ? 'custom' : recordPreset);
  const hasSqlStep = $derived(steps(wizard).includes('sql'));
  const logProblem = $derived(
    settingsProblem(buildSettings(wizard)) === 'logs' || stepProblem(wizard, 'record') === 'logs'
  );

  let basedOn = $state<BasePreset>(presetOf(wizard.settings.record) === 'light' ? 'light' : 'recommended');
  const changed = $derived.by(() => {
    const baseRecord = applyPreset(wizard, basedOn).settings.record;
    return RECORD_KEYS.filter((key) => wizard.settings.record[key] !== baseRecord[key]).length;
  });

  let presetChoice = $state<PresetId>('recommended');
  let intervalChoice = $state(wizard.settings.intervalSeconds);

  $effect(() => {
    presetChoice = preset;
  });
  $effect(() => {
    intervalChoice = wizard.settings.intervalSeconds;
  });

  function presetLabel(value: BasePreset): string {
    return value === 'recommended' ? BBW_TEXT['record.recommended'] : BBW_TEXT['record.light'];
  }

  function presetHelp(value: PresetId): string {
    if (value === 'recommended') return BBW_TEXT['record.recommendedHelp'];
    if (value === 'light') return BBW_TEXT['record.lightHelp'];
    return BBW_TEXT['record.customHelp'];
  }

  function choosePreset() {
    if (presetChoice === 'custom') {
      // Custom adds no settings field. It only opens the owner's current mix as a custom view.
      if (recordPreset !== 'custom') basedOn = recordPreset;
      customPinned = true;
      return;
    }
    customPinned = false;
    basedOn = presetChoice;
    wizard = applyPreset(wizard, presetChoice);
  }

  function resetPreset() {
    customPinned = false;
    wizard = applyPreset(wizard, basedOn);
  }

  function chooseInterval() {
    wizard = { ...wizard, settings: { ...wizard.settings, intervalSeconds: intervalChoice } };
  }

  function setRecord(key: RecordKey, checked: boolean) {
    const before = presetOf(wizard.settings.record);
    if (before !== 'custom') basedOn = before;
    customPinned = false;
    wizard = {
      ...wizard,
      settings: {
        ...wizard.settings,
        record: { ...wizard.settings.record, [key]: checked }
      }
    };
  }
</script>

<div class="record-step">
  <section class="choice" aria-label={BBW_TEXT['record.startingPoint']}>
    <div onchange={choosePreset}>
      <SegmentedControl
        legend={BBW_TEXT['record.startingPoint']}
        options={PRESET_OPTIONS}
        bind:value={presetChoice}
      />
    </div>
    <p class="hint">{presetHelp(preset)}</p>
    {#if preset === 'custom'}
      <div class="custom-line">
        <span>{BBW_TEXT['record.customBased'](presetLabel(basedOn), changed)}</span>
        <button type="button" class="reset" onclick={resetPreset}>{BBW_TEXT['record.reset'](presetLabel(basedOn))}</button>
      </div>
    {/if}
  </section>

  <section class="choice" aria-label={BBW_TEXT['record.sampleEvery']}>
    <div onchange={chooseInterval}>
      <SegmentedControl
        legend={BBW_TEXT['record.sampleEvery']}
        options={INTERVAL_OPTIONS}
        bind:value={intervalChoice}
      />
    </div>
    <p class="hint">{BBW_TEXT['record.intervalHelp']}</p>
  </section>

  <section class="groups" aria-labelledby={`${id}-included`}>
    <h3 id={`${id}-included`}>{BBW_TEXT['record.included']}</h3>
    {#each RECORD_GROUPS as group (group.id)}
      <article class="group">
        <div class="group-heading">
          {#if group.toggle}
            <label class="toggle">
              <input
                type="checkbox"
                checked={wizard.settings.record[group.toggle]}
                onchange={(event) => setRecord(group.toggle!, event.currentTarget.checked)}
              />
              <span>{group.title}</span>
            </label>
          {:else}
            <h4>{group.title}</h4>
            <span class="status">{group.always ? BBW_TEXT['record.alwaysOn'] : BBW_TEXT['record.whenPresent']}</span>
          {/if}
        </div>

        {#if group.sqlHost && hasSqlStep}
          <p class="hint sql-note">{BBW_TEXT['record.sqlStepNote']}</p>
        {/if}

        {#if group.id === 'logsCatalogue' && wizard.settings.record.logsCatalogue}
          <div class="log-folders">
            <label for={`${id}-log-folders`}>{BBW_TEXT['record.logFolders']}</label>
            <textarea
              id={`${id}-log-folders`}
              rows="4"
              spellcheck="false"
              bind:value={wizard.logPathsText}
              aria-invalid={logProblem}
              aria-describedby={`${id}-log-folders-help${logProblem ? ` ${id}-log-folders-error` : ''}`}
            ></textarea>
            <p class="hint" id={`${id}-log-folders-help`}>{BBW_TEXT['record.logFoldersHelp']}</p>
            {#if logProblem}
              <p class="error" id={`${id}-log-folders-error`} role="alert">{BBW_TEXT['record.logFoldersError']}</p>
            {/if}
          </div>
        {/if}

        <details class="group-figures">
          <summary>{BBW_TEXT['record.groupFigures']}<span class="visually-hidden"> for {group.title}</span></summary>
          <div class="figure-list">
            {#each group.figures as figure (figure.id)}
              <section class="group-figure">
                <h5>{figure.label}</h5>
                <FigureDetails details={figure.details} label={figure.label} />
              </section>
            {/each}
          </div>
        </details>
      </article>
    {/each}
  </section>
</div>

<style>
  .record-step { display: grid; gap: var(--space-6); }
  .choice { min-width: 0; }
  .hint { margin: var(--space-2) 0 0; color: var(--text-2); }
  .custom-line { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); margin-top: var(--space-2); color: var(--text-2); }
  .reset { min-width: 40px; min-height: 44px; padding: var(--space-2) var(--space-3); border: 1px solid var(--control-line); border-radius: var(--radius-sm); background: var(--surface); color: var(--accent); cursor: pointer; }
  .reset:hover { background: var(--surface-2); }
  .reset:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .groups { display: grid; gap: var(--space-3); min-width: 0; }
  .groups > h3 { margin: 0; font-size: var(--fs-5); }
  .group { min-width: 0; padding: var(--space-2) var(--space-3); border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface); }
  .group-heading { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--space-2); min-height: 44px; }
  .group-heading h4 { margin: 0; font-size: var(--fs-4); }
  .toggle { display: flex; align-items: center; gap: var(--space-2); min-width: 40px; min-height: 44px; font-weight: 600; cursor: pointer; }
  .toggle input { flex: 0 0 auto; width: 18px; height: 18px; margin: 0; accent-color: var(--accent); }
  .status { color: var(--text-2); font-size: var(--fs-3); font-weight: 400; }
  .sql-note { margin-bottom: var(--space-2); }
  .log-folders { display: grid; gap: var(--space-2); margin: var(--space-2) 0 var(--space-3); }
  .log-folders > label { font-weight: 600; }
  .log-folders .hint, .log-folders .error { margin: 0; }
  textarea { box-sizing: border-box; width: 100%; min-height: 96px; padding: var(--space-2) var(--space-3); resize: vertical; border: 1px solid var(--control-line); border-radius: var(--radius-sm); background: var(--bg); color: var(--text); font-family: var(--mono); font-size: var(--fs-4); line-height: 1.45; }
  textarea:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  textarea[aria-invalid='true'] { border-color: var(--bad); }
  .error { color: var(--bad); }
  .group-figures { margin-top: var(--space-1); border-top: 1px solid var(--line-soft); }
  .group-figures > summary { display: flex; align-items: center; width: fit-content; min-width: 40px; min-height: 44px; color: var(--accent); cursor: pointer; text-decoration: underline; text-underline-offset: 3px; }
  .group-figures > summary::before { content: '▸'; margin-right: var(--space-2); text-decoration: none; }
  .group-figures[open] > summary::before { content: '▾'; }
  .group-figures > summary:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius-sm); }
  .figure-list { display: grid; gap: var(--space-2); padding-bottom: var(--space-2); }
  .group-figure { padding-top: var(--space-2); border-top: 1px solid var(--line-soft); }
  .group-figure h5 { margin: 0; font-size: var(--fs-4); }
  .visually-hidden { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; border: 0; }
  @media (max-width: 640px) {
    .group-heading { align-items: flex-start; }
    .status { width: 100%; }
  }
</style>
