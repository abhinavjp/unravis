<script lang="ts">
  import { onDestroy, tick, untrack, type Snippet } from 'svelte';
  import { EXTRA_KIND_IDS, FILE_KIND_IDS, hostNeed, importerFor, isPanelKind, kindsInUse, needingHost, needingZone, needsHost, usableFiles } from '../../lib/fileKinds';
  import { filesFromDrop } from '../../lib/dropEntries';
  import type { PanelKind, StagedFile } from '../../lib/fileKinds';
  import { addStagedFiles, createStaging, wizardStaging } from '../../lib/wizardFiles.svelte';
  import { fileSizeProblem, type RowResult } from '../../lib/cases';
  import { isZipName, readZip } from '../../lib/zipRead';
  import { BATCH_TEXT, batchHostWarnText, batchImportText, batchReasonText, formatBytes, sizeProblemText, verdictLabel, zipSkippedText } from '../../lib/copy/importCopy';
  import {
    CARDS_TEXT, FILES_TEXT, FILE_CARDS, FILE_KIND_LABELS, EXTRA_KIND_LABELS, addFilesText, cardCountText, cardGroupHeading, cardInputLabel, addedText, alreadyListedText, chosenKindText, dropUnreadableText, hostWarnText, kindSelectLabel, removeRowLabel, looksLikeText, readsText, skippedText, zoneText, zoneWarnText
  } from '../../lib/copy/fileKindsCopy';
  import type { FileCard } from '../../lib/copy/fileKindsCopy';
  import IisGuide from './IisGuide.svelte';
  import FileGuides from './FileGuides.svelte';
  import ZoneChoice from './ZoneChoice.svelte';

  // The "Import files" panel of the sources step (gap G8, wireframe w37/w38). The person chooses many files or a folder, or drops files here.
  // Every row shows what the file name suggests (changeable), the time zone its times are in, and its size. "Add" turns the rows into
  // sources and holds the files for the last step, so the review step does not ask for them again. Nothing is read or sent from here.
  //
  // The same panel serves the import drawer of an open case (packet P9) when `drawer` is set. There, a .zip is opened in the page, the button
  // hands the rows to `onImport` instead of adding sources, the list stays after the import, and a Result column shows how each file ended
  // (Used, Partly used or Rejected, with a plain reason and Try again). `results` is keyed by row id. A row being sent cannot be changed.
  let { drawer = false, busy = false, canImport = true, results = {}, pending = $bindable(0), onImport, onRetry, onListChange, onCancel, footer }: {
    drawer?: boolean;
    /** Drawer only: how many listed files have no result yet (staged, not imported), for the Add data cards and the leave question. */
    pending?: number;
    /** The drawer is sending files: the list cannot be cleared and nothing is sent twice. */
    busy?: boolean;
    /** False until the person has confirmed the notice. */
    canImport?: boolean;
    results?: Record<number, RowResult>;
    onImport?: (rows: StagedFile[]) => void;
    onRetry?: (row: StagedFile) => void;
    /** Files were added to the list or the list was cleared: whatever the person confirmed for the old list no longer covers this one. */
    onListChange?: () => void;
    /** Wizard only: the Cancel button at the foot of the panel (wireframe w37). The files already chosen stay in the draft's list. */
    onCancel?: () => void;
    /** Drawn above the buttons: the notice the person confirms. */
    footer?: Snippet;
  } = $props();
  const uid = $props.id();

  // The wizard keeps its list for the whole draft, so it survives another add-source form being opened (gap F37). The drawer of an open case
  // has its own list, which ends with the drawer.
  const staging = untrack(() => (drawer ? createStaging() : wizardStaging));
  const list = $derived(staging.state.list);
  const fromFolder = $derived(staging.state.fromFolder);
  const reading = $derived(staging.state.reading);
  $effect(() => { if (drawer) pending = list.filter((item) => results[item.id] === undefined).length; });
  // The type cards (wireframe w37, wizard only): a file chosen in a card is listed under that card and takes its kind. The rows without a card
  // (chosen or dropped in the general area) are listed first, as before.
  const cardInputs = $state<Record<string, HTMLInputElement | undefined>>({});
  const groups = $derived.by(() => {
    const out: Array<{ id: string; title: string; rows: StagedFile[] }> = [];
    const general = list.filter((item) => !item.origin);
    if (general.length > 0 || list.length === 0) out.push({ id: '', title: CARDS_TEXT.otherFiles, rows: general });
    for (const card of FILE_CARDS) {
      const rows = list.filter((item) => item.origin === card.id);
      if (rows.length > 0) out.push({ id: card.id, title: card.title, rows });
    }
    return out;
  });
  const showGroupHeads = $derived(groups.length > 1 || (groups.length === 1 && groups[0].id !== ''));
  const cardCount = (card: FileCard): number => list.filter((item) => item.origin === card.id).length;
  let over = $state(false);
  let note = $state('');
  let noteBad = $state(false);
  let filesInput = $state<HTMLInputElement>();
  let folderInput = $state<HTMLInputElement>();
  let noteEl = $state<HTMLElement>();
  let rootEl = $state<HTMLElement>();
  let chooseButton = $state<HTMLButtonElement>();
  // In the drawer, leaving the screen ends any read in flight the same way a Clear does: a zip being opened is not carried on for a list nobody
  // sees. The wizard's list outlives the panel, so a read in flight there finishes into it.
  onDestroy(() => { if (drawer) staging.cancel(); });

  const bigProblem = (item: StagedFile) => {
    const problem = item.choice === '' ? null : fileSizeProblem(item.size, importerFor(item.choice));
    return problem && problem.kind === 'too-big' ? problem : null;
  };
  const ready = $derived(usableFiles(list).filter((item) => bigProblem(item) === null));
  // A kind that needs a computer is told by the computer warning; the zone warning is left for kinds that only offer one.
  const zoneRows = $derived(needingZone(ready).filter((item) => !needsHost(item.choice) && item.host === ''));
  const hostRows = $derived(needingHost(ready));
  const reads = $derived(kindsInUse(ready));
  // Drawer only: a file with a result has been started. Files without one are the ones the button sends.
  const fresh = $derived(ready.filter((item) => results[item.id] === undefined));
  const locked = (item: StagedFile): boolean => busy && results[item.id]?.state !== 'done';
  // The kind and the computer of a file that was kept (Used, Partly used) are fixed: what the screen shows is what was sent. A rejected file may be
  // changed, since Try again sends the row as it is shown then.
  const finished = (item: StagedFile): boolean => {
    const result = results[item.id];
    return result?.state === 'done' && result.outcome.verdict !== 'rejected';
  };
  const choiceLocked = (item: StagedFile): boolean => locked(item) || finished(item);
  // Try again is for a file that is meant to go: one set to "Don't use this file", or refused here, is not sent by it.
  const canRetry = (item: StagedFile): boolean => item.choice !== '' && !refused(item);

  /** A .zip is opened here (wizard and drawer) and its files take its place in the list. Returns what to tell the person. */
  async function expand(picked: File[], mine: number): Promise<{ files: File[]; says: string[] }> {
    if (!picked.some((file) => isZipName(file.name))) return { files: picked, says: [] };
    const files: File[] = [];
    const says: string[] = [];
    note = BATCH_TEXT.zipReading;
    noteBad = false;
    for (const file of picked) {
      if (!isZipName(file.name)) { files.push(file); continue; }
      // A Clear (or leaving the screen) during an earlier archive ends the work: the rest are not unpacked into memory for a list that is gone.
      if (!staging.current(mine)) return { files: [], says: [] };
      const zip = await readZip(file);
      if (zip.problem === 'not-zip') says.push(file.name + ': ' + BATCH_TEXT.zipNotZip);
      else if (zip.problem === 'damaged') says.push(file.name + ': ' + BATCH_TEXT.zipDamaged);
      else if (zip.problem === 'too-big') says.push(file.name + ': ' + BATCH_TEXT.zipTooBig);
      else if (zip.files.length === 0) says.push(file.name + ': ' + BATCH_TEXT.zipEmpty);
      if (zip.truncated) says.push(file.name + ': ' + BATCH_TEXT.zipTruncated);
      if (zip.skipped.length > 0) says.push(file.name + ': ' + zipSkippedText(zip.skipped.length));
      files.push(...zip.files);
    }
    return { files, says };
  }

  const sameFile = (a: File, b: File): boolean => (a.webkitRelativePath || a.name) === (b.webkitRelativePath || b.name) && a.size === b.size && a.lastModified === b.lastModified;
  /** The files that are not yet in the list (nor earlier in this same pick). */
  function newFiles(files: File[]): File[] {
    const seen: File[] = list.map((item) => item.file);
    const out: File[] = [];
    for (const file of files) {
      if (seen.some((other) => sameFile(file, other))) continue;
      seen.push(file);
      out.push(file);
    }
    return out;
  }

  async function take(files: ArrayLike<File> | null | undefined, folder: boolean, before: string[] = [], owner: number | null = null, card: FileCard | null = null) {
    // A drop hands its own read over, so the walk and the opening of a .zip count as one read.
    const mine = owner ?? staging.beginRead();
    const opened = await expand(Array.from(files ?? []), mine);
    if (!staging.current(mine)) return;
    const says = [...before, ...opened.says];
    // The wizard lists a file once: the same file (name, size and date) chosen again is left out, and the person is told in a quiet line.
    const fresh = drawer ? opened.files : newFiles(opened.files);
    const repeats = opened.files.length - fresh.length;
    if (fresh.length > 0) {
      staging.append(fresh, folder, card?.id, card?.kind);
      onListChange?.();
    }
    note = [...says, ...(repeats > 0 ? [alreadyListedText(repeats)] : [])].join(' ');
    noteBad = says.length > 0;
    if (owner === null) staging.endRead(mine);
  }

  function onPick(event: Event & { currentTarget: HTMLInputElement }, folder: boolean) {
    const input = event.currentTarget;
    // The files are taken out of the input first: clearing the value empties its list. The value is cleared so the same file can be chosen again after Clear.
    const picked = Array.from(input.files ?? []);
    input.value = '';
    void take(picked, folder);
  }

  function onCardPick(event: Event & { currentTarget: HTMLInputElement }, card: FileCard) {
    const input = event.currentTarget;
    const picked = Array.from(input.files ?? []);
    input.value = '';
    void take(picked, false, [], null, card);
  }

  function onDragOver(event: DragEvent) {
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
    over = true;
  }

  function onDragLeave(event: DragEvent) {
    const here = event.currentTarget as HTMLElement;
    if (event.relatedTarget instanceof Node && here.contains(event.relatedTarget)) return;
    over = false;
  }

  async function onDrop(event: DragEvent) {
    event.preventDefault();
    over = false;
    const transfer = event.dataTransfer;
    if (!transfer) return;
    // Taken now: the browser clears the drop's items as soon as this handler yields.
    const items = Array.from(transfer.items ?? []);
    const plain = Array.from(transfer.files ?? []);
    const mine = staging.beginRead();
    note = FILES_TEXT.dropReading;
    noteBad = false;
    const dropped = await filesFromDrop(items, plain);
    if (!staging.current(mine)) return;
    if (dropped.files.length === 0) {
      note = dropped.unreadable > 0 ? dropUnreadableText(dropped.unreadable) : plain.length === 0 && items.length > 0 ? FILES_TEXT.dropNoEntries : FILES_TEXT.dropNoFiles;
      noteBad = true;
      staging.endRead(mine);
      return;
    }
    const parts: string[] = [];
    if (dropped.tooMany) parts.push(FILES_TEXT.dropTruncated);
    if (dropped.tooDeep) parts.push(FILES_TEXT.dropTooDeep);
    if (dropped.unreadable > 0) parts.push(dropUnreadableText(dropped.unreadable));
    await take(dropped.files, dropped.folder, parts, mine);
    staging.endRead(mine);
  }

  const dropHere = (event: DragEvent) => { void onDrop(event); };

  function setKind(id: number, value: string) {
    // The three extra choices are for the wizard; the drawer offers the importers only.
    const choice: PanelKind | '' = isPanelKind(value) && (!drawer || !(EXTRA_KIND_IDS as readonly string[]).includes(value)) ? value : '';
    // A new kind may need another computer (or none), so the one picked for the old kind is let go.
    staging.setList(list.map((item) => (item.id === id ? { ...item, choice, changed: choice !== item.suggested, host: choice === item.choice ? item.host : '' } : item)));
  }

  function setHost(id: number, host: string) {
    staging.setList(list.map((item) => (item.id === id ? { ...item, host } : item)));
  }

  // The rows move up when one goes, so the second click of a double click would land on the next row's Remove and drop a second file. A click
  // that is the second (or later) of a quick series is left alone; a keyboard press (detail 0) and a click on its own count as one remove.
  function onRemoveClick(event: MouseEvent, id: number) {
    if (event.detail > 1) return;
    void removeRow(id);
  }

  async function removeRow(id: number) {
    if (busy && results[id] && results[id].state !== 'done') return;
    // The place of the row, before it goes: the row that took the place afterwards gets the focus.
    const at = list.findIndex((item) => item.id === id);
    staging.setList(list.filter((item) => item.id !== id));
    // The clicked button is destroyed with its row: focus the Remove button that took its place, else the one before it, else the choose-files button.
    // A Remove button of a row being sent is disabled and cannot take the focus, so only the enabled ones are looked at.
    await tick();
    const buttons = Array.from(rootEl?.querySelectorAll<HTMLButtonElement>('[data-remove-row]') ?? []);
    const from = Math.max(Math.min(at, buttons.length - 1), 0);
    const after = buttons.slice(from).find((button) => !button.disabled);
    const before = buttons.slice(0, from).reverse().find((button) => !button.disabled);
    const target = at >= 0 ? (after ?? before) : undefined;
    (target ?? chooseButton)?.focus();
  }

  // The Clear button goes with the list it clears, so the focus moves to the choose-files button.
  async function focusChoose() {
    await tick();
    chooseButton?.focus();
  }

  function send() {
    if (fresh.length === 0 || busy) return;
    onImport?.(fresh);
  }

  function clear() {
    staging.clear();
    note = '';
    noteBad = false;
    onListChange?.();
    void focusChoose();
  }

  async function add() {
    const left = list.length - ready.length;
    const result = addStagedFiles(ready);
    const parts: string[] = [];
    if (result.added > 0) parts.push(addedText(result.added));
    if (left > 0) parts.push(skippedText(left));
    if (result.full > 0) parts.push(FILES_TEXT.full);
    note = parts.join(' ');
    noteBad = result.added === 0;
    staging.clear();
    await tick();
    noteEl?.focus();
  }

  function rowNote(item: StagedFile): string {
    if (item.refusal === 'compressed') return FILES_TEXT.compressed;
    if (item.refusal === 'empty') return FILES_TEXT.empty;
    const big = bigProblem(item);
    if (big) return sizeProblemText(big, item.size);
    if (item.choice === '') return item.suggested === '' ? FILES_TEXT.notRecognisedLeft : '';
    return item.changed ? chosenKindText(item.choice) : looksLikeText(item.choice);
  }

  const refused = (item: StagedFile): boolean => item.refusal !== '' || bigProblem(item) !== null;
