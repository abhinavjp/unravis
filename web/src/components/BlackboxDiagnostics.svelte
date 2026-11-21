<script lang="ts">
  import { onDestroy } from 'svelte';
  import { blackboxApi, refusalText, type BlackboxItem, type DiagnosticsAnswer } from '../lib/blackbox';
  import { BB_REFUSAL } from '../lib/copy/blackboxCopy';

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
      problem = refusalText(error, { ...BB_REFUSAL, other: 'The diagnostics could not be read.' });
    } finally {
      busy = false;
    }
  }
  async function copy() {
    if (!answer) return;
    try { await navigator.clipboard.writeText(answer.text); copied = 'yes'; } catch { copied = 'no'; }
  }
</script>

<section class="bb-confirm" aria-label="Diagnostics">
  <strong>Diagnostics</strong>
  <p class="bb-note">Why is this Blackbox slow or late? This reads its timing log and says what it found.</p>
  <div class="bb-actions">
    <button type="button" class="secondary-button" disabled={busy} onclick={load}>{answer ? 'Read again' : 'Read the timing log'}</button>
    {#if answer}<button type="button" class="secondary-button" onclick={copy}>Copy diagnostics</button>{/if}
  </div>
  {#if busy}<p class="bb-note" role="status">Reading the log…</p>{/if}
  {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
  {#if copied === 'yes'}<p class="bb-ok" role="status">Copied. Paste it where you need it.</p>{/if}
  {#if copied === 'no'}<p class="inline-error" role="alert">Could not copy. Select the text below and copy it by hand.</p>{/if}
  {#if answer}
    <p><strong>{answer.headline}</strong></p>
    <ul class="bb-diag-list">
      {#each answer.findings as f (f.code)}
        <li><strong>{f.title}</strong> {f.detail}</li>
      {/each}
    </ul>
    {#if answer.log}
      <details>
        <summary>Newest log lines</summary>
        <textarea class="bb-diag-log" readonly rows="10" aria-label="Diagnostics log lines" value={answer.log}></textarea>
      </details>
    {/if}
  {/if}
</section>

<style>
  .bb-diag-list { margin: 0.4rem 0; padding-left: 1.2rem; }
  .bb-diag-list li { margin: 0.25rem 0; }
  .bb-diag-log { width: 100%; box-sizing: border-box; font: 12px/1.4 ui-monospace, monospace; white-space: pre; padding: var(--space-2); background: var(--surface-2); color: var(--text); border: 1px solid var(--line); border-radius: var(--radius-sm); }
</style>
