<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { wizard } from '../../lib/wizard.svelte';
  import {
    addFile, cancelScan, clearInfo, isChosen, loadEntry, reconcileEnvironments, scanIsStale, sourcesView, startScan, tickFinding
  } from '../../lib/wizardSources.svelte';
  import { FILE_KINDS, galleryGroups, sourceKey } from '../../lib/wizardSources';
  import type { FileKind, GalleryEntry } from '../../lib/wizardSources';
  import { GROUPS, ageMinutes, cancelAllowed, effectiveCost } from '../../lib/discover';
  import { DISCOVER_TEXT, detectorLine, foundText, gapReasonText, hostNameText, howText, rowMetaText, showsText } from '../../lib/copy/discoverCopy';
  import { FILE_KIND_OPTIONS, SOURCES_TEXT, addFindingLabel, adminNote, locateOkText, notReadableText, yourSourcesText } from '../../lib/copy/wizardSourcesCopy';
  import HelpNote from '../HelpNote.svelte';
  import LocateFolder from './LocateFolder.svelte';
  import MongoClassify from './MongoClassify.svelte';
  import ReachableCheck from './ReachableCheck.svelte';
  import SqlServerForm from './SqlServerForm.svelte';
  import SourceTagRow from './SourceTagRow.svelte';

  // The sources step of the create wizard (TSK-18). Three parts: what the scan of this computer found, ways to reach another computer, and the
  // list of chosen sources with their Environment, Portal and API names. Only a source the scan could read can be added (a tick box); the rest
  // are shown greyed with the reason and a way to point at the right place. The scan and the connections live in lib/wizardSources.svelte.ts so
  // they survive Back and Next.
  const uid = $props.id();
  const FILE_IDS = new Set<string>(FILE_KINDS);

  let openForm = $state<'' | 'mongo' | 'sql' | 'redis' | 'files'>('');
  let fileKind = $state<FileKind>('iis-logs');
  let fileLabel = $state('');
  let fileLine = $state('');
  let fileFailed = $state(false);
  let statusNote = $state('');
  let scanClickedAt = $state(0);
  let listHeading = $state<HTMLElement>();

  const groups = $derived(galleryGroups(sourcesView.findings, GROUPS));
  const offered = $derived(groups.reduce((n, g) => n + g.entries.length, 0));
  const running = $derived(sourcesView.phase === 'scanning' || sourcesView.phase === 'checking');
  const stale = $derived(sourcesView.phase === 'done' && scanIsStale());
  const entryKind = $derived(sourcesView.entry.kind);

  onMount(() => {
    reconcileEnvironments();
    void loadEntry();
  });

  async function onScan() {
    scanClickedAt = Date.now();
    statusNote = '';
    await startScan();
  }

  async function onCancel() {
    // The cancel button takes the place of the scan button, so a quick double click must not cancel by accident.
    if (!cancelAllowed(scanClickedAt, Date.now())) return;
    await cancelScan();
  }

  function onTick(event: Event & { currentTarget: HTMLInputElement }, id: string) {
    const input = event.currentTarget;
    const want = input.checked;
    statusNote = '';
    tickFinding(id, want);
    const now = isChosen(sourceKey({ kind: 'discover', ref: id }));
    if (now !== want) { input.checked = now; if (want) statusNote = SOURCES_TEXT.limitFull; }
  }

  async function afterFound(id: string, files: number) {
    statusNote = locateOkText(files);
    await tick();
    document.getElementById(uid + '-f-' + id)?.focus();
  }

  function submitFile() {
    fileLine = '';
    fileFailed = false;
    if (!FILE_IDS.has(fileKind)) return;
    const result = addFile(fileKind, fileLabel);
    if (!result.ok) {
      fileFailed = true;
      fileLine = result.problem === 'full' ? SOURCES_TEXT.limitFull : SOURCES_TEXT.alreadyChosen;
      return;
    }
    fileLabel = '';
    fileLine = SOURCES_TEXT.fileAdded;
  }

  function onFileKey(event: KeyboardEvent) {
    if (event.key === 'Enter' && !event.isComposing) { event.preventDefault(); event.stopPropagation(); submitFile(); }
  }

  async function afterRemoved() {
    await tick();
    listHeading?.focus();
  }

  const toggleForm = (name: typeof openForm) => { openForm = openForm === name ? '' : name; };
  const metaOf = (entry: GalleryEntry): string => rowMetaText(entry.finding, ageMinutes(entry.finding.newest, Date.now()));
