<script lang="ts">
  import { timeZone } from '../lib/timeZone.svelte';
  import { formatTime } from '../lib/time';
  import { onMount, tick } from 'svelte';
  import {
    blackboxApi, defaultSettings, fieldProblemNow, needsReload, pcControls, refusalText, settingsChanges, settingsProblem, shownTime,
    type BlackboxItem, type BlackboxList, type Settings, type SettingsProblem, type SnapshotAnswer, type ThisPc, shownBytes
  } from '../lib/blackbox';
  import { BB_REFUSAL, BB_TEXT as T, ageWords, applyChangePart, intervalWords, stateNextFor } from '../lib/copy/blackboxCopy';
  import Drawer from './Drawer.svelte';
  import Sticker from './BlackboxSticker.svelte';
  import PlaceWizard from './BlackboxPlaceWizard.svelte';
  import AddBlackbox from './BlackboxAdd.svelte';
  import RemoveBlackbox from './BlackboxRemove.svelte';
  import SettingsFields from './BlackboxSettings.svelte';
  import HostCard from './BlackboxHostCard.svelte';
  import CaseUse from './BlackboxCaseUse.svelte';
  import ReadKey from './BlackboxReadKey.svelte';
  import Diagnostics from './BlackboxDiagnostics.svelte';
  import BlackboxCharts from './BlackboxCharts.svelte';
  import { BBC_TEXT } from '../lib/copy/blackboxChartsCopy';
  import WizardDialog from './blackboxWizard/WizardDialog.svelte';

  // The Blackbox page: the list with a sticker per state, the Place pop-up, Add by folder, share or address, a status
  // screen per Blackbox with the actions that exist for it, Remove and Snapshot. It is not part of a case.
  // On this PC the actions go through the elevated helper; for another server Unravis shows steps and does nothing there.
  let { onClose }: { onClose: () => void } = $props();

  type View =
    | { kind: 'list' }
    | { kind: 'add'; location: string; name: string }
    | { kind: 'status'; id: string }
    | { kind: 'remove'; id: string }
    | { kind: 'apply'; id: string }
    | { kind: 'snapshot'; id: string; answer: SnapshotAnswer };

  let list = $state<BlackboxList | null>(null);
  let loading = $state(true);
  let loadError = $state('');
  let view = $state<View>({ kind: 'list' });
  // The Place pop-up opens over whatever the drawer shows, so closing it leaves the drawer where it was (OD-9: the drawer wizard is gone).
  let placeOpen = $state(false);
  // The charts pop-up, for one Blackbox. It opens over the drawer and closing it leaves the drawer as it was.
  let chartsFor = $state<BlackboxItem | null>(null);
  let scanning = $state(false);
  let scanNote = $state('');
  let working = $state('');
  let actionNote = $state('');
  let actionError = $state('');
  let stopAsk = $state(false);
  // True when the last failure means this page lost Unravis (it restarted or the tab is stale): a reload is the next step.
  let reloadHint = $state(false);
  let checkedAt = $state('');
  let keepBtn = $state<HTMLButtonElement>();
  let keyInput = $state<HTMLInputElement>();
  let showNewKey = $state(false);
  let newKey = $state('');
  let keyOpen = $state(false);
  let applySettings = $state<Settings>(defaultSettings());
  let applyLogs = $state('');
  // The settings as the service runs them now, to show what Apply would change (UX-16).
  let applyBefore = $state<Settings | null>(null);
  let applyProblem = $state('');
  let applyField = $state<SettingsProblem | null>(null);
  const applyChanges = $derived.by(() => {
    if (!applyBefore) return [];
    return settingsChanges(applyBefore, { ...applySettings, logPaths: applyLogs.split('\n').map((s) => s.trim()).filter(Boolean) });
  });
  const applyFieldShown = $derived(fieldProblemNow(applyField, applySettings, applyLogs));
  let body = $state<HTMLElement>();
  let drawerEl = $state<HTMLElement>();
  // Status, Remove, Apply and Snapshot open in a pop-up over the list (BL-131), with the same shell as Place.
  let pop = $state<WizardDialog>();
  let removeBusy = $state(false);
  const popView = $derived(view.kind === 'status' || view.kind === 'remove' || view.kind === 'apply' || view.kind === 'snapshot');
  const popLabels = {
    close: T.popCloseLabel,
    leaveTitle: T.popLeaveTitle,
    leaveBody: T.popLeaveBody,
    keepGoing: T.popKeepEditing,
    leave: T.popLeave,
    busyNote: T.popBusy
  };

  const items = $derived(list?.items ?? []);
  const thisPc = $derived<ThisPc>(list?.thisPc ?? { available: false, scanned: false, installed: false, running: false, listed: false });
  const current = $derived.by(() => {
    if (view.kind === 'list' || view.kind === 'add') return null;
    const id = view.id;
    return items.find((i) => i.id === id) ?? null;
  });

  onMount(() => { void load(); });

  /** The words for a failure. It also notes whether the cure is to reload the page. */
  function failed(error: unknown): string {
    reloadHint = needsReload(error);
    return refusalText(error, BB_REFUSAL);
  }
  const reload = () => { try { window.location.reload(); } catch { /* nothing more to do here */ } };

  async function load(keepView = true) {
    loading = true;
    loadError = '';
    try {
      list = await blackboxApi.list();
      checkedAt = formatTime(Date.now(), { zone: timeZone.zone, precision: 'minute', date: false });
    } catch (error) {
      loadError = failed(error);
    } finally {
      loading = false;
    }
    if (!keepView) view = { kind: 'list' };
  }
  /**
   * Shows a screen. The drawer is the thing that scrolls, so it goes back to the top, and focus moves to the new screen's
   * heading (or the drawer title on the list), so a keyboard or screen reader user lands on what changed. Opening a status
   * from the list also looks at the list again, in case another tab changed it.
   */
  async function show(next: View) {
    const fromList = view.kind === 'list' || view.kind === 'add';
    const wasPop = popView;
    // Leaving the pop-up from any of its screens goes back to the row it was opened from (D-C-22).
    const leavingStatus = view.kind !== 'list' && view.kind !== 'add' ? view.id : '';
    view = next;
    actionNote = '';
    actionError = '';
    keyOpen = false;
    stopAsk = false;
    reloadHint = false;
    showNewKey = false;
    newKey = '';
    await tick();
    // The drawer behind the pop-up keeps its scroll, so closing the pop-up lands back on the row that opened it.
    if (!popView && !wasPop) drawerEl?.scrollTo?.({ top: 0 });
    // A screen inside the pop-up: the pop-up puts focus on its own title (or first field) and scrolls itself to the top.
    // Opening it does that by itself; moving between its screens asks for it here.
    if (popView) {
      if (wasPop) pop?.focusStep();
      if (fromList && next.kind === 'status') void refreshOpened(next.id);
      return;
    }
    // Back from a status: return to the row that was opened (D-C-22). If it is gone, use the usual target.
    const row = next.kind === 'list' && leavingStatus
      ? Array.from(body?.querySelectorAll<HTMLElement>('[data-open-for]') ?? []).find((el) => el.dataset.openFor === leavingStatus)
      : undefined;
    const target = row ?? body?.querySelector<HTMLElement>('h3[tabindex="-1"]') ?? drawerEl?.querySelector<HTMLElement>('h2');
    target?.focus({ preventScroll: true });
  }
  /** Closing the pop-up goes back to the list, read again, since a remove or an apply may have changed it. */
  async function closePop() {
    await show({ kind: 'list' });
    void load();
  }
  /** Reloads the list after a status opened. If that Blackbox was removed elsewhere, say so and show the list. */
  async function refreshOpened(id: string) {
    await load();
    if (view.kind === 'status' && view.id === id && !items.some((i) => i.id === id)) {
      await show({ kind: 'list' });
      scanNote = T.goneFromList;
    }
  }
  async function scan() {
    if (scanning) return;
    scanning = true;
    scanNote = T.scanning;
    try {
      const found = await blackboxApi.thisPc();
      if (list) list = { ...list, thisPc: found };
      scanNote = found.problem ? T.scanFailed : '';
      if (!found.problem && found.scanned && !found.installed) scanNote = T.thisPcNote;
    } catch (error) {
      scanNote = failed(error);
    } finally {
      scanning = false;
    }
  }
  function replaceItem(updated: BlackboxItem) {
    if (!list) return;
    list = { ...list, items: list.items.map((i) => (i.id === updated.id ? updated : i)) };
  }

  /** Runs one action on this PC or on a saved place, keeps its words, and refreshes the list. */
  async function act(doing: string, run: () => Promise<string>) {
    if (working) return;
    working = doing;
    actionNote = '';
    actionError = '';
    try {
      actionNote = await run();
    } catch (error) {
      actionError = failed(error);
    } finally {
      working = '';
    }
    await load();
  }
  const check = (item: BlackboxItem) => act(T.checking, async () => { replaceItem(await blackboxApi.check(item.id)); return ''; });
  const start = () => act(T.workingStart, async () => { await blackboxApi.start(); return T.started; });
  const update = () => act(T.workingUpdate, async () => {
    const done = await blackboxApi.updateThisPc();
    return !done.updated ? T.updateCurrent : done.restarted ? T.updatedRestarted : T.updatedStopped;
  });
  const stop = () => { stopAsk = false; return act(T.workingStop, async () => { await blackboxApi.stop(); return T.stopped; }); };
  async function openKey() {
    keyOpen = !keyOpen;
    if (!keyOpen) return;
    await tick();
    keyInput?.focus();
  }
  async function askStop() {
    stopAsk = true;
    await tick();
    keepBtn?.focus();
  }
  async function snapshot(item: BlackboxItem) {
    if (working) return;
    working = T.workingSnapshot;
    actionNote = '';
    actionError = '';
    try {
      const answer = await blackboxApi.snapshot();
      show({ kind: 'snapshot', id: item.id, answer });
    } catch (error) {
      actionError = failed(error);
    } finally {
      working = '';
    }
  }
  async function saveKey(item: BlackboxItem) {
    if (working || !newKey) return;
    working = T.checking;
    actionNote = '';
    actionError = '';
    try {
      replaceItem(await blackboxApi.setKey(item.id, newKey));
      newKey = '';
      keyOpen = false;
      actionNote = T.keySaved;
    } catch (error) {
      actionError = failed(error);
    } finally {
      working = '';
    }
  }
  function openApply(item: BlackboxItem) {
    const found = thisPc.settings;
    show({ kind: 'apply', id: item.id });
    if (found) {
      applySettings = JSON.parse(JSON.stringify(found)) as Settings;
      applyLogs = (found.logPaths ?? []).join('\n');
      applyBefore = JSON.parse(JSON.stringify(found)) as Settings;
    }
    applyField = null;
    applyProblem = found ? '' : T.applyUnreadable;
  }
  async function apply(item: BlackboxItem) {
    if (working) return;
    applyField = null;
    applySettings.logPaths = applyLogs.split('\n').map((s) => s.trim()).filter(Boolean);
    const first = settingsProblem(applySettings);
    if (first === 'name') { applyProblem = T.settingProblems.name; return; }
    if (first) {
      applyProblem = '';
      applyField = first;
      await tick();
      body?.querySelector<HTMLElement>('#bb-apply-f-' + first)?.focus();
      return;
    }
    applyProblem = '';
    working = T.working;
    try {
      const answer = await blackboxApi.apply(JSON.parse(JSON.stringify(applySettings)) as Settings);
      await load();
      show({ kind: 'status', id: item.id });
      // BL-139: the recorder applied, but the SQL service's settings lock did not land.
      actionNote = (answer.restarted ? T.applied : T.appliedNoRestart) + (answer.sqlWarning ? ' ' + answer.sqlWarning : '');
    } catch (error) {
      applyProblem = failed(error);
    } finally {
      working = '';
    }
  }

  const kindWord = (item: BlackboxItem) => (item.kind === 'url' ? T.kindUrl : item.kind === 'share' ? T.kindShare : T.kindFolder);
  const intervalFor = (f: NonNullable<BlackboxItem['facts']>) => intervalWords(f.intervalSeconds, f.effectiveIntervalSeconds);
  // How old the last sample is, then the exact time: "2 minutes ago (2026-11-17 09:05 UTC)".
  const sampleLine = (item: BlackboxItem) => {
    const at = item.facts?.lastSample;
    const age = ageWords(at);
    return age ? age + ' (' + shownTime(at, timeZone.zone) + ')' : shownTime(at, timeZone.zone);
  };
  // A this-PC address with a refused or missing key can be repaired here. Another server's can always be.
  const canChangeKey = (item: BlackboxItem) => item.kind === 'url' && (!item.thisPc || !item.hasKey || item.state.reason === 'key');
  const keyLine = (item: BlackboxItem) => {
    if (item.kind !== 'url') return T.readKeyNone;
    return (item.hasKey ? T.readKeySaved : T.readKeyMissing) + ' ' + (item.thisPc ? T.readKeyNoteThisPc : T.readKeyNote);
  };
