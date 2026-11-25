<script lang="ts">
  import type { SqlAccessReport } from '../../lib/types';
  import { ACCESS_TEXT } from '../../lib/copy/sqlAccessCopy';
  import { isReady, scriptToShow, stateWord, verdictLine } from '../../lib/sqlAccess';
  import './sources.css';

  // The answer of the Blackbox access check: one line per check, one verdict, and a script only when something is missing.
  let { report }: { report: SqlAccessReport } = $props();
  const uid = $props.id();
  const script = $derived(scriptToShow(report));
  let note = $state('');
  let heading: HTMLElement | undefined = $state();
  // The result appears after a button press: move focus to its heading so a screen reader reads it.
  $effect(() => { heading?.focus({ preventScroll: false }); });

  async function copy() {
    try { await navigator.clipboard.writeText(script); note = ACCESS_TEXT.copied; } catch { note = ACCESS_TEXT.copyFailed; }
  }
</script>

<section class="access" aria-labelledby="{uid}-h">
  <h5 id="{uid}-h" tabindex="-1" bind:this={heading}>{ACCESS_TEXT.heading}</h5>
  <ul class="checks" role="list">
    {#each report.checks as c (c.id + c.label)}
      <li data-state={c.state}>
        <span class="word {c.state}">{stateWord(c.state)}</span>
        <span><b>{c.label}</b><span class="detail">{c.detail}</span></span>
      </li>
    {/each}
  </ul>
  {#if report.writableNames && report.writableNames.length}<p class="detail">{ACCESS_TEXT.writable(report.writableNames, report.writableDatabases)}</p>{/if}
  <p class="verdict" class:good={isReady(report)}>{verdictLine(report)}</p>
  {#if script}
    <label class="script-label" for="{uid}-script">{ACCESS_TEXT.scriptLabel}</label>
    <textarea id="{uid}-script" class="script" readonly rows={Math.min(8, script.split('\n').length)}>{script}</textarea>
    <div class="actions"><button type="button" class="btn" onclick={() => void copy()}>{ACCESS_TEXT.copy}</button><span class="detail" role="status">{note}</span></div>
  {/if}
  <p class="detail">{ACCESS_TEXT.note}</p>
</section>

<style>
  .access { margin-top: var(--space-4); padding-top: var(--space-3); border-top: 1px solid var(--line); display: grid; gap: var(--space-2); min-width: 0; }
  h5 { margin: 0; font-size: 1rem; }
  .checks { list-style: none; margin: 0; padding: 0; display: grid; gap: var(--space-2); }
  .checks li { display: grid; grid-template-columns: 6.5rem 1fr; gap: var(--space-3); align-items: start; min-width: 0; }
  .detail { display: block; color: var(--text-2); font-size: 0.9rem; overflow-wrap: anywhere; }
  .word { font-weight: 600; font-size: 0.85rem; }
  .word.passed { color: var(--accent); }
  .word.failed { color: var(--bad); }
  .word.warning { color: var(--warn); }
  .word.skipped { color: var(--text-2); }
  .verdict { margin: 0; font-weight: 600; }
  .verdict.good { color: var(--accent); }
  .script { font-family: var(--mono, ui-monospace, monospace); font-size: 0.85rem; width: 100%; resize: vertical; }
  .script-label { font-size: 0.9rem; }
  @media (max-width: 480px) { .checks li { grid-template-columns: 1fr; gap: 0; } }
</style>
