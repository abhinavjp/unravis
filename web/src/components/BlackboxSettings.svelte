<script lang="ts">
  import { INTERVALS, listensOnAll, type Settings, type SettingsProblem } from '../lib/blackbox';
  import { BB_TEXT as T } from '../lib/copy/blackboxCopy';

  // The settings fields of a Blackbox, shared by the Place wizard (part 3) and Apply settings on this PC.
  // `problem` is the field that is wrong, if any: its message shows under it and the field points at the message (D-B-50).
  let { settings = $bindable(), logPathsText = $bindable(), idPrefix = 'bb', problem = null }: { settings: Settings; logPathsText: string; idPrefix?: string; problem?: SettingsProblem | null } = $props();
  const warnAll = $derived(listensOnAll(settings.bind));
  const invalid = (key: SettingsProblem) => (problem === key ? 'true' : undefined);
  const described = (key: SettingsProblem, help: string) => idPrefix + '-' + help + (problem === key ? ' ' + idPrefix + '-' + key + '-error' : '');
</script>

{#snippet fieldError(key: SettingsProblem)}
  {#if problem === key}<p class="inline-error" id="{idPrefix}-{key}-error" role="alert">{T.settingProblems[key]}</p>{/if}
{/snippet}

<section class="bb-block" aria-label={T.settingsHeading}>
  <h4>{T.settingsHeading}</h4>
  <label class="bb-field"><span>{T.sampleEvery}</span>
    <select id="{idPrefix}-f-interval" bind:value={settings.intervalSeconds} aria-invalid={invalid('interval')} aria-describedby={problem === 'interval' ? idPrefix + '-interval-error' : undefined}>{#each INTERVALS as s (s)}<option value={s}>{s} {T.seconds}</option>{/each}</select>
  </label>
  {@render fieldError('interval')}
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
  <label class="bb-field"><span>{T.keepDays}</span><input id="{idPrefix}-f-keep" type="number" min="1" max="365" step="1" bind:value={settings.keepDays} aria-invalid={invalid('keep')} aria-describedby={described('keep', 'keep-help')} /></label>
  <p class="bb-note" id="{idPrefix}-keep-help">{T.keepDaysHelp}</p>
  {@render fieldError('keep')}
  <label class="bb-field"><span>{T.sizeLimit}</span><input id="{idPrefix}-f-size" type="number" min="0.1" max="10000" step="0.1" bind:value={settings.maxGB} aria-invalid={invalid('size')} aria-describedby={described('size', 'size-help')} /></label>
  <p class="bb-note" id="{idPrefix}-size-help">{T.sizeLimitHelp}</p>
  {@render fieldError('size')}
  <label class="bb-field"><span>{T.keepFree}</span><input id="{idPrefix}-f-free" type="number" min="1" max="50" step="1" bind:value={settings.minFreePercent} aria-invalid={invalid('free')} aria-describedby={described('free', 'free-help')} /></label>
  <p class="bb-note" id="{idPrefix}-free-help">{T.keepFreeHelp}</p>
  {@render fieldError('free')}
  <p class="bb-note">{T.housekeepingNote}</p>
  <label class="bb-field"><span>{T.folder}</span><input id="{idPrefix}-f-folder" type="text" bind:value={settings.folder} autocomplete="off" spellcheck="false" aria-invalid={invalid('folder')} aria-describedby={described('folder', 'folder-help')} /></label>
  <p class="bb-note" id="{idPrefix}-folder-help">{T.folderHelp}</p>
  {@render fieldError('folder')}
  <label class="bb-field"><span>{T.port}</span><input id="{idPrefix}-f-port" type="number" min="1" max="65535" step="1" bind:value={settings.port} aria-invalid={invalid('port')} aria-describedby={described('port', 'port-help')} /></label>
  <p class="bb-note" id="{idPrefix}-port-help">{T.portHelp}</p>
  {@render fieldError('port')}
  <label class="bb-field"><span>{T.bind}</span><input id="{idPrefix}-f-bind" type="text" bind:value={settings.bind} autocomplete="off" spellcheck="false" aria-invalid={invalid('bind')} aria-describedby="{idPrefix}-bind-help{warnAll ? ' ' + idPrefix + '-bind-warn' : ''}{problem === 'bind' ? ' ' + idPrefix + '-bind-error' : ''}" /></label>
  <p class="bb-note" id="{idPrefix}-bind-help">{T.bindHelp}</p>
  {#if warnAll}<p class="bb-note bb-warn" id="{idPrefix}-bind-warn">{T.bindWarn}</p>{/if}
  {@render fieldError('bind')}
  <label class="bb-field"><span>{T.logPaths}</span><textarea id="{idPrefix}-f-logs" rows="2" bind:value={logPathsText} spellcheck="false" aria-invalid={invalid('logs')} aria-describedby={problem === 'logs' ? idPrefix + '-logs-error' : undefined}></textarea></label>
  {@render fieldError('logs')}
</section>

<style>
  .bb-block { display: grid; gap: var(--space-2); min-width: 0; margin: 0; padding: 0; border: 0; }
  .bb-block h4 { margin: 0; font-size: 14px; font-weight: 600; }
  .bb-note { margin: 0; color: var(--text-2); font-size: 12px; line-height: 1.5; overflow-wrap: anywhere; }
  .bb-warn { color: var(--text); border-left: 3px solid var(--warn); padding-left: var(--space-2); }
  .bb-block .inline-error { margin: 0; max-width: 420px; }
  .bb-field { display: grid; gap: 4px; font-size: 13px; font-weight: 500; }
  .bb-field input[type='text'], .bb-field input[type='number'], .bb-field select, .bb-field textarea { width: 100%; max-width: 420px; padding: 6px 8px; font: inherit; font-weight: 400; color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); box-sizing: border-box; }
  .bb-field textarea { font-family: var(--mono); font-size: 12px; resize: vertical; }
  .bb-checks { display: grid; gap: 6px; margin: 0; padding: 0; border: 0; }
  .bb-checks legend { padding: 0; font-size: 13px; font-weight: 500; }
  .bb-check { display: flex; align-items: flex-start; gap: 8px; font-size: 13px; }
  .bb-check input { flex: 0 0 auto; margin: 3px 0 0; }
</style>
