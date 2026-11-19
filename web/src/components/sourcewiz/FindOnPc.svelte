<script lang="ts">
  import { onMount } from 'svelte';
  import { cancelScan, isChosen, loadEntry, reconcileEnvironments, scanIsStale, sourcesView, startScan, tickFinding } from '../../lib/wizardSources.svelte';
  import { galleryGroups, sourceKey } from '../../lib/wizardSources';
  import type { GalleryEntry } from '../../lib/wizardSources';
  import { GROUPS, ageMinutes, cancelAllowed } from '../../lib/discover';
  import type { Finding } from '../../lib/discover';
  import { DISCOVER_TEXT, detectorLine, gapReasonText, howText, rowMetaText, showsText } from '../../lib/copy/discoverCopy';
  import { SOURCES_TEXT, notReadableText } from '../../lib/copy/wizardSourcesCopy';
  import { WIZ } from '../../lib/copy/sourceWizardCopy';
  import LocateFolder from '../wizard/LocateFolder.svelte';
  import OpenFolderButton from './OpenFolderButton.svelte';

  // "Find on this PC": the first tile. One scan of this computer; each row has a status word and (when Unravis can read it) a tick box.
  // Ticks are collected here and added together by "Add N ticked", so a row is linked to its source by the finding id and nothing is added by accident.
  // MongoDB and SQL Server open their short form (the card host does that); a Redis the scan did not find can be added by address.
  let { onCard, onDone, connected = [] }: { onCard: (card: 'mongo' | 'sql' | 'redis', finding?: string) => void; onDone: () => void; connected?: string[] } = $props();

  const uid = $props.id();
  let ticked = $state<Record<string, boolean>>({});
  let note = $state('');
  let scanClickedAt = $state(0);

  const groups = $derived(galleryGroups(sourcesView.findings, GROUPS));
  const offered = $derived(groups.reduce((n, g) => n + g.entries.length, 0));
  const gapsRedis = $derived(sourcesView.gaps.filter((g) => g.detector === 'redis'));
  const running = $derived(sourcesView.phase === 'scanning' || sourcesView.phase === 'checking');
  const stale = $derived(sourcesView.phase === 'done' && scanIsStale());
  const entryKind = $derived(sourcesView.entry.kind);
  const toAdd = $derived(Object.keys(ticked).filter((id) => ticked[id] && !isChosen(sourceKey({ kind: 'discover', ref: id }))));

  onMount(() => { reconcileEnvironments(); void loadEntry(); });

  const dbKind = (f: Finding): 'mongo' | 'sql' | 'redis' | '' => (f.detector === 'mongo' ? 'mongo' : f.detector === 'sql' ? 'sql' : f.detector === 'redis' ? 'redis' : '');
  const isFile = (e: GalleryEntry): boolean => e.state !== 'steps';
  const statusOf = (e: GalleryEntry): string => (isChosen(sourceKey({ kind: 'discover', ref: e.finding.id })) || connected.includes(e.finding.id) ? WIZ.find.added : e.state === 'ready' ? WIZ.find.statusWorks : e.state === 'unverified' ? WIZ.find.statusNotReadable : dbKind(e.finding) ? WIZ.find.statusFound : WIZ.find.statusNeedsSteps);
  const metaOf = (e: GalleryEntry): string => rowMetaText(e.finding, ageMinutes(e.finding.newest, Date.now()));

  async function onScan() { scanClickedAt = Date.now(); note = ''; ticked = {}; await startScan(); }
  async function onStop() { if (cancelAllowed(scanClickedAt, Date.now())) await cancelScan(); }

  function addTicked() {
    let refused = 0;
    for (const id of toAdd) { tickFinding(id, true); if (!isChosen(sourceKey({ kind: 'discover', ref: id }))) refused++; }
    ticked = {};
    if (refused > 0) { note = SOURCES_TEXT.limitFull; return; }
    onDone();
  }
</script>

