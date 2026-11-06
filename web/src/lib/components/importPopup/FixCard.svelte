<script lang="ts">
  import { tick } from 'svelte';
  import { POPUP_TEXT } from '../../copy/importPopupCopy';
  import { labelProblemText, saveFailText, zoneProblemText } from '../../copy/zoneChoiceCopy';
  import { saveHost } from '../../wizardHosts.svelte';
  import { labelProblem, zoneNameProblem } from '../../zoneChoice';

  // One guided fix card per server name (Check step, wireframe fixCardA and fixCardB). Open: add the computer and accept its files, leave the
  // files out, or add every missing server at once. A kind that writes its computer's own clock (`needsZone`) also asks for the name and the
  // time zone; any other kind keeps the server name as it is. The card reports the group key of its file list as `serverName`.
  // `totalServers` is the number of open server cards, for the "Add all" shortcut (hidden below two).
  let {
    card, kind, serverName, fileCount, files, selected = {}, needsZone = false, askName = false, suggestedZone = 'UTC', expanded = false, totalServers = 1,
    onResolve, onLeaveOut, onUndo, onAddAll, onToggle, onGroupAll, onSelect
  }: {
    card: 'open' | 'resolved' | 'leftout';
    kind: string;
    serverName: string;
    fileCount: number;
    files: { key: string; name: string; size: string }[];
    selected?: Record<string, boolean>;
    needsZone?: boolean;
    /** Asks for the computer's name even when the kind needs no time zone. */
    askName?: boolean;
    suggestedZone?: string;
    expanded?: boolean;
    totalServers?: number;
    onResolve?: (name: string, zone?: string) => void;
    onLeaveOut?: () => void;
    onUndo?: () => void;
    onAddAll?: () => void;
    onToggle?: (key: string) => void;
    onGroupAll?: (key: string) => void;
    onSelect?: (key: string, on: boolean) => void;
  } = $props();

  const COMMON_ZONES = ['UTC', 'Europe/London', 'Europe/Paris', 'America/New_York', 'America/Chicago', 'America/Denver', 'America/Los_Angeles', 'Asia/Dubai', 'Asia/Kolkata', 'Asia/Tokyo', 'Australia/Sydney'];

  const uid = $props.id();
  const nameId = uid + '-name';
  const hintId = uid + '-hint';
  const zoneId = uid + '-zone';

  let typedName = $state<string | null>(null);
  let typedZone = $state<string | null>(null);
  let saving = $state(false);
  let failure = $state('');
  let touched = $state(false);
  let nameEl = $state<HTMLInputElement>();
  let primaryEl = $state<HTMLButtonElement>();
  let undoEl = $state<HTMLButtonElement>();
  let allEl = $state<HTMLInputElement>();

  const name = $derived((typedName ?? serverName).trim());
  const zone = $derived(typedZone ?? suggestedZone);
  const zones = $derived(COMMON_ZONES.includes(zone) ? COMMON_ZONES : [...COMMON_ZONES, zone]);
  const nameBad = $derived(touched && (needsZone || askName) ? labelProblemText(labelProblem(name, null)) : '');
  const zoneBad = $derived(touched && needsZone ? zoneProblemText(zoneNameProblem(zone)) : '');
  const chosen = $derived(files.filter((file) => selected[file.key]).length);
  const word = $derived(files.length === 1 ? ' file' : ' files');
  const leftOutHelp = $derived(needsZone || askName ? 'These files were not given a computer.' : 'These files from ' + serverName + ' were not added.');

  $effect(() => {
    if (allEl) allEl.indeterminate = chosen > 0 && chosen < files.length;
  });

  /** Puts the focus on the card's first control: the name field, or the primary button. Used when the popup opens on a retried server. */
  export async function focus(): Promise<void> {
    await tick();
    (card === 'open' ? (nameEl ?? primaryEl) : undoEl)?.focus();
  }

  async function resolve() {
    touched = true;
    failure = '';
    if ((needsZone || askName) && labelProblem(name, null) !== '') return;
    if (needsZone && zoneNameProblem(zone) !== '') return;
    saving = true;
    const result = await saveHost(name, needsZone ? zone : '', !needsZone);
    saving = false;
    // A name already in the list is the goal reached: the server accepts it, so the files are accepted too.
    if (!result.ok && !(result.reason === 'taken' && !needsZone)) {
      failure = saveFailText(result.reason);
      return;
    }
    onResolve?.(name, needsZone ? zone : undefined);
    void tick().then(() => undoEl?.focus());
  }