</script>

<Drawer class="glossary-drawer blackbox-drawer" focusOnOpen bind:element={drawerEl} title={T.title} titleId="blackbox-title" intro={T.intro} closeLabel={T.close} {onClose}>
  <div class="bb-body" bind:this={body}>
    {#if view.kind === 'add'}
      {#key view.location + view.name}
        <AddBlackbox initialLocation={view.kind === 'add' ? view.location : ''} initialName={view.kind === 'add' ? view.name : ''}
          onAdded={async (item) => { await load(); show({ kind: 'status', id: item.id }); actionNote = T.addFound(item.name); }}
          onOpen={(item) => show({ kind: 'status', id: item.id })}
          onCancel={() => show({ kind: 'list' })} />
      {/key}

    {:else}
      <div class="bb-nutshell"><span class="bb-label">{T.nutshellLabel}</span> {T.nutshell}</div>
      <div class="bb-actions bb-top">
        <button type="button" class="primary-button" onclick={() => { placeOpen = true; }}>{T.place}</button>
        <button type="button" class="secondary-button" disabled={scanning} onclick={scan}>{T.scanPc}</button>
        <button type="button" class="quiet-button" onclick={() => show({ kind: 'add', location: '', name: '' })}>{T.addExisting}</button>
      </div>
      <p class="bb-note">{T.readOnly}</p>

      <div>
        {#if scanNote}<p class="bb-note" role="status">{scanNote}</p>{/if}
        {#if scanNote && reloadHint}<button type="button" class="secondary-button" onclick={reload}>{T.reload}</button>{/if}
        {#if thisPc.installed && !thisPc.listed}
          <div class="notice" role="status">
            <p>{thisPc.configUnreadable ? T.thisPcUnreadable : T.thisPcFound}</p>
            {#if thisPc.folder}<button type="button" class="secondary-button" onclick={() => show({ kind: 'add', location: thisPc.folder ?? '', name: thisPc.name ?? '' })}>{T.thisPcFoundAdd}</button>{/if}
          </div>
        {/if}
      </div>

      {#if loading && !list}
        <p class="bb-note" role="status">{T.loading}</p>
      {:else if loadError}
        <div class="inline-error" role="alert">{T.loadFailed} {loadError}</div>
        <div class="bb-actions">
          {#if reloadHint}<button type="button" class="primary-button" onclick={reload}>{T.reload}</button>{/if}
          <button type="button" class="secondary-button" onclick={() => load()}>{T.retry}</button>
        </div>
      {:else if items.length === 0}
        <section class="bb-empty" aria-label={T.emptyTitle}>
          <h3 tabindex="-1">{T.emptyTitle}</h3>
          <p>{T.emptyText}</p>
        </section>
      {:else}
        <ul class="bb-list" aria-label={T.title}>
          {#each items as item (item.id)}
            <li class="bb-row">
              <div class="bb-row-head">
                <strong>{item.name}</strong>
                <Sticker info={item.state} />
                {#if item.thisPc}<span class="bb-chip">{T.rowThisPc}</span>{/if}
              </div>
              <p class="bb-note">{kindWord(item)}: {item.location}</p>
              {#if item.facts}
                <p class="bb-note">{T.lastSample}: {sampleLine(item)}</p>
                <p class="bb-note">{intervalFor(item.facts)}{#if item.facts.freePercent != null}. {T.diskFree}: {item.facts.freePercent}%{/if}</p>
              {:else}
                <p class="bb-note">{T.noRecordingYet}</p>
              {/if}
              <div class="bb-actions"><button type="button" class="secondary-button" data-open-for={item.id} onclick={() => show({ kind: 'status', id: item.id })} aria-label={T.openStatus + ': ' + item.name}>{T.openStatus}</button></div>
            </li>
          {/each}
        </ul>
      {/if}
    {/if}

    <!-- Status, Remove, Apply and Snapshot (BL-131): a pop-up over the list, in the Place pop-up's shell. Closing it goes back to the list. -->
    <WizardDialog
      bind:this={pop}
      open={popView && current !== null}
      titleId="bb-pop-title"
      busy={!!working || removeBusy}
      finished={!(view.kind === 'apply' && applyChanges.length > 0)}
      enterNeedsButton
      labels={popLabels}
      onLeave={closePop}
      onPrimary={() => {}}
    >
      {#snippet rail()}{/snippet}
      {#snippet header()}
        {#if current}
          <h2 id="bb-pop-title" class="bb-pop-title" tabindex="-1">{current.name} <Sticker info={current.state} />{#if current.thisPc}<span class="bb-chip">{T.rowThisPc}</span>{/if}</h2>
        {/if}
      {/snippet}
      {#snippet body()}
            {#if view.kind === 'remove' && current}
          <RemoveBlackbox item={current} {thisPc} onBusy={(b) => { removeBusy = b; }} onChanged={async () => { await load(); show({ kind: 'list' }); }} onCancel={() => show({ kind: 'status', id: current.id })} />

        {:else if view.kind === 'snapshot'}
          <section class="bb-block" aria-label={T.snapshotTitle(current?.name ?? '')}>
            <h3 tabindex="-1">{T.snapshotTitle(current?.name ?? '')}</h3>
            <p role="status">{T.snapshotSaved(view.answer.dir, shownBytes(view.answer.bytes))}</p>
            <p class="bb-note">{T.snapshotRecordings(view.answer.recordings)}</p>
            <p class="bb-note">{T.snapshotLogs(view.answer.logs)}</p>
            <p class="bb-note">{T.snapshotNot}</p>
            <div class="bb-actions"><button type="button" class="primary-button" onclick={() => show({ kind: 'status', id: view.kind === 'snapshot' ? view.id : '' })}>{T.done}</button></div>
          </section>

        {:else if view.kind === 'apply' && current}
          <section class="bb-block" aria-label={T.applyTitle}>
            <h3 tabindex="-1">{T.applyTitle}</h3>
            <p class="bb-note">{T.applyIntro}</p>
            {#if thisPc.settings}
              <SettingsFields bind:settings={applySettings} bind:logPathsText={applyLogs} idPrefix="bb-apply" problem={applyFieldShown} lockReached />
            {/if}
            <div>
              {#if working}<p class="bb-note" role="status">{working}</p>{/if}
              {#if applyProblem}<div class="inline-error" role="alert">{applyProblem}</div>{#if reloadHint}<button type="button" class="secondary-button" onclick={reload}>{T.reload}</button>{/if}{/if}
            </div>
            {#if thisPc.settings}
              <p class="bb-note" id="bb-apply-diff">{applyChanges.length ? T.applyChangedLine(applyChanges.map(applyChangePart)) : T.applyNoChange}</p>
              {#if applyChanges.some((c) => c.field === 'folder')}<p class="bb-note bb-warn">{T.applyFolderWarn}</p>{/if}
              {#if applyChanges.some((c) => c.field === 'interval')}<p class="bb-note">{T.applyIntervalWarn}</p>{/if}
            {/if}
            <p class="bb-note">{T.adminNote}</p>
            <div class="bb-actions">
              <button type="button" class="primary-button" disabled={!!working || !thisPc.settings} onclick={() => apply(current)}>{T.apply}</button>
              <button type="button" class="secondary-button" disabled={!!working} onclick={() => show({ kind: 'status', id: current.id })}>{T.cancel}</button>
            </div>
          </section>

        {:else if view.kind === 'status' && current}
          {@const item = current}
          <section class="bb-block" aria-labelledby="bb-pop-title">
            <p class="bb-ready"><strong>{item.state.readiness}</strong></p>
            <p>{item.state.detail}</p>
            <p class="bb-note">{stateNextFor(item.state, item.thisPc)}</p>
            {#if checkedAt}<p class="bb-note">{T.checkedAt} {checkedAt}.</p>{/if}
            {#if !item.thisPc}<p class="bb-note">{T.statusReadOnly}</p>{/if}
            <dl class="bb-facts">
              <div><dt>{T.rowReachedBy}</dt><dd>{kindWord(item)}: {item.location}</dd></div>
              {#if item.facts}
                <div><dt>{T.lastSample}</dt><dd>{sampleLine(item)}</dd></div>
                <div><dt>{T.samplesEvery}</dt><dd>{intervalFor(item.facts)}</dd></div>
                <div><dt>{T.recorded}</dt><dd>{T.filesAndSamples(item.facts.files, item.facts.samples)}</dd></div>
                <div><dt>{T.gaps}</dt><dd>{T.gapsText(item.facts.gaps, item.facts.unread ?? 0, item.facts.longestGapSeconds ?? 0)}</dd></div>
                {#if item.facts.freePercent != null}<div><dt>{T.diskFree}</dt><dd>{item.facts.freePercent}%</dd></div>{/if}
                {#if item.facts.update}<div><dt>{T.updates}</dt><dd>{T.updateLine(item.facts.update)}{#if item.facts.update.result} {item.facts.update.result}{/if}</dd></div>{/if}
                <div><dt>{T.host}</dt><dd>{item.facts.host}{#if item.facts.version}, {T.version} {item.facts.version}{/if}</dd></div>
              {:else}
                <div><dt>{T.lastSample}</dt><dd>{T.noRecordingYet}</dd></div>
              {/if}
            </dl>
            {#if item.facts && item.facts.effectiveIntervalSeconds && item.facts.effectiveIntervalSeconds !== item.facts.intervalSeconds}<p class="bb-note">{T.intervalHelp}</p>{/if}
            <p class="bb-note">{keyLine(item)}</p>

            <div>
              {#if working}<p class="bb-note" role="status">{working}</p>{/if}
              {#if actionNote}<p class="bb-ok" role="status">{actionNote}</p>{/if}
              {#if actionError}<div class="inline-error" role="alert">{actionError}</div>{/if}
            </div>
            {#if actionError && reloadHint}<div class="bb-actions"><button type="button" class="secondary-button" onclick={reload}>{T.reload}</button></div>{/if}

            {#if item.thisPc && item.state.state === 'not-placed'}
              <div class="bb-actions">
                <button type="button" class="primary-button" disabled={!!working} onclick={() => { placeOpen = true; }}>{T.place}</button>
                <button type="button" class="secondary-button" disabled={!!working} onclick={() => show({ kind: 'remove', id: item.id })}>{T.removeForget}</button>
              </div>
            {:else if item.thisPc}
              <p class="bb-note">{T.adminNote}</p>
              {#if thisPc.installed && thisPc.updateAvailable}
                <div class="bb-confirm" role="group" aria-label={T.updateTitle}>
                  <strong>{T.updateTitle}</strong>
                  <p class="bb-note">{T.updateText}</p>
                  <div class="bb-actions"><button type="button" class="primary-button" disabled={!!working} onclick={update}>{T.updateButton}</button></div>
                </div>
              {/if}
              {@const controls = pcControls(item.state.state, thisPc)}
              <div class="bb-actions">
                {#if controls.start}<button type="button" class="primary-button" disabled={!!working} onclick={start}>{T.start}</button>{/if}
                {#if controls.stop}<button type="button" class="secondary-button" disabled={!!working} onclick={askStop} aria-expanded={stopAsk}>{T.stop}</button>{/if}
                {#if canChangeKey(item)}<button type="button" class="secondary-button" disabled={!!working} onclick={openKey} aria-expanded={keyOpen}>{T.changeKey}</button>{/if}
                <button type="button" class="secondary-button" disabled={!!working || !thisPc.settings} onclick={() => openApply(item)}>{T.apply}</button>
                <button type="button" class="secondary-button" disabled={!!working} onclick={() => snapshot(item)}>{T.snapshot}</button>
                <button type="button" class="secondary-button" disabled={!!working} onclick={() => show({ kind: 'remove', id: item.id })}>{T.remove}</button>
                <button type="button" class="quiet-button" disabled={!!working} onclick={() => check(item)}>{T.check}</button>
              </div>
              {#if stopAsk && controls.stop}
                <div class="bb-confirm" role="group" aria-label={T.stopTitle}>
                  <strong>{T.stopTitle}</strong>
                  <p class="bb-note">{T.confirmStop}</p>
                  <div class="bb-actions">
                    <button type="button" class="secondary-button" disabled={!!working} onclick={stop}>{T.stopYes}</button>
                    <button type="button" class="primary-button" bind:this={keepBtn} onclick={() => { stopAsk = false; }}>{T.stopKeep}</button>
                  </div>
                </div>
              {/if}
            {:else}
              <p class="bb-note">{T.anotherServer}</p>
              <div class="bb-actions">
                <button type="button" class="primary-button" disabled={!!working} onclick={() => check(item)}>{T.check}</button>
                {#if canChangeKey(item)}<button type="button" class="secondary-button" disabled={!!working} onclick={openKey} aria-expanded={keyOpen}>{T.changeKey}</button>{/if}
                <button type="button" class="secondary-button" disabled={!!working} onclick={() => show({ kind: 'remove', id: item.id })}>{T.removeForget}</button>
              </div>
              <details class="bb-services">
                <summary>{T.servicesTitle}</summary>
                <p class="bb-note">{item.state.state === 'running-late' ? T.servicesStepsLate : T.servicesSteps}</p>
              </details>
            {/if}

            {#if keyOpen && canChangeKey(item)}
              <div class="bb-key">
                <label class="bb-field"><span>{T.newKey}</span>
                  <input id="bb-new-key" bind:this={keyInput} type={showNewKey ? 'text' : 'password'} bind:value={newKey} autocomplete="off" spellcheck="false" aria-describedby="bb-new-key-help"
                    onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); void saveKey(item); } }} />
                </label>
                <label class="bb-check"><input type="checkbox" bind:checked={showNewKey} /><span>{T.addKeyShow}</span></label>
                <p class="bb-note" id="bb-new-key-help">{T.keyFieldHelp}</p>
                <button type="button" class="secondary-button" disabled={!!working || !newKey} onclick={() => saveKey(item)}>{T.saveKey}</button>
              </div>
            {/if}

            {#if item.thisPc && item.kind === 'url' && item.hasKey}{#key item.id}<ReadKey {item} />{/key}{/if}
            {#key item.id}<CaseUse {item} />{/key}

            {#if item.facts}
              {#if item.facts.lastSample}<div class="bb-confirm">
                <p class="bb-note">{BBC_TEXT.openHint}</p>
                <div class="bb-actions"><button type="button" class="secondary-button" onclick={() => { chartsFor = item; }}>{BBC_TEXT.open}</button></div>
              </div>{/if}
              <HostCard {item} />
            {/if}
            {#key item.id}<Diagnostics {item} />{/key}
          </section>
            {/if}
      {/snippet}
      {#snippet footer()}
        <button type="button" class="secondary-button" disabled={!!working || removeBusy} onclick={() => pop?.requestLeave()}>{T.popClose}</button>
      {/snippet}
    </WizardDialog>

    <!-- The pop-up closes itself back to the button that opened it. After a real place the list reloads and shows. -->
    {#if placeOpen}
      <PlaceWizard {thisPc} canPack={list?.canPack ?? false} open={placeOpen}
        onLeave={() => { placeOpen = false; }}
        onPlaced={async () => { await load(); placeOpen = false; await show({ kind: 'list' }); }} />
    {/if}
  </div>
</Drawer>
{#if chartsFor}{#key chartsFor.id}<BlackboxCharts item={chartsFor} onClose={() => { chartsFor = null; }} />{/key}{/if}

<style>
  .bb-body { display: grid; gap: var(--space-4); align-content: start; min-width: 0; margin-top: var(--space-3); }
  .bb-block { display: grid; gap: var(--space-3); margin: 0; padding: 0; border: 0; min-width: 0; }
  .bb-block h3, .bb-empty h3 { margin: 0; font-size: var(--fs-5); font-weight: 600; overflow-wrap: anywhere; display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .bb-block p, .bb-empty p { margin: 0; font-size: 13px; line-height: 1.5; overflow-wrap: anywhere; }
  .bb-block p.bb-note { font-size: 12px; }
  .bb-block p.bb-ready { font-size: 15px; }
  .bb-warn { color: var(--text); border-left: 3px solid var(--warn); padding-left: var(--space-2); }
  .bb-nutshell { padding: var(--space-3); font-size: 13px; line-height: 1.5; background: var(--surface-2); border-radius: var(--radius-sm); }
  .bb-label { font-weight: 600; margin-right: 4px; }
  .bb-pop-title { margin: 0; padding-right: 40px; font-size: var(--fs-6); font-weight: 600; line-height: 1.3; overflow-wrap: anywhere; display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .bb-empty { display: grid; gap: var(--space-2); padding: var(--space-4) 0; border-top: 1px solid var(--line); }
  .bb-list { display: grid; margin: 0; padding: 0; list-style: none; }
  .bb-row { display: grid; gap: var(--space-2); padding: var(--space-3) 0; border-top: 1px solid var(--line); min-width: 0; }
  .bb-row p { margin: 0; overflow-wrap: anywhere; }
  .bb-row-head { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); font-size: 14px; }
  .bb-chip { padding: 1px 8px; color: var(--text-2); background: var(--surface-2); border-radius: 999px; font-size: 12px; font-weight: 500; }
  .bb-facts { display: grid; gap: 4px; margin: 0; font-size: 13px; }
  .bb-facts div { display: flex; gap: 10px; min-width: 0; }
  .bb-facts dt { flex: 0 0 100px; color: var(--text-2); }
  .bb-facts dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
  .bb-confirm { display: grid; gap: var(--space-2); padding: var(--space-3); border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface-2); }
  .bb-check { display: flex; align-items: center; gap: 8px; font-size: 13px; }
  .bb-key { display: grid; gap: var(--space-2); justify-items: start; }
  .bb-field { display: grid; gap: 4px; font-size: 13px; font-weight: 500; }
  .bb-field input { width: 100%; max-width: 320px; padding: 6px 8px; font: inherit; font-weight: 400; color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); box-sizing: border-box; }
  .bb-services summary { width: fit-content; color: var(--text-2); font-size: 13px; }
  .notice p { margin: 0 0 var(--space-2); }
</style>
