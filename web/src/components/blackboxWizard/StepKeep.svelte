<script lang="ts">
  import SegmentedControl from './SegmentedControl.svelte';
  import { stepProblem, type WizardState } from '../../lib/blackboxWizard';
  import { BBW_TEXT } from '../../lib/copy/blackboxWizardCopy';

  let { wizard = $bindable() }: { wizard: WizardState } = $props();

  const STANDARD_DAYS = [7, 30, 90] as const;
  type StandardDay = (typeof STANDARD_DAYS)[number];
  type DayChoice = StandardDay | 'other';

  const isStandardDay = (days: number): days is StandardDay =>
    (STANDARD_DAYS as readonly number[]).includes(days);

  const dayOptions: { value: DayChoice; label: string }[] = [
    ...STANDARD_DAYS.map((value) => ({ value, label: `${value} ${BBW_TEXT['keep.daysUnit']}` })),
    { value: 'other', label: BBW_TEXT['keep.other'] }
  ];

  const idPrefix = $props.id();
  let dayChoice = $state<DayChoice>(isStandardDay(wizard.settings.keepDays) ? wizard.settings.keepDays : 'other');
  const problem = $derived(stepProblem(wizard, 'keep'));

  function dayChoiceChanged() {
    if (dayChoice !== 'other') wizard.settings.keepDays = dayChoice;
  }
</script>

<div class="keep-fields">
  <div class="field">
    <div onchange={dayChoiceChanged}>
      <SegmentedControl legend={BBW_TEXT['keep.days']} options={dayOptions} bind:value={dayChoice} />
    </div>
    <p class="help" id="{idPrefix}-days-help">{BBW_TEXT['keep.daysHelp']}</p>
    {#if dayChoice === 'other'}
      <div class="custom-days">
        <label for="{idPrefix}-days">{BBW_TEXT['keep.other']}</label>
        <div class="number-line">
          <input
            id="{idPrefix}-days"
            data-autofocus
            type="number"
            min="1"
            max="365"
            step="1"
            bind:value={wizard.settings.keepDays}
            aria-invalid={problem === 'keep' ? 'true' : undefined}
            aria-describedby="{idPrefix}-days-help{problem === 'keep' ? ` ${idPrefix}-days-error` : ''}"
          />
          <span class="unit">{BBW_TEXT['keep.daysUnit']}</span>
        </div>
      </div>
    {/if}
    {#if problem === 'keep'}
      <p class="error" id="{idPrefix}-days-error" role="alert">{BBW_TEXT['keep.daysError']}</p>
    {/if}
  </div>

  <div class="field">
    <label for="{idPrefix}-size">{BBW_TEXT['keep.size']}</label>
    <div class="number-line">
      <input
        id="{idPrefix}-size"
        type="number"
        min="0.1"
        max="10000"
        step="0.1"
        bind:value={wizard.settings.maxGB}
        aria-invalid={problem === 'size' ? 'true' : undefined}
        aria-describedby="{idPrefix}-size-help{problem === 'size' ? ` ${idPrefix}-size-error` : ''}"
      />
      <span class="unit">{BBW_TEXT['keep.sizeUnit']}</span>
    </div>
    <p class="help" id="{idPrefix}-size-help">{BBW_TEXT['keep.sizeHelp']}</p>
    {#if problem === 'size'}
      <p class="error" id="{idPrefix}-size-error" role="alert">{BBW_TEXT['keep.sizeError']}</p>
    {/if}
  </div>

  <div class="field">
    <label for="{idPrefix}-free">{BBW_TEXT['keep.free']}</label>
    <div class="number-line">
      <input
        id="{idPrefix}-free"
        type="number"
        min="1"
        max="50"
        step="1"
        bind:value={wizard.settings.minFreePercent}
        aria-invalid={problem === 'free' ? 'true' : undefined}
        aria-describedby="{idPrefix}-free-help{problem === 'free' ? ` ${idPrefix}-free-error` : ''}"
      />
      <span class="unit">{BBW_TEXT['keep.freeUnit']}</span>
    </div>
    <p class="help" id="{idPrefix}-free-help">{BBW_TEXT['keep.freeHelp']}</p>
    {#if problem === 'free'}
      <p class="error" id="{idPrefix}-free-error" role="alert">{BBW_TEXT['keep.freeError']}</p>
    {/if}
  </div>
</div>

<style>
  .keep-fields { display: grid; gap: var(--space-6); min-width: 0; }
  .field { display: grid; justify-items: start; gap: var(--space-2); min-width: 0; }
  .field > label, .custom-days > label { color: var(--text); font-weight: 600; }
  .custom-days { display: grid; gap: var(--space-2); margin-top: var(--space-2); }
  .number-line { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); max-width: 100%; }
  input { box-sizing: border-box; width: 10rem; max-width: 100%; min-width: 0; min-height: 44px; padding: var(--space-2) var(--space-3); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); background: var(--surface); color: var(--text); font: inherit; }
  input:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  input[aria-invalid='true'] { border-color: var(--bad); }
  .unit { color: var(--text); }
  .help, .error { max-width: 60ch; margin: 0; font-size: var(--fs-3); overflow-wrap: anywhere; }
  .help { color: var(--text-2); }
  .error { color: var(--bad); }
</style>
