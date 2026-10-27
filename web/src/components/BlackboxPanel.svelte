<script lang="ts">
  import { onMount } from 'svelte';
  import {
    blackboxApi, defaultSettings, refusalText, settingsProblem, shownTime,
    type BlackboxItem, type BlackboxList, type Settings, type SnapshotAnswer, type ThisPc, shownBytes
  } from '../lib/blackbox';
  import { BB_REFUSAL, BB_TEXT as T } from '../lib/copy/blackboxCopy';
  import Drawer from './Drawer.svelte';
  import Sticker from './BlackboxSticker.svelte';
  import Wizard from './BlackboxWizard.svelte';
  import AddBlackbox from './BlackboxAdd.svelte';
  import RemoveBlackbox from './BlackboxRemove.svelte';
  import SettingsFields from './BlackboxSettings.svelte';
  import HostCard from './BlackboxHostCard.svelte';

  // The Blackbox page: the list with a sticker per state, the Place wizard, Add by folder, share or address, a status
  // screen per Blackbox with the actions that exist for it, Remove and Snapshot. It is not part of a case.
  // On this PC the actions go through the elevated helper; for another server Unravis shows steps and does nothing there.
  let { onClose }: { onClose: () => void } = $props();

  type View =
    | { kind: 'list' }
    | { kind: 'wizard' }
    | { kind: 'add'; location: string; name: string }
    | { kind: 'status'; id: string }
    | { kind: 'remove'; id: string }
    | { kind: 'apply'; id: string }
    | { kind: 'snapshot'; id: string; answer: SnapshotAnswer };

  let list = $state<BlackboxList | null>(null);
  let loading = $state(true);
  let loadError = $state('');
  let view = $state<View>({ kind: 'list' });
  let scanning = $state(false);
  let scanNote = $state('');
  let working = $state('');
  let actionNote = $state('');
  let actionError = $state('');
  let newKey = $state('');
  let keyOpen = $state(false);
  let applySettings = $state<Settings>(defaultSettings());
  let applyLogs = $state('');
  let applyProblem = $state('');
  let body = $state<HTMLElement>();

  const items = $derived(list?.items ?? []);
  const thisPc = $derived<ThisPc>(list?.thisPc ?? { available: false, scanned: false, installed: false, running: false, listed: false });
  const current = $derived.by(() => {
    if (view.kind === 'list' || view.kind === 'wizard' || view.kind === 'add') return null;
    const id = view.id;
    return items.find((i) => i.id === id) ?? null;
  });

  onMount(() => { void load(); });

  async function load(keepView = true) {
    loading = true;
    loadError = '';
    try {
      list = await blackboxApi.list();
    } catch (error) {
      loadError = refusalText(error, BB_REFUSAL);
    } finally {
      loading = false;
    }
    if (!keepView) view = { kind: 'list' };
  }
  function show(next: View) {
    view = next;
    actionNote = '';
    actionError = '';
    keyOpen = false;
    newKey = '';
    body?.scrollTo?.({ top: 0 });
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
      scanNote = refusalText(error, BB_REFUSAL);
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
      actionError = refusalText(error, BB_REFUSAL);
    } finally {
      working = '';
    }
    await load();
  }
  const check = (item: BlackboxItem) => act(T.checking, async () => { replaceItem(await blackboxApi.check(item.id)); return ''; });
  const start = () => act(T.working, async () => { await blackboxApi.start(); return T.started; });
  const stop = () => act(T.working, async () => { await blackboxApi.stop(); return T.stopped; });
  async function snapshot(item: BlackboxItem) {
    if (working) return;
    working = T.working;
    actionNote = '';
    actionError = '';
    try {
      const answer = await blackboxApi.snapshot();
      show({ kind: 'snapshot', id: item.id, answer });
    } catch (error) {
      actionError = refusalText(error, BB_REFUSAL);
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
      actionError = refusalText(error, BB_REFUSAL);
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
    }
    applyProblem = found ? '' : T.applyUnreadable;
  }
  async function apply(item: BlackboxItem) {
    if (working) return;
    applySettings.logPaths = applyLogs.split('\n').map((s) => s.trim()).filter(Boolean);
    const first = settingsProblem(applySettings);
    if (first) { applyProblem = T.settingProblems[first]; return; }
    applyProblem = '';
    working = T.working;
    try {
      const answer = await blackboxApi.apply(JSON.parse(JSON.stringify(applySettings)) as Settings);
      await load();
      show({ kind: 'status', id: item.id });
      actionNote = answer.restarted ? T.applied : T.appliedNoRestart;
    } catch (error) {
      applyProblem = refusalText(error, BB_REFUSAL);
    } finally {
      working = '';
    }
  }

  const kindWord = (item: BlackboxItem) => (item.kind === 'url' ? T.kindUrl : item.kind === 'share' ? T.kindShare : T.kindFolder);
  const running = (item: BlackboxItem) => item.state.state === 'running' || item.state.state === 'running-late' || item.state.state === 'disk-low' || item.state.state === 'incomplete' || item.state.state === 'clock-differs';