</script>

{#snippet warnIcon()}
  <svg class="ic" width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M8 2.5l6 10.5H2L8 2.5z" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round" /><path d="M8 7v3" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" /><circle cx="8" cy="11.4" r=".8" fill="currentColor" /></svg>
{/snippet}

{#snippet fileList()}
  <div class="grp" class:open={expanded}>
    <button type="button" class="grp-head" aria-expanded={expanded} onclick={() => onToggle?.(serverName)}>
      <span class="chev" aria-hidden="true"></span><span class="grp-meta">{files.length}{word}</span>
    </button>
    {#if expanded}
      <div class="grp-files">
        <label class="grp-all">
          <input type="checkbox" bind:this={allEl} checked={chosen === files.length && files.length > 0} onchange={() => onGroupAll?.(serverName)} />
          <span>All {files.length}{word}</span>
        </label>
        {#each files as file (file.key)}
          <div class="file-row">
            <label class="file-sel"><input type="checkbox" aria-label={file.name} checked={!!selected[file.key]} onchange={(event) => onSelect?.(file.key, event.currentTarget.checked)} /></label>
            <span class="file-name">{file.name}</span>
            <span class="file-size">{file.size}</span>
          </div>
        {/each}
      </div>
    {/if}
  </div>
{/snippet}

<div class="fix" class:resolved={card === 'resolved'} data-card={card}>
  {#if card === 'resolved'}
    <div class="fix-head">
      <div class="resolved-line">
        <svg class="ic" width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true"><circle cx="8" cy="8" r="6.5" stroke="currentColor" stroke-width="1.3" /><path d="M5.2 8.1l1.9 1.9 3.7-3.9" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg>
        <span>{POPUP_TEXT.fixResolved(serverName, fileCount)}</span>
      </div>
    </div>
    <div class="fix-body">
      <div class="fix-actions">
        <button type="button" class="btn btn-quiet" bind:this={undoEl} onclick={() => onUndo?.()}>
          <svg class="ic" width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M4 6h6a3.5 3.5 0 010 7H6" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" /><path d="M6 4L3 6l3 2" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" /></svg>
          {POPUP_TEXT.fixUndo}
        </button>
      </div>
    </div>
    {@render fileList()}
  {:else if card === 'leftout'}
    <div class="fix-head">
      <div class="fix-title"><span class="muted">{@render warnIcon()}</span> {POPUP_TEXT.fixLeftOut(fileCount, kind)}</div>
      <p class="fix-help">{leftOutHelp} <button type="button" class="linkbtn" bind:this={undoEl} onclick={() => onUndo?.()}>{POPUP_TEXT.fixBringBack}</button></p>
    </div>
  {:else if !needsZone && !askName}
    <div class="fix-head">
      <div class="fix-title"><span class="warn">{@render warnIcon()}</span> {fileCount === 0 ? serverName + ' is not on your computer list.' : POPUP_TEXT.fixTitleNeedsComputer(fileCount, serverName, kind)}</div>
      <p class="fix-help">{fileCount === 0 ? 'Add this computer, then choose the file again.' : POPUP_TEXT.fixHelpNeedsComputer}</p>
      {#if fileCount > 0}<div class="fix-note">{POPUP_TEXT.fixNoteIis}</div>{/if}
    </div>
    <div class="fix-body">
      <div class="fix-actions">
        <button type="button" class="btn btn-primary" bind:this={primaryEl} disabled={saving} onclick={resolve}>{fileCount === 0 ? 'Add ' + serverName + ' as a computer' : POPUP_TEXT.fixAccept(serverName, fileCount)}</button>
        {#if fileCount > 0}<button type="button" class="linkbtn" disabled={saving} onclick={() => onLeaveOut?.()}>{POPUP_TEXT.fixLeaveOut(fileCount)}</button>{/if}
        {#if totalServers > 1}
          <button type="button" class="linkbtn" disabled={saving} onclick={() => onAddAll?.()}>{POPUP_TEXT.fixAddAll(totalServers)}</button>
        {/if}
      </div>
      {#if failure}<p class="fail" role="alert">{failure}</p>{/if}
    </div>
    {#if fileCount > 0}{@render fileList()}{/if}
  {:else}
    <div class="fix-head">
      <div class="fix-title"><span class="warn">{@render warnIcon()}</span> {POPUP_TEXT.fixNoComputerTitle(fileCount, kind)}</div>
    </div>
    <div class="fix-body">
      <div class="field">
        <label for={nameId}>{POPUP_TEXT.fixNameLabel}</label>
        <input type="text" id={nameId} bind:this={nameEl} value={typedName ?? serverName} aria-describedby={hintId} aria-invalid={nameBad !== ''} disabled={saving} oninput={(event) => (typedName = event.currentTarget.value)} />
        <span class="hint" id={hintId}>{POPUP_TEXT.fixNameHint}</span>
        {#if nameBad}<span class="fail" role="alert">{nameBad}</span>{/if}
      </div>
      {#if needsZone}
      <div class="tz-row">
        <div class="field">
          <label for={zoneId}>{POPUP_TEXT.fixZoneLabel}</label>
          <select id={zoneId} value={zone} aria-invalid={zoneBad !== ''} disabled={saving} onchange={(event) => (typedZone = event.currentTarget.value)}>
            {#each zones as option (option)}<option value={option}>{option}</option>{/each}
          </select>
          {#if zoneBad}<span class="fail" role="alert">{zoneBad}</span>{/if}
        </div>
      </div>
      <p class="fix-help">{POPUP_TEXT.fixZoneHelp}</p>
      {/if}
      <div class="fix-actions">
        <button type="button" class="btn btn-primary" bind:this={primaryEl} disabled={saving} onclick={resolve}>{needsZone || name === '' ? POPUP_TEXT.fixUseName(name, fileCount) : POPUP_TEXT.fixAccept(name, fileCount)}</button>
        <button type="button" class="linkbtn" disabled={saving} onclick={() => onLeaveOut?.()}>{POPUP_TEXT.fixLeaveOut(fileCount)}</button>
      </div>
      {#if failure}<p class="fail" role="alert">{failure}</p>{/if}
    </div>
    {@render fileList()}
  {/if}
</div>

<style>
  .fix { border: 1px solid var(--line); border-left: 3px solid var(--warn); border-radius: var(--radius); margin-bottom: 10px; background: color-mix(in srgb, var(--warn) 5%, var(--surface)); }
  .fix.resolved { border-left-color: var(--tone-ok); background: color-mix(in srgb, var(--tone-ok) 5%, var(--surface)); }
  .fix-head { padding: 12px 14px 4px; }
  .fix-title { font-size: 13px; font-weight: 600; display: flex; align-items: flex-start; gap: 8px; }
  .fix-title .warn { color: var(--warn); }
  .fix-title .muted { color: var(--text-2); }
  .fix-help { margin: 6px 0 0; color: var(--text-2); font-size: 12px; line-height: 1.5; }
  .fix-note { margin: 8px 0 0; padding: 6px 10px; color: var(--text-2); font-size: 12px; background: var(--surface-2); border: 1px solid var(--line); border-radius: var(--radius); }
  .fix-body { padding: 0 14px 12px; display: grid; gap: 10px; }
  .fix-actions { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; }
  .resolved-line { display: flex; align-items: center; gap: 8px; color: var(--tone-ok); font-size: 13px; font-weight: 600; }
  .resolved-line svg { flex: 0 0 auto; }
  .fail { margin: 0; color: var(--bad); font-size: 12px; }

  .btn { display: inline-flex; align-items: center; justify-content: center; gap: 8px; border-radius: var(--radius); padding: 8px 14px; font-size: 13px; font-weight: 600; min-height: 36px; cursor: pointer; transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease), color var(--dur) var(--ease); }
  .btn-primary { color: var(--accent-ink); background: var(--accent); border: 1px solid var(--accent); }
  .btn-primary:hover:not(:disabled) { background: color-mix(in srgb, var(--accent) 88%, var(--text)); }
  .btn-primary:disabled { color: var(--text-2); background: var(--surface-2); border-color: var(--control-line); cursor: default; }
  .btn-quiet { color: var(--text-2); background: transparent; border: 1px solid transparent; padding: 6px 10px; font-size: 12px; font-weight: 500; }
  .btn-quiet:hover:not(:disabled) { color: var(--text); background: var(--surface-2); }
  .linkbtn { background: none; border: 0; color: var(--accent); font: inherit; font-size: 13px; padding: 0; text-decoration: underline; text-underline-offset: 3px; cursor: pointer; }
  .linkbtn:hover:not(:disabled) { color: var(--text); }
  .linkbtn:disabled { color: var(--text-2); cursor: default; }

  .field { display: grid; gap: 4px; }
  .field label { color: var(--text-2); font-size: 12px; font-weight: 500; }
  .field .hint { color: var(--text-2); font-size: 12px; font-weight: 400; }
  input[type='text'], select { width: 100%; min-width: 0; color: var(--text); background: var(--bg); border: 1px solid var(--control-line); border-radius: var(--radius); padding: 7px 9px; font-size: 13px; }
  input[type='text']:hover:not(:disabled), select:hover:not(:disabled) { border-color: var(--text-2); }
  select { appearance: auto; }
  .tz-row { display: grid; grid-template-columns: 1fr; gap: 4px; }
  @media (min-width: 560px) { .tz-row { grid-template-columns: 1fr 1fr; gap: 10px; } }

  .grp { border: 0; border-top: 1px solid var(--line-soft); margin: 0; background: transparent; }
  .grp-head { display: flex; align-items: center; gap: 10px; width: 100%; padding: 8px 14px; background: none; border: 0; text-align: left; color: var(--text); font-size: 13px; cursor: pointer; }
  .grp-head:hover { background: var(--surface-2); }
  .chev { flex: 0 0 auto; width: 8px; height: 8px; border-right: 1.5px solid var(--text-2); border-bottom: 1.5px solid var(--text-2); transform: rotate(-45deg) translateY(-1px); transition: transform var(--dur) var(--ease); }
  .grp.open .chev { transform: rotate(45deg) translateY(-1px); }
  .grp-meta { color: var(--text-2); font-size: 12px; font-family: var(--mono); }
  .grp-files { padding: 6px 8px 8px; }
  .grp-all { display: flex; align-items: center; gap: 8px; padding: 5px 4px; color: var(--text-2); font-size: 12px; border-bottom: 1px solid var(--line-soft); margin-bottom: 2px; }
  .grp-all input, .file-sel input { accent-color: var(--accent); width: 15px; height: 15px; }
  .file-row { display: flex; align-items: center; gap: 10px; padding: 5px 4px; border-radius: var(--radius); }
  .file-row:hover { background: var(--surface-2); }
  .file-sel { display: flex; align-items: center; flex: 0 0 auto; }
  .file-name { font-family: var(--mono); font-size: 12px; overflow-wrap: anywhere; min-width: 0; }
  .file-size { flex: 0 0 auto; margin-left: auto; color: var(--text-2); font-family: var(--mono); font-size: 12px; white-space: nowrap; }
</style>
