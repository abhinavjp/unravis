<script lang="ts" generics="T extends string | number">
  let {
    legend,
    options,
    value = $bindable(),
    disabled = false,
    hideLegend = false
  }: {
    /** Hidden from the eye (still read out) when the step title already says it, as the wireframe does for Where and Watch. */
    hideLegend?: boolean;
    legend: string;
    /** `tags` are the small outlined chips after the title ("simplest", "Windows drive"); `warn` tints one amber. */
    options: { value: T; title: string; description: string; tags?: { text: string; warn?: boolean }[]; disabled?: boolean; disabledReason?: string }[];
    value: T | null;
    disabled?: boolean;
  } = $props();

  const name = $props.id();
</script>

<fieldset {disabled}>
  <legend class:sr={hideLegend}>{legend}</legend>
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
          <strong>{option.title}{#each option.tags ?? [] as tag (tag.text)} <span class="tag" class:warn={tag.warn}>{tag.text}</span>{/each}</strong>
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
  legend { margin-bottom: var(--space-2); padding: 0; color: var(--text); font-weight: 500; }
  legend.sr { position: absolute; width: 1px; height: 1px; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
  /* The wireframe's radio card: one card per row, a whole-row target, the chosen one tinted. */
  .cards { display: grid; gap: var(--space-3); }
  label { display: grid; grid-template-columns: 20px minmax(0, 1fr); gap: var(--space-3); min-width: 40px; min-height: 44px; padding: var(--space-4); border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface); color: var(--text); cursor: pointer; transition: border-color var(--dur) var(--ease), background var(--dur) var(--ease); }
  label:hover:not(.unavailable) { border-color: var(--control-line); }
  label.chosen { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 12%, transparent); }
  label:has(input:focus-visible) { outline: 2px solid var(--focus); outline-offset: 2px; }
  label.unavailable { cursor: not-allowed; }
  label.unavailable input { opacity: .6; }
  input { width: 18px; height: 18px; margin: 2px 0 0; accent-color: var(--accent); }
  .copy { display: grid; gap: var(--space-1); min-width: 0; overflow-wrap: anywhere; }
  .copy strong { font-weight: 600; }
  .copy > span { color: var(--text-2); font-size: var(--fs-3); line-height: 1.3; }
  .reason { font-weight: 500; }
  /* Inline, like the wireframe's .tag: an inline chip's vertical padding and border never lift the 20px title line. */
  .tag { padding: 1px var(--space-2); border: 1px solid var(--line); border-radius: var(--radius-sm); color: var(--text-2); font-size: var(--fs-2); white-space: nowrap; }
  .tag.warn { color: var(--warn); border-color: var(--warn); }
</style>
