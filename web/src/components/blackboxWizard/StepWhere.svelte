<script lang="ts">
  // Step 2 of the Place a Blackbox pop-up (TSK-09): where the recorder goes. Two radio cards, This PC and
  // A pack for another server, with the real gating of B-11: This PC only while this PC can take one and has
  // none yet, the pack only while this build can build one. The component owns no rules: a click goes up
  // through onChange with setWhere already applied, so the step list (the pack path shows the SQL step)
  // changes in the one place that owns the state.
  import RadioCards from './RadioCards.svelte';
  import { setWhere, whereChoices, type Where, type WizardState } from '../../lib/blackboxWizard';
  import type { ThisPc } from '../../lib/blackbox';
  import { BBW_TEXT } from '../../lib/copy/blackboxWizardCopy';

  let {
    wizard,
    thisPc,
    canPack,
    onChange
  }: {
    /** The one wizard state. Read here for the choice; changed only through onChange. */
    wizard: WizardState;
    /** What the this-pc scan said, for the This PC card's gate (B-11). */
    thisPc: ThisPc;
    /** Whether this build can build a pack (B-11). */
    canPack: boolean;
    /** A card was picked, with setWhere already applied to the fresh state. */
    onChange: (next: WizardState) => void;
  } = $props();

  // B-11: This PC only while this PC is available and has no Blackbox yet. An installed one says that first.
  const choices = $derived(whereChoices(thisPc, canPack));
  const pcOk = $derived(choices.pc);
  // "Build a pack instead" is only honest while a pack can be built.
  const pcReason = $derived(
    thisPc.installed
      ? BBW_TEXT['where.pcAlready']
      : canPack
        ? BBW_TEXT['where.pcUnavailable'] + ' ' + BBW_TEXT['where.buildPackInstead']
        : BBW_TEXT['where.pcUnavailable']
  );

  // RadioCards binds its choice here; the wizard state stays the one truth. Two small effects hold both sides
  // together: the cards follow the state (a Change link from Review can come back here), and a click is
  // reported up at once. The null guard keeps a fresh, unanswered step from reporting anything.
  let choice = $state<Where | null>(null);
  $effect(() => { choice = wizard.where; });
  $effect(() => {
    if (choice !== null && choice !== wizard.where) onChange(setWhere(wizard, choice));
  });

  const options = $derived([
    { value: 'pc' as Where, title: BBW_TEXT['where.pcTitle'], description: BBW_TEXT['where.pcText'], disabled: !pcOk, disabledReason: pcOk ? undefined : pcReason },
    { value: 'pack' as Where, title: BBW_TEXT['where.packTitle'], description: BBW_TEXT['where.packText'], disabled: !canPack, disabledReason: canPack ? undefined : BBW_TEXT['where.packUnavailable'] }
  ]);
</script>

<div class="where-step">
  <RadioCards legend={BBW_TEXT['rail.where']} hideLegend {options} bind:value={choice} />
  {#if choices.scanFailed}
    <p class="where-notice" role="status">{BBW_TEXT['where.scanFailed']}</p>
  {/if}
  {#if choices.none}
    <p class="where-notice" role="status">{choices.none === 'helper' ? BBW_TEXT['where.noneHelper'] : BBW_TEXT['where.noneOther']}</p>
  {/if}
  <p class="where-help">{BBW_TEXT['step.where.help']}</p>
</div>

<style>
  .where-step { display: grid; gap: var(--space-5); min-width: 0; }
  .where-notice { margin: 0; padding-left: var(--space-3); border-left: 3px solid var(--warn); color: var(--text); overflow-wrap: anywhere; }
  .where-help { margin: 0; color: var(--text-2); font-size: 13px; }
</style>
