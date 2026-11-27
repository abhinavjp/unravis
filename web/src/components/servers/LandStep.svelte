<script lang="ts">
  import StepFooter from './StepFooter.svelte';
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
  const isBlackbox = (i: (typeof items)[number]) => !!i.pull?.job?.blackboxName || i.target?.pullKind === 'blackbox';
  const sets = $derived(items.filter(isBlackbox));
  const logs = $derived(items.filter((i) => !isBlackbox(i)));

  async function save(serverId: string, jobId: string, name: string) {
    saving = jobId + '/' + name; saveError = '';
    try { await downloadPulledFile(serverId, jobId, name); } catch (e) { saveError = errorText(e); } finally { saving = ''; }
  }
</script>

<section class="sv-block" aria-labelledby="sv-land-title">
  <h3 id="sv-land-title">{T.landTitle}</h3>
  <p class="sv-lead">{T.landLead}</p>
  {#if items.length === 0}<p class="sv-note">{T.landEmpty}</p>{/if}

  <div class="sv-panel">
    <header><h4>{T.landBlackboxPanel}</h4><span class="sv-sub">{T.landBlackboxPanelSub}</span><span class="sv-sp"></span>
      <button type="button" class="primary-button sv-small" disabled={sets.every((i) => !i.pull?.job?.blackboxName)} onclick={onOpenBlackbox}>{T.landOpenBlackbox}</button></header>
    {#each sets as item (item.key)}
      {@const job = item.pull?.job}
      {#if job}
        {@const sum = resultSummary(job.result)}
        <div class="sv-land-row">
          <div class="sv-land-main">
            <b>{job.blackboxName ?? item.target?.machine ?? job.machine ?? ''}</b>
            <small>{#if sum.files > 0}{T.landSets(sum.files, formatBytes(sum.bytes))}{#if sum.verified > 0 && sum.verified === sum.files}. {T.landHashes}{:else if sum.verified > 0}. {T.landVerified(sum.verified)}{/if}{/if}
              {#if sum.notChecked > 0} {T.landNotChecked(sum.notChecked)}{/if}{#if sum.changed > 0} {T.landChanged(sum.changed)}{/if}</small>
            {#if !job.blackboxName && job.blackboxNote}<small>{job.blackboxNote}</small>{/if}
            {#if item.pull?.failure}<small class="sv-bad">{failureText(item.pull.failure)}</small>{/if}
            {#if item.pull?.state === 'failed'}<small class="sv-bad">{T.landFailedHeading}</small>{:else if item.pull?.state === 'stopped'}<small class="sv-bad">{T.landStoppedHeading}</small>{/if}
          </div>
        </div>
      {/if}
    {:else}
      <div class="sv-in sv-sub">{T.landNoBlackbox}</div>
    {/each}
  </div>

  <div class="sv-panel">
    <header><h4>{T.landLogs}</h4><span class="sv-sub">{T.landLogsPanelSub}</span></header>
    {#each logs as item (item.key)}
      {@const job = item.pull?.job}
      {#if job}
        {@const sum = resultSummary(job.result)}
        <div class="sv-land-row">
          <div class="sv-land-main">
            <b>{item.target?.machine ?? job.machine ?? ''}: {item.target?.label ?? job.kind}</b>
            <small>{#if sum.files > 0}{T.hasFiles(sum.files, formatBytes(sum.bytes))}.{/if}
              {#if sum.linesHidden > 0} {T.landHidden(sum.linesHidden)}{/if}{#if sum.linesDropped > 0} {T.landDropped(sum.linesDropped)}{/if}
              {#if sum.notChecked > 0} {T.landNotChecked(sum.notChecked)}{/if}{#if sum.changed > 0} {T.landChanged(sum.changed)}{/if}</small>
            {#if item.pull?.failure}<small class="sv-bad">{failureText(item.pull.failure)}</small>{/if}
            {#if item.pull?.state === 'failed'}<small class="sv-bad">{T.landFailedHeading}</small>{:else if item.pull?.state === 'stopped'}<small class="sv-bad">{T.landStoppedHeading}</small>{/if}
            {#if job.result && flow.server}
              {@const serverId = flow.server.id}
              {@const saved = job.result.files.filter((f) => f.status === 'done')}
              {#if saved.length > 0}
              <details class="sv-filelist"><summary>{T.landFilesSummary(saved.length)}</summary>
              <ul class="sv-files" aria-label={T.landLogs}>
                {#each saved as f (f.outName)}
                  <li><code>{f.outName}</code> <span class="sv-sub">{formatBytes(f.outBytes)}</span>
                    <button type="button" class="quiet-button" disabled={saving !== ''} onclick={() => save(serverId, job.id, f.outName)}>{saving === job.id + '/' + f.outName ? T.landDownloading : T.landDownload}</button></li>
                {/each}
              </ul>
              </details>
              {/if}
            {/if}
          </div>
          <span class="sv-sub">{T.landSource}: <span class="sv-mono">{item.target?.kind ?? job.kind}</span></span>
        </div>
      {/if}
    {:else}
      <div class="sv-in sv-sub">{T.landNoLogs}</div>
    {/each}
  </div>
  {#if saveError}<p class="sv-bad" role="alert">{saveError}</p>{/if}

  <StepFooter back={() => flow.go('has')} backLabel={T.landAnother} primary={{ label: T.landOpenBlackbox, onclick: onOpenBlackbox }} />
</section>

<style>
  .sv-land-row { display: flex; flex-wrap: wrap; align-items: flex-start; justify-content: space-between; gap: 8px 16px; padding: 10px 14px; border-bottom: 1px solid var(--line); }
  .sv-land-row:last-child { border-bottom: 0; }
  .sv-land-main { display: grid; gap: 3px; min-width: 0; flex: 1 1 320px; }
  .sv-land-main small { color: var(--text-2); font-size: var(--fs-2); }
  .sv-land-main small.sv-bad { color: var(--bad); }
  .sv-files { display: grid; gap: 4px; margin: 4px 0 0; padding: 0; list-style: none; font-size: var(--fs-2); }
  .sv-files li { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; overflow-wrap: anywhere; }
  .sv-files code { font-family: var(--mono); font-size: 12px; }
  .sv-filelist summary { cursor: pointer; font-size: var(--fs-2); color: var(--text-2); }
  .sv-small { padding: 4px 10px; }
</style>
