<script lang="ts">
  import BlackboxTargetPicker from '../BlackboxTargetPicker.svelte';
  import RadioCards from './RadioCards.svelte';
  import { BBW_TEXT as W } from '../../lib/copy/blackboxWizardCopy';
  import type { SettingsProblem } from '../../lib/blackbox';
  import type { WatchMode, WizardState } from '../../lib/blackboxWizard';

  // The Watch step of the Place pop-up (S-09, S-10, BL-135). It owns two answers and nothing else:
  // wizard.watchMode ('none' | 'some') and wizard.settings.targets (kind + name, at most 32). The target list
  // itself is the shared BlackboxTargetPicker, so Apply settings and this step follow the same rules.
  // `problem` is the field the last Next found wrong; only 'targets' is read, and the picker shows it.
  let { wizard = $bindable(), problem = null }: { wizard: WizardState; problem?: SettingsProblem | null } = $props();

  // This PC lists what it can read. A pack for another server cannot, so names are typed (S-10).
  const mode = $derived(wizard.where === 'pack' ? 'pack' : 'pc');
  const count = $derived(wizard.settings.targets.length);

  // The picker reads the list from this PC once, when it first appears. It is created the first time "some" is
  // chosen and then only hidden, so switching back and forth never reads the server again and never loses a pick.
  let pickerOpened = $state(wizard.watchMode === 'some');
  $effect(() => { if (wizard.watchMode === 'some') pickerOpened = true; });

  const options: { value: WatchMode; title: string; description: string }[] = [
    { value: 'none', title: W['watch.noneTitle'], description: W['watch.noneText'] },
    { value: 'some', title: W['watch.someTitle'], description: W['watch.someText'] }
  ];
</script>

<div class="step">
  <p class="note">{W['step.watch.help']}</p>

  <RadioCards legend={W['watch.legend']} {options} bind:value={wizard.watchMode} />

  {#if wizard.watchMode === 'none' && count > 0}
    <!-- Picked names are hidden, not lost; buildSettings sends [] while this choice stays. -->
    <p class="note" role="status">{W['watch.keptNote'](count)}</p>
  {/if}

  {#if pickerOpened}
    <div class="picker" hidden={wizard.watchMode !== 'some'}>
      <BlackboxTargetPicker bind:targets={wizard.settings.targets} {mode} idPrefix="bbw" {problem} />

      <p class="count" role="status">{count === 0 ? W['watch.noneChosen'] : W['watch.watching'](count)}</p>

      <ul class="honest">
        {#if mode === 'pc'}
          <li>{W['watch.appPoolState']}</li>
          <li>{W['watch.serviceState']}</li>
          <li>{W['watch.pcHelp']}</li>
        {:else}
          <li>{W['watch.packHelp']}</li>
        {/if}
        <li>{W['watch.notRunning']}</li>
        <li>{W['watch.unavailable']}</li>
        <li>{W['watch.ambiguous']}</li>
      </ul>
    </div>
  {/if}
</div>

<style>
  .step { display: grid; gap: var(--space-3); min-width: 0; }
  .note { margin: 0; color: var(--text-2); font-size: 13px; line-height: 1.5; overflow-wrap: anywhere; }
  .picker { display: grid; gap: var(--space-3); min-width: 0; padding-top: var(--space-3); border-top: 1px solid var(--line); }
  .picker[hidden] { display: none; }
  .count { margin: 0; font-weight: 600; color: var(--text); }
  .honest { margin: 0; padding-left: var(--space-4); display: grid; gap: var(--space-1); color: var(--text-2); font-size: 12px; line-height: 1.5; overflow-wrap: anywhere; }
</style>
