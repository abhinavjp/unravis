<script lang="ts">
  // Step 1 of the Place a Blackbox pop-up (TSK-09): the name. One text input, its hint (the B-06 rule: letters,
  // numbers and dashes, no spaces) and the inline problem line. The component owns no rules and no state of its
  // own: the problem comes from stepProblem in blackboxWizard.ts, and every keystroke goes up through onChange,
  // so the wizard stays the one place that holds the WizardState.
  import { NAME_MAX } from '../../lib/blackbox';
  import { stepProblem, type WizardState } from '../../lib/blackboxWizard';
  import { BBW_TEXT } from '../../lib/copy/blackboxWizardCopy';

  const problemWords: Record<string, string> = {
    'name-empty': BBW_TEXT['name.empty'],
    'name-long': BBW_TEXT['name.long'],
    'name-chars': BBW_TEXT['name.chars']
  };

  let {
    wizard,
    onChange,
    showProblem = false
  }: {
    /** The one wizard state. Read here for the name and its problem; changed only through onChange. */
    wizard: WizardState;
    /** Every keystroke, with the typed name already written into a fresh state. */
    onChange: (next: WizardState) => void;
    /** True after the wizard refused a Next on this step, so the problem shows even before the owner has typed. */
    showProblem?: boolean;
  } = $props();

  const uid = $props.id();

  // The problem line waits until the owner has typed something, or until the wizard refused a Next,
  // so a fresh step never opens with a complaint about a box that was never touched.
  let touched = $state(false);
  const problem = $derived(stepProblem(wizard, 'name'));
  // A space is the commonest slip, so it gets its own words: say what to do instead of listing the rules again.
  const hasSpace = $derived(/\s/.test(wizard.settings.name));
  const words = $derived(
    problem === null ? null : problem === 'name-chars' && hasSpace ? BBW_TEXT['name.space'] : problemWords[problem] ?? null
  );
  const show = $derived(problem !== null && (touched || showProblem));
  // The box stops at the limit, so the owner is told why typing stops instead of left guessing.
  const atLimit = $derived(wizard.settings.name.length >= NAME_MAX);

  function type(event: Event & { currentTarget: HTMLInputElement }): void {
    touched = true;
    onChange({ ...wizard, settings: { ...wizard.settings, name: event.currentTarget.value } });
  }
</script>

<!-- The label reuses the review row word, the only plain "Name" in the wizard copy. -->
<div class="name-step">
  <label class="name-label" for={uid}>{BBW_TEXT['review.name']}</label>
  <input
    id={uid}
    class="name-input"
    type="text"
    data-autofocus
    value={wizard.settings.name}
    maxlength={NAME_MAX}
    autocomplete="off"
    spellcheck="false"
    aria-invalid={show ? 'true' : undefined}
    aria-describedby={show ? `${uid}-hint ${uid}-problem` : `${uid}-hint`}
    oninput={type}
  />
  <p class="name-hint" id={`${uid}-hint`}>{BBW_TEXT['step.name.help']}</p>
  {#if show && words}
    <p class="inline-error" id={`${uid}-problem`} role="alert">{words}</p>
  {/if}
  {#if atLimit}
    <p class="name-hint name-limit" role="status">{BBW_TEXT['name.limit']}</p>
  {/if}
</div>

<style>
  .name-step { display: grid; gap: var(--space-2); max-width: 420px; min-width: 0; }
  .name-label { color: var(--text); font-size: var(--fs-3); font-weight: 600; }
  .name-input { box-sizing: border-box; width: 100%; min-height: 44px; padding: var(--space-1) var(--space-2); color: var(--text); font: inherit; background: var(--surface); border: 1px solid var(--control-line); border-radius: var(--radius-sm); }
  .name-input:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .name-input[aria-invalid='true'] { border-color: var(--bad); }
  .name-hint { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
</style>