</script>

<Drawer class="glossary-drawer blackbox-drawer" title={T.title} titleId="blackbox-title" intro={T.intro} closeLabel={T.close} {onClose}>
  <div class="bb-body" bind:this={body}>
    {#if view.kind === 'wizard'}
      <Wizard {thisPc} canPack={list?.canPack ?? false} onLeave={() => show({ kind: 'list' })} onPlaced={async () => { await load(); show({ kind: 'list' }); }} />

    {:else if view.kind === 'add'}
      {#key view.location + view.name}
        <AddBlackbox initialLocation={view.kind === 'add' ? view.location : ''} initialName={view.kind === 'add' ? view.name : ''}
          onAdded={async (item) => { await load(); show({ kind: 'status', id: item.id }); actionNote = T.addFound(item.name); }}
          onOpen={(item) => show({ kind: 'status', id: item.id })}
          onCancel={() => show({ kind: 'list' })} />
      {/key}

    {:else if view.kind === 'remove' && current}
      <RemoveBlackbox item={current} {thisPc} onChanged={async () => { await load(); show({ kind: 'list' }); }} onCancel={() => show({ kind: 'status', id: current.id })} />

    {:else if view.kind === 'snapshot'}
      <section class="bb-block" aria-label={T.snapshotTitle(current?.name ?? '')}>
        <h3>{T.snapshotTitle(current?.name ?? '')}</h3>
        <p role="status">{T.snapshotSaved(view.answer.dir, shownBytes(view.answer.bytes))}</p>
        <p class="bb-note">{T.snapshotRecordings(view.answer.recordings)}</p>
        <p class="bb-note">{T.snapshotLogs(view.answer.logs)}</p>
        <p class="bb-note">{T.snapshotNot}</p>
        <div class="bb-actions"><button type="button" class="primary-button" onclick={() => show({ kind: 'status', id: view.kind === 'snapshot' ? view.id : '' })}>{T.done}</button></div>
      </section>

    {:else if view.kind === 'apply' && current}
      <section class="bb-block" aria-label={T.applyTitle}>
        <h3>{T.applyTitle}</h3>
        <p class="bb-note">{T.applyIntro}</p>
        {#if thisPc.settings}
          <SettingsFields bind:settings={applySettings} bind:logPathsText={applyLogs} idPrefix="bb-apply" />
        {/if}
        <div aria-live="polite">
          {#if working}<p class="bb-note" role="status">{working}</p>{/if}
          {#if applyProblem}<div class="inline-error" role="alert">{applyProblem}</div>{/if}
        </div>
        <p class="bb-note">{T.adminNote}</p>
        <div class="bb-actions">
          <button type="button" class="primary-button" disabled={!!working || !thisPc.settings} onclick={() => apply(current)}>{T.apply}</button>
          <button type="button" class="secondary-button" disabled={!!working} onclick={() => show({ kind: 'status', id: current.id })}>{T.cancel}</button>
        </div>
      </section>

    {:else if view.kind === 'status' && current}
      {@const item = current}
      <section class="bb-block" aria-label={item.name}>
        <button type="button" class="quiet-button bb-back" onclick={() => show({ kind: 'list' })}>{T.back}</button>
        <h3>{item.name} <Sticker info={item.state} />{#if item.thisPc}<span class="bb-chip">{T.rowThisPc}</span>{/if}</h3>
        <p class="bb-ready"><strong>{item.state.readiness}</strong></p>
        <p>{item.state.detail}</p>
        <p class="bb-note">{T.stateNext[item.state.state]}</p>
        <dl class="bb-facts">
          <div><dt>{T.rowReachedBy}</dt><dd>{kindWord(item)}: {item.location}</dd></div>
          <div><dt>{T.lastSample}</dt><dd>{shownTime(item.facts?.lastSample)}</dd></div>
          {#if item.facts}
            <div><dt>{T.samplesEvery}</dt><dd>{item.facts.intervalSeconds} {T.seconds}</dd></div>
            <div><dt>{T.files}</dt><dd>{item.facts.files}, {item.facts.samples} {T.samples}</dd></div>
            <div><dt>{T.gaps}</dt><dd>{item.facts.gaps || T.noGaps}</dd></div>
            {#if item.facts.freePercent != null}<div><dt>{T.diskFree}</dt><dd>{item.facts.freePercent} %</dd></div>{/if}
            <div><dt>{T.host}</dt><dd>{item.facts.host}{#if item.facts.version}, {T.version} {item.facts.version}{/if}</dd></div>
          {/if}
        </dl>
        <p class="bb-note">{item.kind === 'url' ? (item.hasKey ? T.readKeySaved : T.readKeyNone) : T.readKeyNone} {item.kind === 'url' ? T.readKeyNote : ''}</p>

        <div aria-live="polite">
          {#if working}<p class="bb-note" role="status">{working}</p>{/if}
          {#if actionNote}<p class="bb-ok" role="status">{actionNote}</p>{/if}
          {#if actionError}<div class="inline-error" role="alert">{actionError}</div>{/if}
        </div>

        {#if item.thisPc}
          <p class="bb-note">{T.adminNote}</p>
          <div class="bb-actions">
            {#if !running(item) && thisPc.installed}<button type="button" class="primary-button" disabled={!!working} onclick={start}>{T.start}</button>{/if}
            {#if running(item)}<button type="button" class="secondary-button" disabled={!!working} onclick={stop}>{T.stop}</button>{/if}
            <button type="button" class="secondary-button" disabled={!!working || !thisPc.settings} onclick={() => openApply(item)}>{T.apply}</button>
            <button type="button" class="secondary-button" disabled={!!working} onclick={() => snapshot(item)}>{T.snapshot}</button>
            <button type="button" class="secondary-button" disabled={!!working} onclick={() => show({ kind: 'remove', id: item.id })}>{T.remove}</button>
            <button type="button" class="quiet-button" disabled={!!working} onclick={() => check(item)}>{T.check}</button>
          </div>
          {#if running(item)}<p class="bb-note">{T.confirmStop}</p>{/if}
        {:else}
          <p class="bb-note">{T.anotherServer}</p>
          <div class="bb-actions">
            <button type="button" class="primary-button" disabled={!!working} onclick={() => check(item)}>{T.check}</button>
            {#if item.kind === 'url'}<button type="button" class="secondary-button" disabled={!!working} onclick={() => { keyOpen = !keyOpen; }} aria-expanded={keyOpen}>{T.changeKey}</button>{/if}
            <button type="button" class="secondary-button" disabled={!!working} onclick={() => show({ kind: 'remove', id: item.id })}>{T.removeForget}</button>
          </div>
          {#if keyOpen}
            <div class="bb-key">
              <label class="bb-field"><span>{T.newKey}</span><input type="password" bind:value={newKey} autocomplete="off" spellcheck="false" /></label>
              <button type="button" class="secondary-button" disabled={!!working || !newKey} onclick={() => saveKey(item)}>{T.saveKey}</button>
            </div>
          {/if}
          <details class="bb-services">
            <summary>{T.servicesTitle}</summary>
            <p class="bb-note">{T.servicesSteps}</p>
          </details>
        {/if}

        {#if item.facts}
          <HostCard {item} />
        {/if}
      </section>

    {:else}
      <div class="bb-nutshell"><span class="bb-label">{T.nutshellLabel}</span> {T.nutshell}</div>
      <div class="bb-actions bb-top">
        <button type="button" class="primary-button" onclick={() => show({ kind: 'wizard' })}>{T.place}</button>
        <button type="button" class="secondary-button" disabled={scanning} onclick={scan}>{T.scanPc}</button>
        <button type="button" class="quiet-button" onclick={() => show({ kind: 'add', location: '', name: '' })}>{T.addExisting}</button>
      </div>
      <p class="bb-note">{T.readOnly}</p>

      <div aria-live="polite">
        {#if scanNote}<p class="bb-note" role="status">{scanNote}</p>{/if}
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
        <div class="bb-actions"><button type="button" class="secondary-button" onclick={() => load()}>{T.retry}</button></div>
      {:else if items.length === 0}
        <section class="bb-empty" aria-label={T.emptyTitle}>
          <h3>{T.emptyTitle}</h3>
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
              <p class="bb-note">{T.lastSample}: {shownTime(item.facts?.lastSample)}{#if item.facts}. {T.samplesEvery} {item.facts.intervalSeconds} {T.seconds}{#if item.facts.freePercent != null}. {T.diskFree} {item.facts.freePercent} %{/if}{/if}</p>
              <div class="bb-actions"><button type="button" class="secondary-button" onclick={() => show({ kind: 'status', id: item.id })} aria-label={T.openStatus + ': ' + item.name}>{T.openStatus}</button></div>
            </li>
          {/each}
        </ul>
      {/if}
    {/if}
  </div>
</Drawer>

<style>
  :global(.connections-drawer.blackbox-drawer) { overflow-x: hidden; }
  .bb-body { display: grid; gap: var(--space-4); align-content: start; min-width: 0; margin-top: var(--space-3); }
  .bb-block { display: grid; gap: var(--space-3); margin: 0; padding: 0; border: 0; min-width: 0; }
  .bb-block h3, .bb-empty h3 { margin: 0; font-size: var(--fs-5); font-weight: 600; overflow-wrap: anywhere; display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .bb-block p, .bb-empty p { margin: 0; font-size: 13px; line-height: 1.5; overflow-wrap: anywhere; }
  .bb-note { color: var(--text-2); font-size: 12px !important; }
  .bb-ready { font-size: 15px !important; }
  .bb-ok { color: var(--tone-ok); }
  .bb-nutshell { padding: var(--space-3); font-size: 13px; line-height: 1.5; background: var(--surface-2); border-radius: var(--radius-sm); }
  .bb-label { font-weight: 600; margin-right: 4px; }
  .bb-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .bb-back { width: fit-content; }
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
  .bb-key { display: grid; gap: var(--space-2); justify-items: start; }
  .bb-field { display: grid; gap: 4px; font-size: 13px; font-weight: 500; }
  .bb-field input { width: 100%; max-width: 320px; padding: 6px 8px; font: inherit; font-weight: 400; color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); box-sizing: border-box; }
  .bb-services summary { width: fit-content; color: var(--text-2); font-size: 13px; }
  .notice p { margin: 0 0 var(--space-2); }
</style>
