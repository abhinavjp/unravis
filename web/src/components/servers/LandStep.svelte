<script lang="ts">
  import type { ServersFlow } from '../../lib/serversFlow.svelte';
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';
  import { downloadPulledFile } from '../../lib/serversApi';
  import { errorText, failureText, formatBytes, resultSummary } from '../../lib/servers';

  // Step 7. What was copied, from the copy's own result. A Blackbox set is saved by the backend and opened in the Blackbox view.
  // Other kinds of file are offered as a download from the copy's folder on this PC.
  let { flow, onOpenBlackbox }: { flow: ServersFlow; onOpenBlackbox: () => void } = $props();

  let saving = $state('');
  let saveError = $state('');

  const items = $derived(flow.order.map((key) => ({ key, pull: flow.pulls[key], target: flow.targets.find((t) => t.key === key) })).filter((i) => i.pull?.job));

  async function save(serverId: string, jobId: string, name: string) {
    saving = jobId + '/' + name; saveError = '';
    try { await downloadPulledFile(serverId, jobId, name); } catch (e) { saveError = errorText(e); } finally { saving = ''; }
  }
</script>

<section class="sv-block" aria-labelledby="sv-land-title">
  <h3 id="sv-land-title">{T.landTitle}</h3>
  <p class="sv-lead">{T.landLead}</p>

  {#if items.length === 0}
    <p class="sv-note">{T.landEmpty}</p>
  {/if}

  <ul class="sv-list" aria-label={T.landTitle}>
    {#each items as item (item.key)}
      {@const job = item.pull?.job}
      {#if job}
        {@const sum = resultSummary(job.result)}
        <li class="sv-row">
          <div class="sv-row-main"><strong>{item.target?.label ?? job.kind}</strong> <span class="sv-note">{item.target?.machine ?? job.machine ?? ''}</span>
            {#if item.pull?.state === 'failed'}<span class="sv-chip sv-tone-bad">{T.landFailedHeading}</span>{:else if item.pull?.state === 'stopped'}<span class="sv-chip sv-tone-pending">{T.landStoppedHeading}</span>{/if}</div>
          <code class="sv-dir">{job.dir}</code>
          {#if item.pull?.failure}<p class="sv-bad">{failureText(item.pull.failure)}</p>{/if}
          {#if sum.files > 0}
            <ul class="sv-facts-list">
              <li>{T.hasFiles(sum.files, formatBytes(sum.bytes))}</li>
              {#if sum.verified > 0}<li>{T.landVerified(sum.verified)}</li>{/if}
              {#if sum.notChecked > 0}<li>{T.landNotChecked(sum.notChecked)}</li>{/if}
              {#if sum.changed > 0}<li>{T.landChanged(sum.changed)}</li>{/if}
              {#if sum.linesHidden > 0}<li>{T.landHidden(sum.linesHidden)}</li>{/if}
              {#if sum.linesDropped > 0}<li>{T.landDropped(sum.linesDropped)}</li>{/if}
            </ul>
          {/if}
          {#if job.blackboxName}
            <p class="sv-line">{T.landBlackbox} <strong>{job.blackboxName}</strong></p>
            <p class="sv-note">{T.landBlackboxHelp}</p>
            <div class="sv-actions"><button type="button" class="primary-button" onclick={onOpenBlackbox}>{T.landOpenBlackbox}</button></div>
          {:else if job.blackboxNote}
            <p class="sv-note">{job.blackboxNote}</p>
          {/if}
          {#if !job.blackboxName && job.result && flow.server}
            {@const serverId = flow.server.id}
            <ul class="sv-files" aria-label={T.landLogs}>
              {#each job.result.files.filter((f) => f.status === 'done') as f (f.outName)}
                <li><code>{f.outName}</code> <span class="sv-note">{formatBytes(f.outBytes)}</span>
                  <button type="button" class="quiet-button" disabled={saving !== ''} onclick={() => save(serverId, job.id, f.outName)}>{saving === job.id + '/' + f.outName ? T.landDownloading : T.landDownload}</button></li>
              {/each}
            </ul>
          {/if}
        </li>
      {/if}
    {/each}
  </ul>
  {#if saveError}<p class="sv-bad" role="alert">{saveError}</p>{/if}

  <div class="sv-actions">
    <button type="button" class="secondary-button" onclick={() => flow.go('has')}>{T.landAnother}</button>
    <button type="button" class="quiet-button" onclick={() => flow.reset()}>{T.landDone}</button>
  </div>
</section>

<style>
  .sv-list { display: grid; margin: 0; padding: 0; list-style: none; }
  .sv-dir { font-family: var(--mono); font-size: 12px; overflow-wrap: anywhere; color: var(--text-2); }
  .sv-facts-list { margin: 0; padding-left: var(--space-5); font-size: 13px; }
  .sv-files { display: grid; gap: var(--space-1); margin: 0; padding: 0; list-style: none; font-size: 13px; }
  .sv-files li { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); overflow-wrap: anywhere; }
  .sv-files code { font-family: var(--mono); font-size: 12px; }
  .sv-line { margin: 0; font-size: 13px; }
</style>
