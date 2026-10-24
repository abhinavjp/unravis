<script lang="ts">
  import { checkAllSupplied, type SuppliedSpec } from '../cases';
  import { IMPORT_TEXT, suppliedHint, suppliedLabel, suppliedProblemText, suppliedRangeText, suppliedSummaryNote, suppliedValueLabel } from '../copy/importCopy';

  // "Settings the owner told you": the keys the chosen importer reads, folded away until asked for. A blank box is not sent.
  // This part only holds what was typed and says what is wrong with it. The panel sends the checked values and reads the file again
  // when a box is left (the change event: a select is picked, or a text box loses focus or gets Enter).
  // Typed values stay in this screen's memory. They are never logged and never put in a path.
  let { specs, drafts = $bindable(), disabled = false, onApply }: {
    specs: SuppliedSpec[];
    drafts: Record<string, string>;
    disabled?: boolean;
    onApply: () => void;
  } = $props();

  let open = $state(false);
  // A box shows its problem once it has been left, so typing the first digits is not scolded.
  let touched = $state<Record<string, boolean>>({});

  const checked = $derived(checkAllSupplied(specs, drafts));
  const setCount = $derived(Object.keys(checked.values).length);
  const problemCount = $derived(Object.keys(checked.problems).length);

  function set(key: string, value: string) { drafts[key] = value; }
  function leave(key: string) { touched[key] = true; }
  function apply(key: string) {
    // A bad value is not sent; the panel keeps the file from being kept until it is fixed.
    touched[key] = true;
    onApply();
  }
  const problemOf = (key: string) => (touched[key] ? checked.problems[key] : undefined);
</script>

<details class="supplied" bind:open>
  <summary>{IMPORT_TEXT.suppliedHeading}{suppliedSummaryNote(setCount, problemCount)}</summary>
  <div class="body">
    <p class="note">{IMPORT_TEXT.suppliedIntro}</p>
    {#each specs as spec (spec.key)}
      {@const id = 'supplied-' + spec.key}
      {@const problem = problemOf(spec.key)}
      {@const hint = suppliedHint(spec.key)}
      <div class="field">
        <label for={id}>{suppliedLabel(spec.key)}</label>
        {#if spec.kind === 'enum'}
          <select {id} value={drafts[spec.key] ?? ''} {disabled} aria-describedby={id + '-note'} onchange={(event) => { set(spec.key, event.currentTarget.value); apply(spec.key); }}>
            <option value="">{IMPORT_TEXT.suppliedNotSet}</option>
            {#each spec.values as v (v)}<option value={v}>{suppliedValueLabel(spec.key, v)}</option>{/each}
          </select>
        {:else}
          <input
            {id}
            type="text"
            value={drafts[spec.key] ?? ''}
            {disabled}
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
            inputmode={spec.kind === 'number' ? 'decimal' : 'text'}
            maxlength={spec.kind === 'text' ? spec.maxLen : 26}
            aria-invalid={problem ? 'true' : undefined}
            aria-describedby={id + '-note' + (problem ? ' ' + id + '-error' : '')}
            oninput={(event) => set(spec.key, event.currentTarget.value)}
            onblur={() => leave(spec.key)}
            onchange={() => apply(spec.key)}
          />
        {/if}
        <p class="note" id={id + '-note'}>{[hint, suppliedRangeText(spec)].filter(Boolean).join(' ')}</p>
        {#if problem}<p class="problem" id={id + '-error'} role="alert">{suppliedProblemText(problem, spec)}</p>{/if}
      </div>
    {/each}
  </div>
</details>

<style>
  .supplied { min-width: 0; }
  summary { width: fit-content; max-width: 100%; color: var(--text-2); font-size: var(--fs-3); font-weight: 600; cursor: pointer; overflow-wrap: anywhere; }
  .body { display: grid; gap: var(--space-3); min-width: 0; margin-top: var(--space-2); padding: var(--space-3); background: var(--surface-2); border: 1px solid var(--line-soft); border-radius: var(--radius); }
  .field { display: grid; gap: 4px; min-width: 0; }
  .field label { color: var(--text-2); font-size: var(--fs-2); font-weight: 650; }
  .field input, .field select { width: 100%; }
  .field input[aria-invalid='true'] { border-color: var(--bad); }
  .note { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; overflow-wrap: anywhere; }
  .problem { margin: 0; color: var(--bad); font-size: var(--fs-2); font-weight: 650; line-height: 1.5; overflow-wrap: anywhere; }
</style>