<div class="find">
  <h3>{WIZ.find.heading}</h3>
  <p class="hint">{WIZ.find.intro}</p>
  {#if sourcesView.problem !== ''}<p class="inline-error" role="alert">{sourcesView.problem}</p>{/if}
  {#if note !== ''}<p class="notice" role="status">{note}</p>{/if}

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
        <button type="button" class="secondary-button" disabled={sourcesView.cancelling} onclick={() => void onStop()}>{sourcesView.cancelling ? SOURCES_TEXT.scanCancelling : WIZ.find.cancel}</button>
      {:else}
        <button type="button" class="primary-button" onclick={() => void onScan()}>{sourcesView.phase === 'done' ? WIZ.find.again : WIZ.find.start}</button>
      {/if}
    </div>
    {#if running}
      <p class="hint" role="status">{SOURCES_TEXT.scanBusy}</p>
      {#if sourcesView.progress.length > 0}
        <ul class="progress" aria-label={DISCOVER_TEXT.scanning}>{#each sourcesView.progress as step (step.detector)}<li>{detectorLine(step.detector, step.state)}</li>{/each}</ul>
      {/if}
    {:else if sourcesView.phase === 'done'}
      {#if stale}<p class="notice" role="status">{SOURCES_TEXT.locateExpired}</p>{/if}
      <p class="found-line" role="status">{WIZ.find.found(offered + gapsRedis.length)}</p>
      <p class="hint">{WIZ.find.mongoSqlNote}</p>
      {#if offered + gapsRedis.length === 0}<p class="hint">{WIZ.find.nothing}</p>{/if}

      {#each groups as group (group.id)}
        <section class="group" aria-labelledby="{uid}-g-{group.id}">
          <h4 id="{uid}-g-{group.id}">{DISCOVER_TEXT.groups[group.id]}</h4>
          <ul class="rows" aria-label={DISCOVER_TEXT.groups[group.id]}>
            {#each group.entries as entry (entry.finding.id)}
              {@const f = entry.finding}
              {@const rowId = uid + '-f-' + f.id}
              {@const db = dbKind(f)}
              {@const added = isChosen(sourceKey({ kind: 'discover', ref: f.id })) || connected.includes(f.id)}
              <li class="row" class:muted={entry.state !== 'ready'} data-finding={f.id}>
                <div class="pick">
                  {#if entry.state === 'ready'}
                    <input id={rowId} type="checkbox" checked={added || ticked[f.id] === true} disabled={added} aria-describedby="{rowId}-d" onchange={(e) => { ticked[f.id] = e.currentTarget.checked; }} />
                  {:else}<span class="nobox" aria-hidden="true"></span>{/if}
                  <div class="body">
                    {#if entry.state === 'ready'}<label class="name" for={rowId}>{f.display}</label>{:else}<span class="name">{f.display}</span>{/if}
                    <span class="status" class:ok={entry.state === 'ready'} title={entry.state === 'ready' ? SOURCES_TEXT.chipTipReady : entry.state === 'unverified' ? SOURCES_TEXT.chipTipUnverified : SOURCES_TEXT.chipTipSteps}>{statusOf(entry)}</span>
                    <p class="hint" id="{rowId}-d">
                      {#if entry.state === 'unverified'}{SOURCES_TEXT.unverifiedLead} {notReadableText(f.unreadable)}
                      {:else if entry.state === 'steps'}{db ? '' : SOURCES_TEXT.stepsLead + ' ' + howText(f.how)}
                      {:else}{showsText(f.shows, f.importer)} {metaOf(entry)}{/if}
                    </p>
                    <div class="actions">
                      {#if db === 'mongo' || db === 'sql'}
                        <button type="button" class="secondary-button" onclick={() => onCard(db, f.id)}>{connected.includes(f.id) ? WIZ.find.added : WIZ.find.connect}</button>
                      {:else if db === 'redis'}
                        <button type="button" class="secondary-button" onclick={() => onCard('redis')}>{WIZ.find.addByAddress}</button>
                      {/if}
                      {#if isFile(entry) && sourcesView.scanId !== ''}<OpenFolderButton target={{ scan: sourcesView.scanId, finding: f.id }} what={f.display} onLine={(l) => { note = l; }} />{/if}
                    </div>
                    {#if entry.state === 'unverified'}
                      <LocateFolder findingId={f.id} label={f.display} onFound={() => { note = ''; }} />
                    {/if}
                  </div>
                </div>
              </li>
            {/each}
          </ul>
        </section>
      {/each}

      {#if gapsRedis.length > 0 || sourcesView.gaps.length > 0}
        <section class="group" aria-labelledby="{uid}-gaps">
          <h4 id="{uid}-gaps">{SOURCES_TEXT.gapsHeading}</h4>
          <ul class="rows">
            {#each sourcesView.gaps as gap, i (i)}
              <li class="row muted"><span class="name">{gap.display}</span> <span class="status">{WIZ.find.statusNotFound}</span> <span class="hint">{gapReasonText(gap.reason)}</span>
                {#if gap.detector === 'redis'}<div class="actions"><button type="button" class="secondary-button" onclick={() => onCard('redis')}>{WIZ.find.addByAddress}</button></div>{/if}
              </li>
            {/each}
          </ul>
        </section>
      {/if}

      <div class="actions sticky">
        <button type="button" class="primary-button" disabled={toAdd.length === 0} onclick={addTicked}>{WIZ.find.addTicked(toAdd.length)}</button>
      </div>
    {/if}
  {/if}
</div>

<style>
  .find { display: grid; gap: var(--space-3); min-width: 0; }
  h3, h4 { margin: 0; font-size: var(--fs-5); font-weight: 600; }
  h4 { font-size: var(--fs-4); }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .found-line { margin: 0; font-weight: 600; }
  .notice { margin: 0; padding: var(--space-2) var(--space-3); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-3); }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; }
  .group { display: grid; gap: var(--space-2); min-width: 0; }
  .rows, .progress { display: grid; gap: var(--space-2); margin: 0; padding: 0; list-style: none; min-width: 0; }
  .progress { color: var(--text-2); font-size: var(--fs-3); }
  .row { min-width: 0; padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .row.muted { background: var(--bg); }
  .pick { display: grid; grid-template-columns: 24px minmax(0, 1fr); gap: var(--space-3); align-items: start; }
  .pick input[type='checkbox'] { width: 22px; height: 22px; margin: 2px 0 0; accent-color: var(--accent); }
  .pick input[type='checkbox']:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .nobox { display: block; width: 22px; height: 22px; }
  .body { display: grid; gap: var(--space-1); min-width: 0; }
  .name { font-size: var(--fs-4); font-weight: 600; overflow-wrap: anywhere; }
  .status { justify-self: start; padding: 0 var(--space-2); color: var(--text-2); background: var(--surface-2); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font-size: var(--fs-2); }
  .status.ok { color: var(--text); font-weight: 600; }
  .sticky { position: sticky; bottom: 0; padding: var(--space-2) 0; background: var(--bg); }
</style>
