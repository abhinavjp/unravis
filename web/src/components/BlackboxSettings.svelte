<script lang="ts">
  import { onMount } from 'svelte';
  import { INTERVALS, blackboxApi, listensOnAll, toggleTarget, type Settings, type SettingsProblem, type TargetInfo, type TargetKind, type TargetRef } from '../lib/blackbox';
  import { BB_TEXT as T } from '../lib/copy/blackboxCopy';

  // The settings fields of a Blackbox, shared by the Place wizard (part 3) and Apply settings on this PC.
  // `problem` is the field that is wrong, if any: its message shows under it and the field points at the message (D-B-50).
  // `targetMode` is 'pc' when the Blackbox runs on this PC (the wizard's this-PC choice and Apply settings):
  // the detected app pools and services are read once on mount. 'manual' (pack/remote) never fetches: names are typed.
  let { settings = $bindable(), logPathsText = $bindable(), idPrefix = 'bb', problem = null, targetMode = 'pc' }: { settings: Settings; logPathsText: string; idPrefix?: string; problem?: SettingsProblem | null; targetMode?: 'pc' | 'manual' } = $props();
  const warnAll = $derived(listensOnAll(settings.bind));
  const invalid = (key: SettingsProblem) => (problem === key ? 'true' : undefined);
  const described = (key: SettingsProblem, help: string) => idPrefix + '-' + help + (problem === key ? ' ' + idPrefix + '-' + key + '-error' : '');

  // Targets (BBX-1-12): only kind+name is ever stored. A target that is not running says "not running", never 0.
  let appPools = $state<TargetInfo[]>([]);
  let services = $state<TargetInfo[]>([]);
  let detected = $state(false);
  let manualKind = $state<TargetKind>('appPool');
  let manualName = $state('');

  onMount(() => {
    if (targetMode !== 'pc') return;
    let alive = true;
    blackboxApi.targets().then((answer) => {
      if (!alive) return;
      appPools = answer.appPools;
      services = answer.services;
      detected = answer.detected;
    }).catch(() => { /* nothing could be read: the manual row below is used instead */ });
    return () => { alive = false; };
  });

  const picked = $derived(settings.targets ?? []);
  // Detected names are offered as checkboxes; with nothing detected (or pack/remote) names are typed instead.
  const showLists = $derived(targetMode === 'pc' && detected && (appPools.length > 0 || services.length > 0));
  const extraPicked = $derived(picked.filter((t) => !(t.kind === 'appPool' ? appPools : services).some((d) => d.name === t.name)));
  const isSelected = (kind: TargetKind, name: string) => picked.some((t) => t.kind === kind && t.name === name);
  function pick(kind: TargetKind, name: string) { settings.targets = toggleTarget(picked, kind, name); }
  function addTarget() {
    const name = manualName.trim();
    if (!name) return;
    if (!isSelected(manualKind, name)) settings.targets = toggleTarget(picked, manualKind, name);
    manualName = '';
  }
  function removeTarget(kind: TargetKind, name: string) { settings.targets = toggleTarget(picked, kind, name); }
</script>

{#snippet fieldError(key: SettingsProblem)}
  {#if problem === key}<p class="inline-error" id="{idPrefix}-{key}-error" role="alert">{T.settingProblems[key]}</p>{/if}
{/snippet}

{#snippet targetChips(list: TargetRef[])}
  {#if list.length > 0}
    <ul class="bb-chips">
      {#each list as t (t.kind + ':' + t.name)}
        <li class="bb-chip"><span>{t.kind === 'appPool' ? T.targetsKindAppPool : T.targetsKindService}: {t.name}</span>
          <button type="button" class="bb-chip-x" aria-label="{T.targetsRemove}: {t.name}" onclick={() => removeTarget(t.kind, t.name)}>×</button>
        </li>
      {/each}
    </ul>
  {/if}
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
  <h4>{T.targetsHeading}</h4>
  <p class="bb-note">{T.targetsHelp}</p>
  {#if showLists}
    <fieldset class="bb-checks">
      <legend>{T.targetsAppPools}</legend>
      {#each appPools as t (t.name)}
        <label class="bb-check"><input type="checkbox" checked={isSelected('appPool', t.name)} onchange={() => pick('appPool', t.name)} /><span>{t.name}{#if !t.running} <span class="bb-note">{T.targetsNotRunning}</span>{/if}</span></label>
      {/each}
    </fieldset>
    <fieldset class="bb-checks">
      <legend>{T.targetsServices}</legend>
      {#each services as t (t.name)}
        <label class="bb-check"><input type="checkbox" checked={isSelected('service', t.name)} onchange={() => pick('service', t.name)} /><span>{t.name}{#if !t.running} <span class="bb-note">{T.targetsNotRunning}</span>{/if}</span></label>
      {/each}
    </fieldset>
    {@render targetChips(extraPicked)}
  {:else}
    <p class="bb-note">{T.targetsManualNote}</p>
    <div class="bb-target-add">
      <label class="bb-field"><span>{T.targetsKindLabel}</span>
        <select bind:value={manualKind}>
          <option value="appPool">{T.targetsKindAppPool}</option>
          <option value="service">{T.targetsKindService}</option>
        </select>
      </label>
      <label class="bb-field"><span>{T.targetsNameLabel}</span>
        <input type="text" bind:value={manualName} autocomplete="off" spellcheck="false" onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addTarget(); } }} />
      </label>
      <button type="button" class="secondary-button" disabled={!manualName.trim()} onclick={addTarget}>{T.targetsAdd}</button>
    </div>
    {@render targetChips(picked)}
  {/if}
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
  .bb-target-add { display: flex; flex-wrap: wrap; align-items: end; gap: var(--space-2); }
  .bb-target-add .bb-field { flex: 1 1 160px; max-width: 240px; }
  .bb-chips { display: flex; flex-wrap: wrap; gap: 6px; margin: 0; padding: 0; list-style: none; }
  .bb-chip { display: inline-flex; align-items: center; gap: 6px; padding: 2px 8px; font-size: 12px; border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface); overflow-wrap: anywhere; }
  .bb-chip-x { border: 0; padding: 0 2px; background: none; color: var(--text-2); font-size: 14px; line-height: 1; cursor: pointer; }
  .bb-chip-x:hover { color: var(--text); }
</style>
