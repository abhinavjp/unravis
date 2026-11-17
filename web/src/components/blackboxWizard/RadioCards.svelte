<script lang="ts" generics="T extends string | number">
  let {
    legend,
    options,
    value = $bindable(),
    disabled = false
  }: {
    legend: string;
    options: { value: T; title: string; description: string; disabled?: boolean; disabledReason?: string }[];
    value: T | null;
    disabled?: boolean;
  } = $props();

  const name = $props.id();
</script>

<fieldset {disabled}>
  <legend>{legend}</legend>
  <div class="cards">
    {#each options as option, index (option.value)}
      <label class:chosen={value === option.value} class:unavailable={option.disabled || disabled}>
        <input
          type="radio"
          {name}
          value={option.value}
          bind:group={value}
          disabled={option.disabled}
          aria-label={option.title}
          aria-describedby={`${name}-description-${index}${option.disabledReason ? ` ${name}-reason-${index}` : ''}`}
        />
        <span class="copy">
          <strong>{option.title}</strong>
          <span id={`${name}-description-${index}`}>{option.description}</span>
          {#if option.disabledReason}
            <span class="reason" id={`${name}-reason-${index}`}>{option.disabledReason}</span>
          {/if}
        </span>
      </label>
    {/each}
  </div>
</fieldset>

<style>
  fieldset { min-width: 0; margin: 0; padding: 0; border: 0; }
  legend { margin-bottom: var(--space-2); padding: 0; color: var(--text); font-weight: 600; }
  .cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr)); gap: var(--space-2); }
  label { display: flex; align-items: flex-start; gap: var(--space-2); min-width: 40px; min-height: 44px; padding: var(--space-2) var(--space-3); border: 1px solid var(--control-line); border-radius: var(--radius-sm); background: var(--surface); color: var(--text); cursor: pointer; }
  label:hover:not(.unavailable) { background: var(--surface-2); }
  label.chosen { border-color: var(--accent); background: var(--surface-2); }
  label:has(input:focus-visible) { outline: 2px solid var(--focus); outline-offset: 2px; }
  label.unavailable { cursor: not-allowed; }
  label.unavailable input { opacity: .6; }
  input { flex: 0 0 auto; margin: 3px 0 0; accent-color: var(--accent); }
  .copy { display: grid; gap: var(--space-1); min-width: 0; overflow-wrap: anywhere; }
  .copy > span { color: var(--text-2); }
  .reason { font-weight: 500; }
</style>
