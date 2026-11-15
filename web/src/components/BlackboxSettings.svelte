<script lang="ts">
  import { onMount } from 'svelte';
  import { INTERVALS, blackboxApi, listensOnAll, pickedTargetState, targetListState, targetsProblem, toggleTarget, type Settings, type SettingsProblem, type TargetInfo, type TargetKind, type TargetRef, targetRunState } from '../lib/blackbox';
  import { BB_TEXT as T } from '../lib/copy/blackboxCopy';

  // The settings fields of a Blackbox, shared by the Place wizard (part 3) and Apply settings on this PC.
  // `problem` is the field that is wrong, if any: its message shows under it and the field points at the message (D-B-50).
  // `targetMode` is 'pc' when the Blackbox runs on this PC (the wizard's this-PC choice and Apply settings):
  // the detected app pools and services are read once on mount. 'manual' (pack/remote) never fetches: names are typed.
  let { settings = $bindable(), logPathsText = $bindable(), idPrefix = 'bb', problem = null, targetMode = 'pc' }: { settings: Settings; logPathsText: string; idPrefix?: string; problem?: SettingsProblem | null; targetMode?: 'pc' | 'manual' } = $props();
  const warnAll = $derived(listensOnAll(settings.bind));
  const invalid = (key: SettingsProblem) => (problem === key ? 'true' : undefined);
  const described = (key: SettingsProblem, help: string) => idPrefix + '-' + help + (problem === key ? ' ' + idPrefix + '-' + key + '-error' : '');

  // Targets (BBX-1-12): only kind+name is ever stored. A stopped target says "not running";
  // one whose state could not be read says so, and is never labelled stopped (W2-07). A name
  // absent from a capped or unreadable list is unknown, never guessed stopped (W3-06).
  let appPools = $state<TargetInfo[]>([]);
  let services = $state<TargetInfo[]>([]);
  let detected = $state(false);
  let appPoolsState = $state<string | undefined>(undefined);
  let servicesState = $state<string | undefined>(undefined);
  let targetsRead = $state(false);
  // True when the one mount-time read was refused or never reached the server (UX-5). A failed
  // read is never shown as an empty healthy list: both detected lists say it could not be read.
  let targetsFailed = $state(false);
  let manualKind = $state<TargetKind>('appPool');
  let manualName = $state('');
  // Why the last Add was refused. The name stays in the box so it can be fixed (UX-4).
  let targetRefusal = $state('');
  // Per-group filter text (UX-6). Functions below read it live on every render, as groupState does.
  let poolFilter = $state('');
  let serviceFilter = $state('');

  onMount(() => {
    if (targetMode !== 'pc') return;
    let alive = true;
    blackboxApi.targets().then((answer) => {
      if (!alive) return;
      appPools = answer.appPools;
      services = answer.services;
      detected = answer.detected;
      appPoolsState = answer.appPoolsState;
      servicesState = answer.servicesState;
      targetsRead = true;
    }).catch(() => { targetsRead = true; targetsFailed = true; /* nothing could be read: the lists say so and names are typed below */ });
    return () => { alive = false; };
  });

  // What one detected list can honestly show (UX-5): loading, failed, not-found, unavailable,
  // empty or list. A function so it reads the live state on every render; the rule itself is pure.
  function groupState(kind: TargetKind) {
    return kind === 'appPool'
      ? targetListState(appPools, appPoolsState, detected, targetsRead, targetsFailed)
      : targetListState(services, servicesState, detected, targetsRead, targetsFailed);
  }

  // UX-6: a long list stays usable. The filter narrows what is shown, never the read list itself;
  // names match without regard to case, as Windows matches them. A blank filter shows everything.
  function shownTargets(kind: TargetKind): TargetInfo[] {
    const text = (kind === 'appPool' ? poolFilter : serviceFilter).trim().toLowerCase();
    const list = kind === 'appPool' ? appPools : services;
    return text ? list.filter((t) => t.name.toLowerCase().includes(text)) : list;
  }
  // What the count line under a filter box says: how many of the names are shown, or how to see
  // them all again when none match. The filter never silently hides that names exist.
  function filterNote(kind: TargetKind): string {
    const total = (kind === 'appPool' ? appPools : services).length;
    const shown = shownTargets(kind).length;
    return shown > 0 ? T.targetsFilterCount(shown, total) : T.targetsFilterNone(total);
  }
  // True when no row's running state could be read: one note under the legend says it, with the
  // consequence, instead of the same note on every row. A mixed list keeps its per-row notes.
  function allUnknown(kind: TargetKind): boolean {
    const list = kind === 'appPool' ? appPools : services;
    return list.length > 0 && list.every((t) => targetRunState(t) === 'unknown');
  }

  const picked = $derived(settings.targets ?? []);
  // Detected names are offered as checkboxes; the manual add row is always available alongside them,
  // so a known valid name can be added even when detection succeeded but did not list it (W3-06).
  const showLists = $derived(targetMode === 'pc' && detected && (appPools.length > 0 || services.length > 0));
  const extraPicked = $derived(picked.filter((t) => !(t.kind === 'appPool' ? appPools : services).some((d) => d.name === t.name)));
  // Chips carry an honest state note only on this PC once the single read has settled; pack/remote never fetches.
  const chipsLabelled = $derived(targetMode === 'pc' && targetsRead);
  const isSelected = (kind: TargetKind, name: string) => picked.some((t) => t.kind === kind && t.name === name);
  function pick(kind: TargetKind, name: string) { targetRefusal = ''; settings.targets = toggleTarget(picked, kind, name); }
  function chipNote(t: TargetRef): string {
    const s = pickedTargetState(t.kind, t.name, { appPools, services, detected, appPoolsState, servicesState });
    if (s === 'stopped') return T.targetsNotRunning;
    if (s === 'unknown') return T.targetsStateUnknown;
    if (s === 'not-in-list') return T.targetsNotInList;
    if (s === 'source-not-found') return T.targetsSourceNotFound;
    if (s === 'list-unreadable') return T.targetsListUnreadable;
    return '';
  }
  // The words beside the target row: why the last Add was refused, or the targets problem found at the
  // last attempt. One alert, so the row never shows two messages at once.
  function targetError(): string { return targetRefusal || (problem === 'targets' ? T.settingProblems.targets : ''); }
  function addTarget() {
    const name = manualName.trim();
    if (!name) return;
    // The server's own rules, checked here so a wrong name is refused beside the box instead of
    // coming back as a raw refusal. Nothing is added when the list would be wrong (UX-4).
    const why = targetsProblem([...picked, { kind: manualKind, name }]);
    if (why) { targetRefusal = T.targetProblems[why]; return; }
    if (!isSelected(manualKind, name)) settings.targets = toggleTarget(picked, manualKind, name);
    targetRefusal = '';
    manualName = '';
  }
  function removeTarget(kind: TargetKind, name: string) { targetRefusal = ''; settings.targets = toggleTarget(picked, kind, name); }
