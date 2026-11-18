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

  // The wireframe's [-] value [+] stepper: the buttons move the same bound number the box does, inside its limits.
  type NumKey = 'keepDays' | 'maxGB' | 'minFreePercent';
  function bump(key: NumKey, delta: number, min: number, max: number) {
    const now = Number(wizard.settings[key]);
    const from = Number.isFinite(now) ? now : min;
    wizard.settings[key] = Math.min(max, Math.max(min, Math.round((from + delta) * 10) / 10));
  }

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
          <span class="num">
            <button type="button" aria-label="Less" onclick={() => bump('keepDays', -1, 1, 365)}>−</button>
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
            <button type="button" aria-label="More" onclick={() => bump('keepDays', 1, 1, 365)}>+</button>
          </span>
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
      <span class="num">
            <button type="button" aria-label="Less" onclick={() => bump('maxGB', -0.5, 0.1, 10000)}>−</button>
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
            <button type="button" aria-label="More" onclick={() => bump('maxGB', 0.5, 0.1, 10000)}>+</button>
          </span>
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
      <span class="num">
            <button type="button" aria-label="Less" onclick={() => bump('minFreePercent', -1, 1, 50)}>−</button>
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
            <button type="button" aria-label="More" onclick={() => bump('minFreePercent', 1, 1, 50)}>+</button>
          </span>
      <span class="unit">{BBW_TEXT['keep.freeUnit']}</span>
    </div>
    <p class="help" id="{idPrefix}-free-help">{BBW_TEXT['keep.freeHelp']}</p>
    {#if problem === 'free'}
      <p class="error" id="{idPrefix}-free-error" role="alert">{BBW_TEXT['keep.freeError']}</p>
    {/if}
  </div>
</div>

<style>
  .keep-fields { display: grid; gap: var(--space-5); min-width: 0; }
  .field { display: grid; justify-items: start; gap: var(--space-2); min-width: 0; }
  .field > label, .custom-days > label { color: var(--text); font-weight: 500; }
  .custom-days { display: grid; gap: var(--space-2); margin-top: var(--space-2); }
  .number-line { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); max-width: 100%; }
  .num { display: inline-flex; align-items: stretch; overflow: hidden; border: 1px solid var(--control-line); border-radius: var(--radius-sm); }
  .num:has(input[aria-invalid='true']) { border-color: var(--bad); }
  .num:has(input:focus-visible) { outline: 2px solid var(--focus); outline-offset: 2px; }
  .num button { width: 44px; min-height: 44px; padding: 0; border: 0; border-radius: 0; background: var(--surface-2); color: var(--text); font-size: 18px; line-height: 1; cursor: pointer; }
  .num button:hover { background: var(--line); }
  .num button:focus-visible { outline: 2px solid var(--focus); outline-offset: -3px; }
  input { box-sizing: border-box; width: 72px; min-width: 0; min-height: 44px; padding: 0 var(--space-2); border: 0; border-left: 1px solid var(--line); border-right: 1px solid var(--line); border-radius: 0; background: var(--surface); color: var(--text); font: inherit; text-align: center; appearance: textfield; -moz-appearance: textfield; }
  input:focus-visible { outline: none; }
  input::-webkit-outer-spin-button, input::-webkit-inner-spin-button { appearance: none; margin: 0; }
  .unit { color: var(--text-2); font-size: 13px; }
  .help, .error { max-width: 60ch; margin: 0; font-size: 13px; overflow-wrap: anywhere; }
  .help { color: var(--text-2); }
  .error { color: var(--bad); }
</style>
