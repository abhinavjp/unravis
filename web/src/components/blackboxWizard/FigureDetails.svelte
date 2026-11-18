<script lang="ts">
  let {
    details,
    label = ''
  }: {
    details: { what: string; why: string; watch: string; stored: string };
    label?: string;
  } = $props();
</script>

<details>
  <summary>Details{#if label}<span class="visually-hidden"> for {label}</span>{/if}</summary>
  <dl>
    <dt>What it is</dt>
    <dd>{details.what}</dd>
    <dt>Why it matters</dt>
    <dd>{details.why}</dd>
    <dt>What to look for</dt>
    <dd>{details.watch}</dd>
    <dt>What is stored</dt>
    <dd>{details.stored}</dd>
  </dl>
</details>

<style>
  details { margin: 0 0 0 32px; }
  summary { display: flex; align-items: center; width: fit-content; min-height: 28px; color: var(--text-2); font-size: 12.5px; cursor: pointer; text-decoration: underline; text-underline-offset: 3px; }
  summary:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius-sm); }
  dl { display: grid; grid-template-columns: 110px 1fr; gap: var(--space-2) var(--space-3); margin: var(--space-1) 0 var(--space-2); padding: var(--space-3); border-left: 3px solid var(--line); border-radius: var(--radius-sm); background: var(--surface-2); font-size: var(--fs-3); }
  dt { color: var(--text-2); }
  dd { margin: 0; overflow-wrap: anywhere; }
  .visually-hidden { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; border: 0; }
  @media (max-width: 640px) {
    dl { grid-template-columns: 1fr; }
    dd { margin-bottom: var(--space-2); }
  }
</style>