</script>

{#snippet fieldError(key: SettingsProblem)}
  {#if problem === key}<p class="inline-error" id="{idPrefix}-{key}-error" role="alert">{T.settingProblems[key]}</p>{/if}
{/snippet}

{#snippet rowNote(t: TargetInfo)}
  {#if targetRunState(t) === 'stopped'}{' '}<span class="bb-note">{T.targetsNotRunning}</span>{:else if targetRunState(t) === 'unknown'}{' '}<span class="bb-note">{T.targetsStateUnknown}</span>{/if}
{/snippet}

{#snippet targetChips(list: TargetRef[])}
  {#if list.length > 0}
    <ul class="bb-chips">
      {#each list as t (t.kind + ':' + t.name)}
        <li class="bb-chip"><span>{t.kind === 'appPool' ? T.targetsKindAppPool : T.targetsKindService}: {t.name}{#if chipsLabelled && chipNote(t)} <span class="bb-note">{chipNote(t)}</span>{/if}</span>
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
  {#if targetMode === 'pc' && groupState('appPool') === 'loading'}
    <p class="bb-note" role="status">{T.targetsLoading}</p>
  {:else if targetMode === 'pc' && groupState('appPool') === 'failed'}
    <p class="bb-note bb-warn" role="status">{T.targetsLoadFailed}</p>
  {/if}
  {#if targetMode === 'pc' && targetsRead && !targetsFailed}
    {#if groupState('appPool') === 'not-found'}<p class="bb-note">{T.targetsPoolsNotFound}</p>{/if}
    {#if groupState('appPool') === 'unavailable'}<p class="bb-note bb-warn">{T.targetsPoolsUnreadable}</p>{/if}
    {#if groupState('service') === 'not-found'}<p class="bb-note">{T.targetsServicesNotFound}</p>{/if}
    {#if groupState('service') === 'unavailable'}<p class="bb-note bb-warn">{T.targetsServicesUnreadable}</p>{/if}
  {/if}
  {#if showLists}
    {#if groupState('appPool') === 'list'}
      <fieldset class="bb-checks">
        <legend>{T.targetsAppPools}</legend>
        {#if allUnknown('appPool')}<p class="bb-note">{T.targetsAllUnknown}</p>{/if}
        <label class="bb-field bb-filter"><span>{T.targetsFilterLabel}</span>
          <input id="{idPrefix}-pools-filter" type="text" bind:value={poolFilter} autocomplete="off" spellcheck="false" aria-describedby={poolFilter.trim() ? idPrefix + '-pools-count' : undefined} />
        </label>
        {#if poolFilter.trim()}<p class="bb-note" id="{idPrefix}-pools-count" role="status">{filterNote('appPool')}</p>{/if}
        <!-- A scroll region must be reachable by keyboard, so it takes tabindex despite being a div. -->
        <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
        <div class="bb-scroll" tabindex="0" role="group" aria-label={T.targetsAppPools}>
          {#each shownTargets('appPool') as t (t.name)}
            <label class="bb-check"><input type="checkbox" checked={isSelected('appPool', t.name)} onchange={() => pick('appPool', t.name)} /><span>{t.name}{#if !allUnknown('appPool')}{@render rowNote(t)}{/if}</span></label>
          {/each}
        </div>
      </fieldset>
    {/if}
    {#if groupState('service') === 'list'}
      <fieldset class="bb-checks">
        <legend>{T.targetsServices}</legend>
        {#if allUnknown('service')}<p class="bb-note">{T.targetsAllUnknown}</p>{/if}
        <label class="bb-field bb-filter"><span>{T.targetsFilterLabel}</span>
          <input id="{idPrefix}-services-filter" type="text" bind:value={serviceFilter} autocomplete="off" spellcheck="false" aria-describedby={serviceFilter.trim() ? idPrefix + '-services-count' : undefined} />
        </label>
        {#if serviceFilter.trim()}<p class="bb-note" id="{idPrefix}-services-count" role="status">{filterNote('service')}</p>{/if}
        <!-- A scroll region must be reachable by keyboard, so it takes tabindex despite being a div. -->
        <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
        <div class="bb-scroll" tabindex="0" role="group" aria-label={T.targetsServices}>
          {#each shownTargets('service') as t (t.name)}
            <label class="bb-check"><input type="checkbox" checked={isSelected('service', t.name)} onchange={() => pick('service', t.name)} /><span>{t.name}{#if !allUnknown('service')}{@render rowNote(t)}{/if}</span></label>
          {/each}
        </div>
      </fieldset>
    {/if}
    {@render targetChips(extraPicked)}
    <p class="bb-note">{T.targetsListNote}</p>
  {:else}
    <p class="bb-note">{T.targetsManualNote}</p>
  {/if}
  <div class="bb-target-add">
    <label class="bb-field"><span>{T.targetsKindLabel}</span>
      <select bind:value={manualKind} onchange={() => { targetRefusal = ''; }}>
        <option value="appPool">{T.targetsKindAppPool}</option>
        <option value="service">{T.targetsKindService}</option>
      </select>
    </label>
    <label class="bb-field"><span>{T.targetsNameLabel}</span>
      <input id="{idPrefix}-f-targets" type="text" bind:value={manualName} autocomplete="off" spellcheck="false" aria-invalid={targetError() ? 'true' : undefined} aria-describedby={targetError() ? idPrefix + '-targets-error' : undefined} oninput={() => { if (targetRefusal) targetRefusal = ''; }} onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addTarget(); } }} />
    </label>
    <button type="button" class="secondary-button" disabled={!manualName.trim()} onclick={addTarget}>{T.targetsAdd}</button>
  </div>
  {#if targetError()}<p class="inline-error" id="{idPrefix}-targets-error" role="alert">{targetError()}</p>{/if}
  {#if !showLists}
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
  .bb-check span { min-width: 0; overflow-wrap: anywhere; }
  /* UX-6: a long detected list scrolls inside a bounded region instead of stretching the page. */
  .bb-scroll { display: grid; gap: 6px; max-height: 240px; overflow: auto; min-width: 0; }
  .bb-filter { max-width: 240px; }
  .bb-target-add { display: flex; flex-wrap: wrap; align-items: end; gap: var(--space-2); }
  .bb-target-add .bb-field { flex: 1 1 160px; max-width: 240px; }
  .bb-chips { display: flex; flex-wrap: wrap; gap: 6px; margin: 0; padding: 0; list-style: none; }
  .bb-chip { display: inline-flex; align-items: center; gap: 6px; padding: 2px 8px; font-size: 12px; border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface); overflow-wrap: anywhere; }
  .bb-chip-x { border: 0; padding: 0 2px; background: none; color: var(--text-2); font-size: 14px; line-height: 1; cursor: pointer; }
  .bb-chip-x:hover { color: var(--text); }
</style>
