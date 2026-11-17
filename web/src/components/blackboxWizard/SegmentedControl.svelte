<script lang="ts" generics="T extends string | number">
  let {
    legend,
    options,
    value = $bindable(),
    disabled = false
  }: {
    legend: string;
    options: { value: T; label: string; disabled?: boolean }[];
    value: T | null;
    disabled?: boolean;
  } = $props();

  const name = $props.id();
</script>

<fieldset {disabled}>
  <legend>{legend}</legend>
  <div class="segments">
    {#each options as option (option.value)}
      <label class:chosen={value === option.value} class:unavailable={option.disabled || disabled}>
        <input type="radio" {name} value={option.value} bind:group={value} disabled={option.disabled} />
        <span>{option.label}</span>
      </label>
    {/each}
  </div>
</fieldset>

<style>
  fieldset { min-width: 0; margin: 0; padding: 0; border: 0; }
  legend { margin-bottom: var(--space-2); padding: 0; color: var(--text); font-weight: 600; }
  .segments { display: flex; flex-wrap: wrap; gap: var(--space-1); }
  label { position: relative; display: inline-flex; align-items: center; justify-content: center; min-width: 40px; min-height: 44px; padding: var(--space-2) var(--space-3); border: 1px solid var(--control-line); border-radius: var(--radius-sm); background: var(--surface); color: var(--text); cursor: pointer; }
  label:hover:not(.unavailable) { background: var(--surface-2); }
  label.chosen { border-color: var(--accent); background: var(--surface-2); box-shadow: inset 0 -2px 0 var(--accent); font-weight: 600; }
  label:has(input:focus-visible) { outline: 2px solid var(--focus); outline-offset: 2px; }
  label.unavailable { opacity: .6; cursor: not-allowed; }
  input { position: absolute; width: 1px; height: 1px; margin: 0; opacity: 0; }
  @media (forced-colors: active) {
    label { gap: var(--space-1); }
    input { position: static; width: auto; height: auto; opacity: 1; }
  }
</style>
