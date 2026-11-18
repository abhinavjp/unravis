<script lang="ts">
  import { INTERVALS, settingsProblem } from '../../lib/blackbox';
  import { RECORD_GROUPS } from '../../lib/blackboxCatalogue';
  import type { RecordGroup } from '../../lib/blackboxCatalogue';
  import {
    applyPreset,
    buildSettings,
    presetOf,
    stepProblem,
    steps
  } from '../../lib/blackboxWizard';
  import type { PresetId, WizardState } from '../../lib/blackboxWizard';
  import { BBW_TEXT } from '../../lib/copy/blackboxWizardCopy';
  import FigureRow from './FigureRow.svelte';
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

  // The wireframe shows seven plain rows, so the catalogue's smaller groups are shown together here. Only the three
  // optional lists have a real tick; every other row is recorded on its own and its checkbox is shown ticked and locked.
  type Row = { id: string; title: string; desc: string; tag?: string; toggle?: RecordKey; groups: RecordGroup[]; sql?: true };
  const find = (id: string): RecordGroup => RECORD_GROUPS.find((g) => g.id === id) as RecordGroup;
  const some = (...ids: string[]): RecordGroup[] => ids.map(find).filter(Boolean);
  const ROWS: Row[] = [
    { id: 'machine', title: find('machine').title, desc: BBW_TEXT['record.desc.machine'], tag: BBW_TEXT['record.tag.alwaysOn'], groups: some('machine') },
    { id: 'web', title: BBW_TEXT['record.title.web'], desc: BBW_TEXT['record.desc.web'], tag: BBW_TEXT['record.tag.whenPresent'], groups: some('http', 'iis', 'aspnet', 'clr') },
    { id: 'processes', title: find('processes').title, desc: BBW_TEXT['record.desc.processes'], toggle: 'processes', groups: some('processes') },
    { id: 'watched', title: BBW_TEXT['record.title.watched'], desc: BBW_TEXT['record.desc.watched'], tag: BBW_TEXT['record.tag.whenPresent'], groups: some('proc10', 'procIO') },
    { id: 'events', title: find('events').title, desc: BBW_TEXT['record.desc.events'], tag: BBW_TEXT['record.tag.whenPresent'], groups: some('events') },
    { id: 'logsCatalogue', title: find('logsCatalogue').title, desc: BBW_TEXT['record.desc.logsCatalogue'], toggle: 'logsCatalogue', groups: some('logsCatalogue') },
    { id: 'copyLogsOnSnapshot', title: find('copyLogsOnSnapshot').title, desc: BBW_TEXT['record.desc.copyLogsOnSnapshot'], toggle: 'copyLogsOnSnapshot', groups: some('copyLogsOnSnapshot') },
    { id: 'dumps', title: find('dumps').title, desc: BBW_TEXT['record.desc.dumps'], tag: BBW_TEXT['record.tag.whenPresent'], groups: some('dumps') },
    { id: 'sqlHost', title: find('sqlHost').title, desc: BBW_TEXT['record.desc.sqlHost'], tag: BBW_TEXT['record.tag.extraStep'], sql: true, groups: some('sqlHost') }
  ];
  const figureTotal = (row: Row): number => row.groups.reduce((n, g) => n + g.figures.length, 0);
  const figureOn = (row: Row): number => (row.toggle && !wizard.settings.record[row.toggle] ? 0 : figureTotal(row));
  const allFigures = ROWS.reduce((n, row) => n + figureTotal(row), 0);
  const recordedFigures = $derived(ROWS.reduce((n, row) => n + figureOn(row), 0));

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
    <div class="based" class:is-custom={preset === 'custom'}>
      <span class="dotc" aria-hidden="true"></span>
      {#if preset === 'custom'}
        <span>{BBW_TEXT['record.customBased'](presetLabel(basedOn), changed)} {BBW_TEXT['record.customFigures'](recordedFigures, allFigures)}</span>
        <button type="button" class="reset" onclick={resetPreset}>{BBW_TEXT['record.reset'](presetLabel(basedOn))}</button>
      {:else}
        <span>{BBW_TEXT['record.setLine'](presetLabel(preset), recordedFigures, allFigures)}</span>
      {/if}
    </div>
  </section>

  <section class="groups" aria-labelledby={`${id}-included`}>
    <h3 id={`${id}-included`}>{BBW_TEXT['record.included']}</h3>
    <div class="list">
      {#each ROWS as row (row.id)}
        <article class="grp">
          <label class="grp-h">
            {#if row.toggle}
              <input
                type="checkbox"
                checked={wizard.settings.record[row.toggle]}
                onchange={(event) => setRecord(row.toggle!, event.currentTarget.checked)}
              />
            {:else}
              <input type="checkbox" checked disabled title={BBW_TEXT['record.tag.whenPresentHelp']} />
            {/if}
            <span class="grp-t">
              <span class="grp-name"><b>{row.title}</b>{#if row.tag}<span class="tag" class:warn={row.sql}>{row.tag}</span>{/if}</span>
              <span class="hint">{row.desc}</span>
            </span>
            <span class="cnt">{BBW_TEXT['record.countOf'](figureOn(row), figureTotal(row))}</span>
          </label>

          {#if row.sql && hasSqlStep}
            <p class="hint sql-note">{BBW_TEXT['record.sqlStepNote']}</p>
          {/if}

          {#if row.id === 'logsCatalogue' && wizard.settings.record.logsCatalogue}
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
            <summary>{BBW_TEXT['record.groupFigures']}<span class="visually-hidden"> for {row.title}</span></summary>
            <div class="figure-list">
              {#each row.groups as group (group.id)}
                {#each group.figures as figure (figure.id)}
                  <FigureRow
                    {figure}
                    checked={!row.toggle || wizard.settings.record[row.toggle]}
                  />
                {/each}
              {/each}
            </div>
          </details>
        </article>
      {/each}
    </div>
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
</div>

<style>
  .record-step { display: grid; gap: var(--space-5); }
  .choice { min-width: 0; }
  .hint { margin: var(--space-2) 0 0; color: var(--text-2); }
  .based { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); min-height: 32px; margin-top: var(--space-2); color: var(--text-2); font-size: var(--fs-3); }
  .dotc { flex: 0 0 auto; width: 8px; height: 8px; border-radius: 50%; background: var(--accent); }
  .based.is-custom .dotc { background: var(--warn); }
  .reset { min-width: 40px; min-height: 32px; padding: 0 var(--space-1); border: 0; background: none; color: var(--accent); font: inherit; text-decoration: underline; text-underline-offset: 3px; cursor: pointer; }
  .reset:hover { color: var(--text); }
  .reset:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .groups { display: grid; gap: var(--space-2); min-width: 0; }
  .groups > h3 { margin: 0; font-size: var(--fs-4); font-weight: 500; }
  /* The wireframe's list: one bordered box, a row per group, a hairline between rows. */
  .list { min-width: 0; border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface); }
  .grp { min-width: 0; }
  .grp + .grp { border-top: 1px solid var(--line); }
  .grp-h { display: grid; grid-template-columns: 20px 1fr auto; gap: var(--space-3); align-items: start; min-height: 48px; padding: var(--space-3) var(--space-4); cursor: pointer; }
  .grp-h:hover { background: var(--surface-2); }
  .grp-h input { width: 18px; height: 18px; margin: 2px 0 0; accent-color: var(--accent); }
  .grp-h input:disabled { cursor: default; }
  .grp-t { display: grid; gap: 0; min-width: 0; }
  .grp-name { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-1); }
  .grp-name b { font-weight: 600; }
  .grp-t .hint { margin: 0; font-size: var(--fs-3); }
  .tag { padding: 1px var(--space-2); border: 1px solid var(--line); border-radius: var(--radius-sm); color: var(--text-2); font-size: var(--fs-2); line-height: 16px; white-space: nowrap; }
  .tag.warn { border-color: var(--warn); color: var(--warn); }
  .cnt { color: var(--text-2); font-size: var(--fs-2); font-variant-numeric: tabular-nums; white-space: nowrap; }
  .sql-note { margin: var(--space-2) var(--space-4); }
  .log-folders { display: grid; gap: var(--space-2); margin: var(--space-2) var(--space-4) var(--space-3); }
  .log-folders > label { font-weight: 600; }
  .log-folders .hint, .log-folders .error { margin: 0; }
  textarea { box-sizing: border-box; width: 100%; min-height: 96px; padding: var(--space-2) var(--space-3); resize: vertical; border: 1px solid var(--control-line); border-radius: var(--radius-sm); background: var(--bg); color: var(--text); font-family: var(--mono); font-size: var(--fs-4); line-height: 1.45; }
  textarea:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  textarea[aria-invalid='true'] { border-color: var(--bad); }
  .error { color: var(--bad); }
  .group-figures { margin: 0; padding: 0 var(--space-4) var(--space-3) calc(var(--space-4) + 32px); }
  .group-figures > summary { display: flex; align-items: center; width: fit-content; min-height: 32px; color: var(--text-2); font-size: var(--fs-3); cursor: pointer; text-decoration: underline; text-underline-offset: 3px; }
  .group-figures > summary:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius-sm); }
  .figure-list { display: grid; gap: 0; }
  .visually-hidden { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; border: 0; }
</style>
