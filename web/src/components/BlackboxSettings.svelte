<script lang="ts">
  import { INTERVALS, type Settings } from '../lib/blackbox';
  import { BB_TEXT as T } from '../lib/copy/blackboxCopy';

  // The settings fields of a Blackbox, shared by the Place wizard (part 3) and Apply settings on this PC.
  let { settings = $bindable(), logPathsText = $bindable(), idPrefix = 'bb' }: { settings: Settings; logPathsText: string; idPrefix?: string } = $props();
</script>

<section class="bb-block" aria-label={T.settingsHeading}>
  <h4>{T.settingsHeading}</h4>
  <label class="bb-field"><span>{T.sampleEvery}</span>
    <select bind:value={settings.intervalSeconds}>{#each INTERVALS as s (s)}<option value={s}>{s} {T.seconds}</option>{/each}</select>
  </label>
  <fieldset class="bb-checks">
    <legend>{T.whatToRecord}</legend>
    <p class="bb-note">{T.recordCounters} <span>({T.recordCountersNote})</span></p>
    <label class="bb-check"><input type="checkbox" bind:checked={settings.record.processes} /><span>{T.recordProcesses}</span></label>
    {#if !settings.record.processes}<p class="bb-note">{T.processesNote}</p>{/if}
    <label class="bb-check"><input type="checkbox" bind:checked={settings.record.logsCatalogue} /><span>{T.recordLogs}</span></label>
    <label class="bb-check"><input type="checkbox" bind:checked={settings.record.copyLogsOnSnapshot} /><span>{T.recordCopyLogs}</span></label>
    <p class="bb-note">{T.probeNote}</p>
  </fieldset>
  <h4>{T.keepHeading}</h4>
  <label class="bb-field"><span>{T.keepDays}</span><input type="number" min="1" max="365" step="1" bind:value={settings.keepDays} aria-describedby="{idPrefix}-keep-help" /></label>
  <p class="bb-note" id="{idPrefix}-keep-help">{T.keepDaysHelp}</p>
  <label class="bb-field"><span>{T.sizeLimit}</span><input type="number" min="0.1" max="10000" step="0.1" bind:value={settings.maxGB} aria-describedby="{idPrefix}-size-help" /></label>
  <p class="bb-note" id="{idPrefix}-size-help">{T.sizeLimitHelp}</p>
  <label class="bb-field"><span>{T.keepFree}</span><input type="number" min="1" max="50" step="1" bind:value={settings.minFreePercent} aria-describedby="{idPrefix}-free-help" /></label>
  <p class="bb-note" id="{idPrefix}-free-help">{T.keepFreeHelp}</p>
  <p class="bb-note">{T.housekeepingNote}</p>
  <label class="bb-field"><span>{T.folder}</span><input type="text" bind:value={settings.folder} autocomplete="off" spellcheck="false" aria-describedby="{idPrefix}-folder-help" /></label>
  <p class="bb-note" id="{idPrefix}-folder-help">{T.folderHelp}</p>
  <label class="bb-field"><span>{T.port}</span><input type="number" min="1" max="65535" step="1" bind:value={settings.port} aria-describedby="{idPrefix}-port-help" /></label>
  <p class="bb-note" id="{idPrefix}-port-help">{T.portHelp}</p>
  <label class="bb-field"><span>{T.bind}</span><input type="text" bind:value={settings.bind} autocomplete="off" spellcheck="false" aria-describedby="{idPrefix}-bind-help" /></label>
  <p class="bb-note" id="{idPrefix}-bind-help">{T.bindHelp}</p>
  <label class="bb-field"><span>{T.logPaths}</span><textarea rows="2" bind:value={logPathsText} spellcheck="false"></textarea></label>
</section>

<style>
  .bb-block { display: grid; gap: var(--space-2); min-width: 0; margin: 0; padding: 0; border: 0; }
  .bb-block h4 { margin: 0; font-size: 14px; font-weight: 600; }
  .bb-note { margin: 0; color: var(--text-2); font-size: 12px; line-height: 1.5; overflow-wrap: anywhere; }
  .bb-field { display: grid; gap: 4px; font-size: 13px; font-weight: 500; }
  .bb-field input[type='text'], .bb-field input[type='number'], .bb-field select, .bb-field textarea { width: 100%; max-width: 420px; padding: 6px 8px; font: inherit; font-weight: 400; color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); box-sizing: border-box; }
  .bb-field textarea { font-family: var(--mono); font-size: 12px; resize: vertical; }
  .bb-checks { display: grid; gap: 6px; margin: 0; padding: 0; border: 0; }
  .bb-checks legend { padding: 0; font-size: 13px; font-weight: 500; }
  .bb-check { display: flex; align-items: flex-start; gap: 8px; font-size: 13px; }
  .bb-check input { flex: 0 0 auto; margin: 3px 0 0; }
</style>
