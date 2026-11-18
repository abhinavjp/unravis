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
  legend { margin-bottom: var(--space-2); padding: 0; color: var(--text); font-weight: 500; }
  /* The wireframe's segmented control: one joined bar, the chosen option filled with the accent. */
  .segments { display: inline-flex; flex-wrap: nowrap; max-width: 100%; overflow: hidden; border: 1px solid var(--control-line); border-radius: var(--radius-sm); }
  label { position: relative; display: grid; place-items: center; min-width: 40px; min-height: 40px; padding: 0 var(--space-3); border-right: 1px solid var(--line); background: transparent; color: var(--text-2); font-weight: 500; white-space: nowrap; cursor: pointer; transition: background var(--dur) var(--ease), color var(--dur) var(--ease); }
  label:last-child { border-right: 0; }
  label:hover:not(.unavailable):not(.chosen) { background: var(--surface-2); color: var(--text); }
  label.chosen { background: var(--accent); color: var(--accent-ink); }
  label:has(input:focus-visible) { outline: 2px solid var(--focus); outline-offset: -3px; }
  label.unavailable { opacity: .6; cursor: not-allowed; }
  input { position: absolute; width: 1px; height: 1px; margin: 0; opacity: 0; }
  @media (forced-colors: active) {
    label { gap: var(--space-1); }
    input { position: static; width: auto; height: auto; opacity: 1; }
  }
</style>
