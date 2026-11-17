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
  summary { display: flex; align-items: center; width: fit-content; min-width: 40px; min-height: 44px; color: var(--accent); cursor: pointer; text-decoration: underline; text-underline-offset: 3px; }
  summary::before { content: '▸'; margin-right: var(--space-2); text-decoration: none; }
  details[open] > summary::before { content: '▾'; }
  summary:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius-sm); }
  dl { display: grid; gap: var(--space-1); margin: 0 0 var(--space-2); }
  dt { color: var(--text); font-weight: 600; }
  dd { margin: 0 0 var(--space-2); color: var(--text-2); overflow-wrap: anywhere; }
  .visually-hidden { position: absolute; width: 1px; height: 1px; padding: 0; margin: -1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; border: 0; }
</style>