</script>

<!-- In the wizard the whole panel takes a drop (wireframe w37 has no drop box); the drawer keeps its own dashed zone below. -->
<div class="files" class:in-drawer={drawer} class:over={!drawer && over} role="group" aria-labelledby="{uid}-h"
  ondragover={drawer ? undefined : onDragOver} ondragenter={drawer ? undefined : onDragOver} ondragleave={drawer ? undefined : onDragLeave} ondrop={drawer ? undefined : dropHere} bind:this={rootEl}>
  <h5 id="{uid}-h">{drawer ? BATCH_TEXT.heading : FILES_TEXT.heading}</h5>
  <p class="hint">{drawer ? BATCH_TEXT.intro : FILES_TEXT.intro}</p>

  {#if !drawer}
    <section class="cards" aria-labelledby="{uid}-cards">
      <h6 id="{uid}-cards" class="sr-only">{CARDS_TEXT.heading}</h6>
      <ul class="card-grid">
        {#each FILE_CARDS as card (card.id)}
          <li class="file-card" data-file-card={card.id}>
            <p class="card-title">{card.title}</p>
            <p class="hint card-text">{card.text}</p>
            <p class="card-chips"><span class="chip">{card.chip}</span></p>
            <p class="sr-only" data-card-count={card.id}>{cardCountText(cardCount(card))}</p>
            <button type="button" class="secondary-button" onclick={() => cardInputs[card.id]?.click()}>{card.button}</button>
            <input bind:this={cardInputs[card.id]} class="pick-input" type="file" multiple tabindex="-1" aria-label={cardInputLabel(card)} onchange={(event) => onCardPick(event, card)} />
          </li>
        {/each}
      </ul>
      <p class="hint">{CARDS_TEXT.footer}</p>
    </section>
    <p class="hint unsure">{CARDS_TEXT.unsure}</p>
  {:else}
    <div class="drop" class:over role="group" aria-labelledby="{uid}-drop" ondragover={onDragOver} ondragenter={onDragOver} ondragleave={onDragLeave} ondrop={(event) => void onDrop(event)} aria-busy={reading}>
      <p class="drop-title" id="{uid}-drop">{FILES_TEXT.dropTitle}</p>
      <p class="hint">{FILES_TEXT.dropOr}</p>
      <div class="actions">
        <button type="button" class="secondary-button" bind:this={chooseButton} onclick={() => filesInput?.click()}>{FILES_TEXT.chooseFiles}</button>
        <button type="button" class="secondary-button" onclick={() => folderInput?.click()}>{FILES_TEXT.chooseFolder}</button>
      </div>
    </div>
  {/if}
  <input bind:this={filesInput} class="pick-input" type="file" multiple tabindex="-1" aria-label={FILES_TEXT.filesInputLabel} onchange={(event) => onPick(event, false)} />
  <input bind:this={folderInput} class="pick-input" type="file" webkitdirectory tabindex="-1" aria-label={FILES_TEXT.folderInputLabel} onchange={(event) => onPick(event, true)} />

  <p class="note" class:bad={noteBad} role="status" tabindex="-1" bind:this={noteEl} hidden={note === ''}>{note}</p>

  {#if list.length > 0}
    <section class="list" aria-labelledby="{uid}-list">
      <h5 id="{uid}-list">{fromFolder ? FILES_TEXT.listHeadingFolder : FILES_TEXT.listHeading}</h5>
      <p class="hint">{drawer ? BATCH_TEXT.listHelp : FILES_TEXT.listHelp}{fromFolder ? ' ' + FILES_TEXT.folderNote : ''}</p>
      {#each groups as group (group.id)}
      {#if showGroupHeads}<h6 class="group-head" data-group={group.id}>{cardGroupHeading(group.title, group.rows.length)}</h6>{/if}
      <div class="scroll">
        <table>
          <thead>
            <tr>
              <th scope="col">{FILES_TEXT.colFile}</th>
              <th scope="col" class="num">{FILES_TEXT.colSize}</th>
              <th scope="col">{FILES_TEXT.colKind}</th>
              <th scope="col">{FILES_TEXT.colComputer}</th>
              {#if drawer}<th scope="col">{BATCH_TEXT.colResult}</th>{/if}
            </tr>
          </thead>
          <tbody>
            {#each group.rows as item (item.id)}
              {@const text = rowNote(item)}
              <tr class:refused={refused(item)} data-kind={item.choice} data-file={item.name} data-verdict={drawer ? (results[item.id]?.state === 'done' ? (results[item.id] as { outcome: { verdict: string } }).outcome.verdict : (results[item.id]?.state ?? '')) : undefined}>
                <th scope="row" class="file">
                  <div class="file-cell">
                    <span class="file-name">{item.name}</span>
                    {#if text !== ''}<span class="row-note" id="{uid}-n-{item.id}">{text}</span>{/if}
                  </div>
                </th>
                <td class="num" data-label={FILES_TEXT.colSize}>{formatBytes(item.size)}</td>
                <td data-label={FILES_TEXT.colKind}>
                  <select value={item.choice} disabled={item.refusal !== '' || choiceLocked(item)} aria-label={kindSelectLabel(item.name)} aria-describedby={text !== '' ? uid + '-n-' + item.id : undefined}
                    onchange={(event) => setKind(item.id, event.currentTarget.value)}>
                    <option value="">{FILES_TEXT.dontUse}</option>
                    {#each FILE_KIND_IDS as id (id)}<option value={id}>{FILE_KIND_LABELS[id]}</option>{/each}
                    {#if !drawer}{#each EXTRA_KIND_IDS as id (id)}<option value={id}>{EXTRA_KIND_LABELS[id]}</option>{/each}{/if}
                  </select>
                  <button type="button" class="quiet-button row-remove" data-remove-row disabled={locked(item)} aria-label={removeRowLabel(item.name)} onclick={(event) => onRemoveClick(event, item.id)}>{FILES_TEXT.removeRow}</button>
                </td>
                <td class="zone" data-label={FILES_TEXT.colComputer}>
                  {item.choice === '' || item.refusal !== '' ? FILES_TEXT.noZone : zoneText(item.choice)}
                  {#if item.choice !== '' && item.refusal === '' && hostNeed(item.choice) !== 'none'}
                    <ZoneChoice kind={item.choice} host={item.host} name={item.name} onChange={(label) => setHost(item.id, label)} disabled={choiceLocked(item)} />
                  {/if}
                </td>
                {#if drawer}
                  {@const result = results[item.id]}
                  <td class="result" data-label={BATCH_TEXT.colResult}>
                    {#if result === undefined}
                      {#if refused(item) || item.choice === ''}<span class="muted">—</span>{/if}
                    {:else if result.state === 'waiting'}
                      <span class="muted">{BATCH_TEXT.waiting}</span>
                    {:else if result.state === 'sending'}
                      <span class="muted" role="status">{BATCH_TEXT.sending}</span>
                    {:else}
                      <span class="verdict {result.outcome.verdict}">{verdictLabel(result.outcome.verdict)}</span>
                      <span class="why">{batchReasonText(result.outcome)}</span>
                      {#if result.outcome.verdict === 'rejected' && canRetry(item)}
                        <button type="button" class="quiet-button" disabled={busy} aria-label={BATCH_TEXT.tryAgain + ': ' + item.name} onclick={() => { if (canRetry(item)) onRetry?.(item); }}>{BATCH_TEXT.tryAgain}</button>
                      {/if}
                    {/if}
                  </td>
                {/if}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {/each}

      {#if reads.length > 0}
        <div class="reads">
          <p class="reads-title">{FILES_TEXT.readsHeading}</p>
          <ul>{#each reads as id (id)}<li>{readsText(id)}</li>{/each}</ul>
        </div>
      {/if}
      {#if hostRows.length > 0}<p class="warn" role="status">{drawer ? batchHostWarnText(hostRows.length) : hostWarnText(hostRows.length)}</p>{/if}
      {#if zoneRows.length > 0}<p class="warn" role="status">{zoneWarnText(zoneRows.length)}</p>{/if}

      {#if drawer}
        {@render footer?.()}
        <div class="actions">
          <button type="button" class="primary-button" disabled={fresh.length === 0 || busy || !canImport} onclick={send}>{busy ? BATCH_TEXT.importing : batchImportText(fresh.length)}</button>
          <button type="button" class="quiet-button" disabled={busy} onclick={clear}>{FILES_TEXT.clear}</button>
        </div>
      {:else}
        <div class="actions">
          <button type="button" class="primary-button" disabled={ready.length === 0} onclick={() => void add()}>{addFilesText(ready.length)}</button>
          <button type="button" class="quiet-button" onclick={clear}>{FILES_TEXT.clear}</button>
        </div>
      {/if}
    </section>
  {/if}

  {#if drawer}
    <p class="hint supported">{BATCH_TEXT.supported}</p>
    <IisGuide />
  {/if}
  <!-- The wizard shows the card guides (FileGuides, the IIS one among them) and no second IIS guide. -->
  {#if !drawer}<FileGuides />{/if}
  {#if !drawer}
    <div class="actions foot" aria-busy={reading}>
      <button type="button" class="secondary-button" bind:this={chooseButton} onclick={() => filesInput?.click()}>{FILES_TEXT.chooseFiles}</button>
      <button type="button" class="quiet-button" onclick={() => folderInput?.click()}>{FILES_TEXT.chooseFolder}</button>
      <button type="button" class="quiet-button" data-cancel-files onclick={() => onCancel?.()}>{FILES_TEXT.cancel}</button>
    </div>
  {/if}
</div>

<style>
  .files { display: grid; gap: var(--space-3); min-width: 0; }
  .files h5 { margin: 0; font-size: var(--fs-4); font-weight: 600; }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .cards { display: grid; gap: var(--space-2); min-width: 0; }
  .cards h6, .group-head { margin: 0; font-size: var(--fs-3); font-weight: 600; }
  /* Wireframe w37: the four cards wrap three and one, each as tall as its own words (no equal-height empty gaps). */
  .card-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); align-items: start; gap: var(--space-3); margin: 0; padding: 0; list-style: none; }
  .file-card { display: grid; align-content: start; gap: var(--space-2); min-width: 0; padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .file-card > * { margin: 0; }
  .file-card button { justify-self: start; }
  .card-title { font-size: var(--fs-4); font-weight: 600; overflow-wrap: anywhere; }
  .file-card p, .file-card .chip { max-width: 100%; box-sizing: border-box; overflow-wrap: anywhere; }
  .chip { display: inline-block; padding: 0 var(--space-2); color: var(--text-2); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-2); }
  .unsure { max-width: 80ch; }
  /* A long word wraps only when it cannot fit a line alone, so the drive path of the IIS card is never cut in the middle of a name. */
  .file-card p.card-text { overflow-wrap: break-word; }
  .sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip-path: inset(50%); border: 0; white-space: nowrap; }
  .files.over { outline: 2px dashed var(--accent); outline-offset: 4px; }
  .foot { align-items: center; }
  .drop { display: grid; justify-items: center; gap: var(--space-2); min-width: 0; padding: var(--space-5) var(--space-3); text-align: center; background: var(--bg); border: 2px dashed var(--control-line); border-radius: var(--radius-sm); }
  .drop.over { background: var(--surface-2); border-color: var(--accent); }
  .drop-title { margin: 0; font-size: var(--fs-4); font-weight: 600; }
  .pick-input { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip-path: inset(50%); border: 0; white-space: nowrap; }
  .note { margin: 0; padding: var(--space-2) var(--space-3); color: var(--text); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .note[hidden] { display: none; }
  .note:focus { outline: none; }
  .note:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .list { display: grid; gap: var(--space-3); min-width: 0; }
  .scroll { min-width: 0; overflow-x: auto; border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  table { width: 100%; border-collapse: collapse; font-size: var(--fs-3); white-space: normal; }
  th, td { padding: var(--space-2) var(--space-3); text-align: left; vertical-align: top; border-bottom: var(--hairline-width) solid var(--line); }
  tbody tr:last-child > * { border-bottom: 0; }
  thead th { color: var(--text-2); background: var(--surface-2); font-size: var(--fs-2); font-weight: 600; white-space: nowrap; }
  tbody th.file { font-weight: 400; }
  .num { text-align: right; white-space: nowrap; }
  .file { min-width: 160px; }
  .file-cell { display: grid; gap: var(--space-1); }
  .file-name { font-family: var(--mono); font-size: var(--fs-2); overflow-wrap: anywhere; }
  .row-note { color: var(--text-2); font-size: var(--fs-2); overflow-wrap: anywhere; }
  .row-remove { display: block; margin-top: var(--space-1); }
  .zone { display: table-cell; min-width: 200px; color: var(--text-2); }
  .zone :global(.choice) { margin-top: var(--space-1); color: var(--text); }
  tr.refused .file-name { color: var(--text-2); text-decoration: line-through; }
  select { box-sizing: border-box; width: 100%; min-width: 230px; max-width: 100%; min-height: 36px; padding: 4px 8px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-3); }
  select:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .reads { display: grid; gap: var(--space-1); }
  .reads-title { margin: 0; font-size: var(--fs-3); font-weight: 600; }
  .reads ul { display: grid; gap: var(--space-1); margin: 0; padding-left: var(--space-5); color: var(--text-2); font-size: var(--fs-3); }
  .reads li { overflow-wrap: anywhere; }
  .warn { margin: 0; padding: var(--space-2) var(--space-3); color: var(--text); background: var(--surface-2); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .supported { max-width: 80ch; }
  .result { min-width: 160px; }
  .result .muted { color: var(--text-2); }
  .verdict { display: inline-block; margin-right: var(--space-1); padding: 0 var(--space-2); font-size: var(--fs-2); font-weight: 600; border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); }
  .verdict.used { color: var(--ok, var(--text)); }
  .verdict.rejected { color: var(--bad, var(--text)); }
  .why { display: block; margin-top: var(--space-1); color: var(--text-2); font-size: var(--fs-2); overflow-wrap: anywhere; }
  /* The drawer is about 600px wide: the columns share it, so the Result is always in view (wireframe w112). */
  .in-drawer th, .in-drawer td { padding: var(--space-2); }
  .in-drawer .file, .in-drawer .zone, .in-drawer .result { min-width: 0; }
  .in-drawer select { min-width: 0; }
  .in-drawer .file-name { font-size: var(--fs-1, var(--fs-2)); }
  /* A phone is too narrow for four columns: each file becomes a stacked block, the labels come from the column heads. */
  @media (max-width: 760px) {
    .card-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
  }
  @media (max-width: 560px) {
    .card-grid { grid-template-columns: minmax(0, 1fr); }
    .scroll { overflow-x: visible; border: 0; }
    table, tbody, tr, th, td { display: block; }
    thead { position: absolute; width: 1px; height: 1px; margin: -1px; overflow: hidden; clip: rect(0 0 0 0); }
    tbody tr { margin-bottom: var(--space-2); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
    tbody th.file { min-width: 0; border-bottom: 0; background: transparent; }
    th, td { border-bottom: 0; padding-top: var(--space-1); padding-bottom: var(--space-1); }
    td[data-label]::before { display: block; content: attr(data-label); color: var(--text-2); font-size: var(--fs-2); }
    .num { text-align: left; }
    .zone { min-width: 0; }
    select { min-width: 0; }
  }
</style>
