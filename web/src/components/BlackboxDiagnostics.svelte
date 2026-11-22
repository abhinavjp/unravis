<script lang="ts">
  import { onDestroy } from 'svelte';
  import { blackboxApi, refusalText, type BlackboxItem, type DiagnosticsAnswer } from '../lib/blackbox';
  import { BB_REFUSAL, BB_TEXT as T } from '../lib/copy/blackboxCopy';

  // Diagnostics: reads the recorder's own timing log and says, in plain words, why it runs late. Nothing is read until the
  // button is pressed. Copy gives the same words plus the newest log lines, for sending to whoever looks after the PC.
  let { item }: { item: BlackboxItem } = $props();

  let busy = $state(false);
  let answer = $state<DiagnosticsAnswer | null>(null);
  let problem = $state('');
  let copied = $state<'' | 'yes' | 'no'>('');
  let ctl: AbortController | undefined;
  onDestroy(() => ctl?.abort());

  async function load() {
    if (busy) return;
    busy = true; problem = ''; copied = '';
    ctl?.abort();
    ctl = new AbortController();
    try {
      answer = await blackboxApi.diagnostics(item.id, ctl.signal);
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') return;
      answer = null;
      problem = refusalText(error, { ...BB_REFUSAL, other: T.diagFailed });
    } finally {
      busy = false;
    }
  }
  // An older Blackbox, or one that has only just started, has no timing log: there is nothing to copy then.
  const empty = $derived(!!answer && answer.lines === 0 && !answer.log);
  async function copy() {
    if (!answer) return;
    try { await navigator.clipboard.writeText(answer.text); copied = 'yes'; } catch { copied = 'no'; }
  }
</script>

<section class="bb-diag" aria-label={T.diagTitle}>
  <strong>{T.diagTitle}</strong>
  <p class="bb-note">{T.diagIntro}</p>
  <div class="bb-actions">
    <button type="button" class="secondary-button" disabled={busy} onclick={load}>{answer ? T.diagReadAgain : T.diagRead}</button>
    {#if answer && !empty}<button type="button" class="secondary-button" onclick={copy}>{T.diagCopy}</button>{/if}
  </div>
  {#if busy}<p class="bb-note" role="status">{T.diagReading}</p>{/if}
  {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
  {#if copied === 'yes'}<p class="bb-ok" role="status">{T.diagCopied}</p>{/if}
  {#if copied === 'no'}<p class="inline-error" role="alert">{T.diagCopyFailed}</p>{/if}
  {#if answer}
    <p><strong>{answer.headline}</strong></p>
    {#if empty}
      <p class="bb-note">{answer.findings[0]?.detail || T.diagNone}</p>
    {:else}
      <ul class="bb-diag-list">
        {#each answer.findings as f (f.code)}
          <li><strong>{f.title}</strong> {f.detail}</li>
        {/each}
      </ul>
    {/if}
    {#if answer.log}
      <details>
        <summary>{T.diagLog}</summary>
        <textarea class="bb-diag-log" readonly rows="10" aria-label={T.diagLogLabel} value={answer.log}></textarea>
      </details>
    {/if}
  {/if}
</section>

<style>
  .bb-diag { display: grid; gap: var(--space-2); min-width: 0; padding-top: var(--space-3); border-top: 1px solid var(--line); }
  .bb-diag > strong { font-size: 14px; font-weight: 600; }
  .bb-diag p { margin: 0; font-size: 13px; line-height: 1.5; }
  .bb-diag p.bb-note { font-size: 12px; }
  .bb-diag .bb-actions { margin-top: var(--space-1); }
  .bb-diag-list { margin: 0; padding-left: var(--space-5); display: grid; gap: var(--space-2); }
  .bb-diag-log { width: 100%; box-sizing: border-box; margin-top: var(--space-2); font: 12px/1.4 var(--mono); white-space: pre; padding: var(--space-2); background: var(--surface-2); color: var(--text); border: 1px solid var(--line); border-radius: var(--radius-sm); }
</style>