</script>

<div class="gallery">
  <p class="lead">{SOURCES_TEXT.intro}</p>
  <HelpNote id="wizard-sources" />

  <div class="notices">
    <p class="notice" role="status" hidden={sourcesView.info === ''}>{sourcesView.info}{#if sourcesView.info !== ''} <button type="button" class="link-button" onclick={clearInfo}>{SOURCES_TEXT.close}</button>{/if}</p>
    <p class="inline-error" role="alert" hidden={sourcesView.problem === ''}>{sourcesView.problem}</p>
    <p class="notice" role="status" hidden={statusNote === ''}>{statusNote}</p>
  </div>

  <!-- Part 1: this computer -->
  {#if entryKind !== 'hidden'}
    <section class="part" aria-labelledby="{uid}-pc">
      <h4 id="{uid}-pc">{SOURCES_TEXT.thisPcHeading}</h4>
      <p class="hint">{SOURCES_TEXT.thisPcNote}</p>

      {#if entryKind === 'loading'}
        <p class="hint" role="status">{SOURCES_TEXT.scanLoading}</p>
      {:else if entryKind === 'unsupported'}
        <p class="hint">{SOURCES_TEXT.noScanSupport}</p>
      {:else if entryKind === 'unavailable'}
        <p class="hint">{DISCOVER_TEXT.unavailable}</p>
        <div class="actions"><button type="button" class="secondary-button" onclick={() => void loadEntry(undefined, true)}>{DISCOVER_TEXT.retry}</button></div>
      {:else}
        <div class="actions">
          {#if running}
            <button type="button" class="secondary-button" disabled={sourcesView.cancelling} onclick={() => void onCancel()}>{sourcesView.cancelling ? SOURCES_TEXT.scanCancelling : SOURCES_TEXT.scanCancel}</button>
          {:else}
            <button type="button" class="primary-button" onclick={() => void onScan()}>{sourcesView.phase === 'done' ? SOURCES_TEXT.scanAgain : SOURCES_TEXT.scanStart}</button>
          {/if}
        </div>
        {#if running}
          <p class="hint" role="status">{SOURCES_TEXT.scanBusy}</p>
          {#if sourcesView.progress.length > 0}
            <ul class="progress" aria-label={DISCOVER_TEXT.scanning}>
              {#each sourcesView.progress as step (step.detector)}<li>{detectorLine(step.detector, step.state)}</li>{/each}
            </ul>
          {/if}
        {:else if sourcesView.phase === 'done'}
          {#if stale}<p class="notice" role="status">{SOURCES_TEXT.locateExpired}</p>{/if}
          <p class="hint" role="status">{foundText(offered, sourcesView.gaps.length)}{sourcesView.host ? ' ' + hostNameText(sourcesView.host.label) : ''}</p>
          {#if offered === 0}<p class="hint">{SOURCES_TEXT.scanNothing}</p>{/if}

          {#each groups as group (group.id)}
            <section class="group" aria-labelledby="{uid}-g-{group.id}">
              <h5 id="{uid}-g-{group.id}">{DISCOVER_TEXT.groups[group.id]}</h5>
              <ul class="rows" aria-label="{SOURCES_TEXT.listLabel}: {DISCOVER_TEXT.groups[group.id]}">
                {#each group.entries as entry (entry.finding.id)}
                  {@const f = entry.finding}
                  {@const rowId = uid + '-f-' + f.id}
                  <li class="found" class:muted={entry.state !== 'ready'}>
                    <div class="pick">
                      {#if entry.state === 'steps'}
                        <span class="nobox" aria-hidden="true"></span>
                      {:else}
                        <input id={rowId} type="checkbox" checked={isChosen(sourceKey({ kind: 'discover', ref: f.id }))} disabled={entry.state !== 'ready'}
                          aria-describedby="{rowId}-d" onchange={(event) => onTick(event, f.id)} />
                      {/if}
                      <div class="body">
                        {#if entry.state === 'steps'}
                          <span class="name">{f.display}</span>
                        {:else}
                          <label class="name" for={rowId}>{addFindingLabel(f.display)}</label>
                        {/if}
                        <ul class="chips" aria-label="{f.display}">
                          <li class="chip">{entry.state === 'ready' ? SOURCES_TEXT.chipReady : entry.state === 'unverified' ? SOURCES_TEXT.chipNotChecked : SOURCES_TEXT.chipSteps}</li>
                          {#if f.needsAdmin}<li class="chip">{SOURCES_TEXT.chipAdmin}</li>{/if}
                          {#if effectiveCost(f) === 'large'}<li class="chip">{SOURCES_TEXT.chipLarge}</li>{/if}
                        </ul>
                        <p class="hint" id="{rowId}-d">
                          {#if entry.state === 'unverified'}{SOURCES_TEXT.unverifiedLead} {notReadableText(f.unreadable)}
                          {:else if entry.state === 'steps'}{SOURCES_TEXT.stepsLead} {howText(f.how)}
                          {:else}{showsText(f.shows, f.importer)} {metaOf(entry)}{f.needsAdmin ? ' ' + adminNote : ''}{/if}
                        </p>
                        {#if entry.state === 'unverified'}
                          <LocateFolder findingId={f.id} label={f.display} onFound={(files) => void afterFound(f.id, files)} />
                        {/if}
                      </div>
                    </div>
                  </li>
                {/each}
              </ul>
            </section>
          {/each}

          {#if sourcesView.gaps.length > 0}
            <section class="group" aria-labelledby="{uid}-gaps">
              <h5 id="{uid}-gaps">{SOURCES_TEXT.gapsHeading}</h5>
              <ul class="gaps">
                {#each sourcesView.gaps as gap, i (i)}<li><span class="name">{gap.display}</span> <span class="hint">{gapReasonText(gap.reason)}</span></li>{/each}
              </ul>
            </section>
          {/if}
        {/if}
      {/if}
    </section>
  {/if}

  <!-- Part 2: another computer -->
  <section class="part" aria-labelledby="{uid}-other">
    <h4 id="{uid}-other">{SOURCES_TEXT.otherHeading}</h4>
    <p class="hint">{SOURCES_TEXT.otherHelp}</p>
    <div class="actions">
      <button type="button" class="secondary-button" aria-expanded={openForm === 'mongo'} aria-controls="{uid}-mongo" onclick={() => toggleForm('mongo')}>{SOURCES_TEXT.addMongo}</button>
      <button type="button" class="secondary-button" aria-expanded={openForm === 'sql'} aria-controls="{uid}-sql" onclick={() => toggleForm('sql')}>{SOURCES_TEXT.addSql}</button>
      <button type="button" class="secondary-button" aria-expanded={openForm === 'redis'} aria-controls="{uid}-redis" onclick={() => toggleForm('redis')}>{SOURCES_TEXT.addRedis}</button>
      <button type="button" class="secondary-button" aria-expanded={openForm === 'files'} aria-controls="{uid}-files" onclick={() => toggleForm('files')}>{SOURCES_TEXT.addFiles}</button>
    </div>

    {#if openForm === 'mongo'}
      <div class="panel" id="{uid}-mongo"><h5>{SOURCES_TEXT.mongoHeading}</h5><MongoClassify /></div>
    {:else if openForm === 'sql'}
      <div class="panel" id="{uid}-sql"><SqlServerForm /></div>
    {:else if openForm === 'redis'}
      <div class="panel" id="{uid}-redis"><ReachableCheck kind="redis" /></div>
    {:else if openForm === 'files'}
      <div class="panel" id="{uid}-files">
        <h5>{SOURCES_TEXT.fileHeading}</h5>
        <p class="hint">{SOURCES_TEXT.fileIntro}</p>
        <div class="field">
          <label for="{uid}-fk">{SOURCES_TEXT.fileKind}</label>
          <select id="{uid}-fk" bind:value={fileKind}>
            {#each FILE_KIND_OPTIONS as option (option.kind)}<option value={option.kind}>{option.label}</option>{/each}
          </select>
        </div>
        <div class="field">
          <label for="{uid}-fn">{SOURCES_TEXT.fileName}</label>
          <input id="{uid}-fn" type="text" bind:value={fileLabel} maxlength="80" autocomplete="off" autocapitalize="off" spellcheck="false" aria-describedby="{uid}-fn-hint" onkeydown={onFileKey} />
          <p class="hint" id="{uid}-fn-hint">{SOURCES_TEXT.fileNameHint}</p>
        </div>
        <div class="actions"><button type="button" class="primary-button" onclick={submitFile}>{SOURCES_TEXT.fileAdd}</button></div>
        <p class="hint" class:bad={fileFailed} role="status" hidden={fileLine === ''}>{fileLine}</p>
      </div>
    {/if}
  </section>

  <!-- Part 3: what is chosen, and its names -->
  <section class="part" aria-labelledby="{uid}-mine">
    <h4 id="{uid}-mine" tabindex="-1" bind:this={listHeading}>{yourSourcesText(wizard.sources.length)}</h4>
    {#if wizard.sources.length === 0}
      <p class="hint">{SOURCES_TEXT.tagsEmpty}</p>
    {:else}
      <p class="hint">{SOURCES_TEXT.tagsIntro} {SOURCES_TEXT.tagsNote}</p>
      <ul class="mine" aria-label={SOURCES_TEXT.tagsHeading}>
        {#each wizard.sources as source (sourceKey(source))}
          <SourceTagRow {source} onRemoved={() => void afterRemoved()} />
        {/each}
      </ul>
    {/if}
  </section>
</div>

<style>
  .gallery { display: grid; gap: var(--space-5); min-width: 0; max-width: 900px; }
  .lead { margin: 0; max-width: 64ch; color: var(--text-2); font-size: var(--fs-5); overflow-wrap: anywhere; }
  .notices { display: grid; gap: var(--space-2); }
  .notice { margin: 0; padding: var(--space-2) var(--space-3); color: var(--text); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .notice[hidden], .inline-error[hidden], .hint[hidden] { display: none; }
  .inline-error { margin: 0; }
  .part { display: grid; gap: var(--space-3); align-content: start; min-width: 0; }
  .part h4 { margin: 0; font-size: var(--fs-5); font-weight: 600; }
  .part h4:focus { outline: none; }
  .part h4:focus-visible { outline: 2px solid var(--focus); outline-offset: 4px; }
  .group { display: grid; gap: var(--space-2); min-width: 0; }
  .group h5, .panel h5 { margin: 0; font-size: var(--fs-4); font-weight: 600; }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .hint.bad { color: var(--text); }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .progress { display: grid; gap: var(--space-1); margin: 0; padding: 0; list-style: none; color: var(--text-2); font-size: var(--fs-3); }
  .rows, .gaps, .mine { display: grid; gap: var(--space-2); margin: 0; padding: 0; list-style: none; min-width: 0; }
  .found { min-width: 0; padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .found.muted { background: var(--bg); }
  .pick { display: grid; grid-template-columns: 24px minmax(0, 1fr); gap: var(--space-3); align-items: start; }
  .pick input[type='checkbox'] { width: 22px; height: 22px; margin: 2px 0 0; accent-color: var(--accent); }
  .pick input[type='checkbox']:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .nobox { display: block; width: 22px; height: 22px; }
  .body { display: grid; gap: var(--space-1); min-width: 0; }
  .name { font-size: var(--fs-4); font-weight: 600; overflow-wrap: anywhere; }
  .found.muted .name { color: var(--text-2); }
  .chips { display: flex; flex-wrap: wrap; gap: var(--space-1); margin: 0; padding: 0; list-style: none; }
  .chip { padding: 0 var(--space-2); color: var(--text-2); background: var(--surface-2); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font-size: var(--fs-2); }
  .panel { display: grid; gap: var(--space-3); min-width: 0; padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .field { display: grid; gap: var(--space-1); max-width: 640px; min-width: 0; }
  .field label { font-size: var(--fs-3); font-weight: 600; }
  .field input, .field select { box-sizing: border-box; width: 100%; max-width: 100%; min-height: 44px; padding: 8px 10px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-4); }
  .field input:focus-visible, .field select:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .link-button { padding: 0; color: var(--accent); background: transparent; border: 0; font: inherit; font-size: var(--fs-3); text-decoration: underline; cursor: pointer; }
  .link-button:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
</style>
