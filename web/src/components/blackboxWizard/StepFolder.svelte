<script lang="ts">
  import { onMount } from 'svelte';
  import { defaultSettings, folderKind, listensOnAll } from '../../lib/blackbox';
  import { eligibleDrives, fetchDrives } from '../../lib/blackboxDrives';
  import type { DriveInfo } from '../../lib/blackboxDrives';
  import {
    applyDefaultFolder,
    defaultFolder,
    driveOfPath,
    resetFolder,
    setFolder,
    stepProblem
  } from '../../lib/blackboxWizard';
  import type { WizardState } from '../../lib/blackboxWizard';
  import { BB_TEXT } from '../../lib/copy/blackboxCopy';
  import { BBW_TEXT as W } from '../../lib/copy/blackboxWizardCopy';
  import RadioCards from './RadioCards.svelte';

  /**
   * The Folder step (TSK-15, rule S-11). One editable full path is always visible. On this PC the drives of the machine
   * are cards above it; on the pack path there are no cards, because Unravis cannot see the other server. Port and
   * "who can connect" sit under Advanced (OD-8). All rules come from blackboxWizard.ts and blackbox.ts; this file only
   * shows them and writes the answers back into the one WizardState.
   *
   * Props: `wizard` (bound) is the wizard state. `attempted` is true once Next was refused on this step, so the
   * problem is shown even before the owner has touched the field.
   */
  let { wizard = $bindable(), attempted = false }: { wizard: WizardState; attempted?: boolean } = $props();

  const FALLBACK_FOLDER = defaultSettings().folder;
  const GB = 1024 ** 3;
  const TB = 1024 ** 4;

  let drives = $state<DriveInfo[] | null>(null);
  let driveStatus = $state<'idle' | 'loading' | 'ready' | 'failed'>('idle');
  let advancedOpen = $state(false);
  let folderLeft = $state(false);
  let folderInput = $state<HTMLInputElement | null>(null);
  // The port as typed. An unreadable entry is kept here and sent to the wizard as 0, which the port rule refuses.
  let portText = $state(String(wizard.settings.port));

  const idBase = $props.id();
  const folderId = idBase + '-folder';
  const folderHelpId = idBase + '-folder-help';
  const folderErrorId = idBase + '-folder-error';
  const portId = idBase + '-port';
  const portHelpId = idBase + '-port-help';
  const portErrorId = idBase + '-port-error';
  const bindId = idBase + '-bind';
  const bindHelpId = idBase + '-bind-help';
  const bindErrorId = idBase + '-bind-error';

  const isPc = $derived(wizard.where === 'pc');
  const folder = $derived(wizard.settings.folder);
  const letter = $derived(driveOfPath(folder));
  const systemLetter = $derived((wizard.systemDrive ?? 'C').toUpperCase());
  const cards = $derived(isPc && driveStatus === 'ready' && drives ? eligibleDrives(drives) : []);
  const cardLetters = $derived(cards.map((d) => d.letter.toUpperCase()));
  const defaultPath = $derived(defaultFolder(isPc ? drives : null, FALLBACK_FOLDER));
  const defaultLetter = $derived(driveOfPath(defaultPath.folder));
  const selectedLetter = $derived(letter !== null && cardLetters.includes(letter) ? letter : null);
  const onSystemDrive = $derived(letter !== null && letter === systemLetter);
  const canReset = $derived(wizard.folderTouched && folder.trim().toLowerCase() !== defaultPath.folder.toLowerCase());

  // The current problem of this step, shown in the place that owns it.
  const problem = $derived(stepProblem(wizard, 'folder'));
  // Each folder fault says what is wrong: not a full path, forward slashes, a character Windows refuses, or a system folder.
  const FOLDER_WORDS: Record<string, string> = {
    'folder-invalid': W['folder.errFull'],
    'folder-slash': W['folder.errSlash'],
    'folder-chars': W['folder.errChars'],
    'folder-unsafe': W['folder.errSystem']
  };
  const folderProblem = $derived(
    problem !== null && problem in FOLDER_WORDS ? (attempted || folderLeft ? FOLDER_WORDS[problem] : null) : null
  );
  const tickProblem = $derived(problem === 'folder-system-drive' && attempted);
  const portProblem = $derived(problem === 'port' ? W['folder.errPort'] : null);
  const bindProblem = $derived(problem === 'bind' ? BB_TEXT.settingProblems.bind : null);

  // One notice at most, about the folder as it stands now.
  const notice = $derived.by((): string | null => {
    const kind = folderKind(folder);
    if (kind === 'unc') return W['notice.networkDrive'];
    if (kind !== 'drive' || letter === null) return null;
    if (letter === systemLetter) return W['notice.systemDrive'](systemLetter);
    const card = drives?.find((d) => d.letter.toUpperCase() === letter);
    if (card?.type === 'network') return W['notice.networkDrive'];
    if (card?.type === 'removable') return W['notice.usbDrive'];
    if (isPc && driveStatus === 'ready' && !cardLetters.includes(letter)) return W['notice.unlistedDrive'](letter);
    return null;
  });

  onMount(() => {
    let alive = true;
    if (wizard.where === 'pc') {
      driveStatus = 'loading';
      fetchDrives().then(
        (answer) => {
          if (!alive) return;
          drives = answer.drives;
          driveStatus = 'ready';
          wizard = applyDefaultFolder(wizard, answer.drives, FALLBACK_FOLDER);
        },
        () => {
          if (!alive) return;
          driveStatus = 'failed';
          wizard = applyDefaultFolder(wizard, null, FALLBACK_FOLDER);
        }
      );
    } else {
      wizard = applyDefaultFolder(wizard, null, FALLBACK_FOLDER);
    }
    return () => { alive = false; };
  });

  // A refused Next on a wrong port or address must be seen, so Advanced opens for it. The wizard shell also opens
  // a closed Advanced before it moves focus to the field (focusProblem), for a Next pressed after it was shut again.
  $effect(() => {
    void attempted;
    if (problem === 'port' || problem === 'bind') advancedOpen = true;
  });

  function formatBytes(bytes: number): string {
    return bytes >= TB ? (bytes / TB).toFixed(1) + ' TB' : Math.round(bytes / GB) + ' GB';
  }

  function describe(drive: DriveInfo): string {
    const parts: string[] = [];
    const l = drive.letter.toUpperCase();
    if (l === defaultLetter && !defaultPath.needsSystemTick) parts.push(W['folder.pickedForYou']);
    if (drive.system) parts.push(W['folder.windowsDrive']);
    else if (drive.type === 'removable') parts.push(W['folder.driveUsb']);
    else if (drive.type === 'network') parts.push(W['folder.driveNetwork']);
    parts.push(
      drive.freeBytes !== null && drive.totalBytes !== null
        ? W['folder.space'](formatBytes(drive.freeBytes), formatBytes(drive.totalBytes))
        : W['folder.spaceUnknown']
    );
    return parts.join(' · ');
  }

  const options = $derived(
    cards.map((d) => ({
      value: d.letter.toUpperCase(),
      title: d.letter.toUpperCase() + ': ' + d.label,
      description: describe(d)
    }))
  );

  /** Picking a card changes the drive letter only; the rest of the typed path stays. */
  function pickDrive(next: string | null): void {
    if (next === null || next === selectedLetter) return;
    const m = /^[A-Za-z]:(.*)$/s.exec(folder.trim());
    let rest = m ? m[1] : '';
    if (!/^[\\/]+[^\\/]/.test(rest)) rest = '\\' + FALLBACK_FOLDER.slice(3);
    wizard = setFolder(wizard, next + ':' + rest);
  }

  function typeFolder(value: string): void {
    wizard = setFolder(wizard, value);
  }

  function resetToDefault(): void {
    wizard = resetFolder(wizard, isPc ? drives : null, FALLBACK_FOLDER);
    folderLeft = false;
    folderInput?.focus();
  }

  function tick(checked: boolean): void {
    wizard = { ...wizard, confirmSystemDrive: checked };
  }

  function typePort(value: string): void {
    portText = value;
    const text = value.trim();
    wizard = { ...wizard, settings: { ...wizard.settings, port: /^\d{1,5}$/.test(text) ? Number(text) : 0 } };
  }

  function typeBind(value: string): void {
    wizard = { ...wizard, settings: { ...wizard.settings, bind: value } };
  }
</script>

<div class="step">
  {#if isPc}
    {#if driveStatus === 'loading'}
      <p class="note" role="status">{W['folder.drivesLoading']}</p>
    {:else if driveStatus === 'failed'}
      <p class="note" role="status">{W['folder.drivesFailed']}</p>
    {:else if driveStatus === 'ready'}
      {#if options.length > 0}
        <div class="cards">
          <RadioCards legend={W['folder.drive']} {options} bind:value={() => selectedLetter, pickDrive} />
          {#if defaultPath.needsSystemTick}
            <p class="note">{W['folder.noOtherDrive'](systemLetter)}</p>
          {/if}
        </div>
      {:else}
        <p class="note" role="status">{W['folder.noDrives']}</p>
      {/if}
    {/if}
  {/if}

  <div class="field">
    <label for={folderId}>{isPc ? W['folder.pathPc'] : W['folder.pathPack']}</label>
    <input
      id={folderId}
      data-autofocus
      class="path"
      type="text"
      value={folder}
      bind:this={folderInput}
      spellcheck="false"
      autocomplete="off"
      aria-invalid={folderProblem ? 'true' : undefined}
      aria-describedby={folderHelpId +(folderProblem ? ' ' + folderErrorId : '')}
      oninput={(e) => typeFolder(e.currentTarget.value)}
      onblur={() => { folderLeft = true; }}
    />
    <p class="hint" id={folderHelpId}>
      {isPc ? W['folder.pathHelpPc'] : W['folder.pathHelpPack']}
      {#if canReset}
        <button type="button" class="link" onclick={resetToDefault}>{W['folder.reset'](defaultPath.folder)}</button>
      {/if}
    </p>
    {#if folderProblem}
      <p class="error" role="alert" id={folderErrorId}>{folderProblem}</p>
    {/if}
    {#if notice}
      <p class="notice" role="note">{notice}</p>
    {/if}
  </div>

  {#if onSystemDrive}
    <div class="confirm">
      <label class="tick">
        <input type="checkbox" checked={wizard.confirmSystemDrive} onchange={(e) => tick(e.currentTarget.checked)} aria-describedby={idBase + '-tick-help'} />
        <span>{W['folder.confirmC'](systemLetter)}</span>
      </label>
      <p class="hint" id={idBase + '-tick-help'}>{W['folder.confirmCHelp']}</p>
      {#if tickProblem}
        <p class="error" role="alert">{W['folder.confirmCError'](systemLetter)}</p>
      {/if}
    </div>
  {/if}

  <details class="advanced" bind:open={advancedOpen}>
    <summary>{W['folder.advanced']}</summary>
    <div class="advanced-body">
      <div class="field">
        <label for={portId}>{W['folder.port']}</label>
        <input
          id={portId}
          class="short"
          type="text"
          inputmode="numeric"
          value={portText}
          autocomplete="off"
          aria-invalid={portProblem ? 'true' : undefined}
          aria-describedby={portHelpId + (portProblem ? ' ' + portErrorId : '')}
          oninput={(e) => typePort(e.currentTarget.value)}
        />
        <p class="hint" id={portHelpId}>{W['folder.portHelp']}</p>
        {#if portProblem}<p class="error" role="alert" id={portErrorId}>{portProblem}</p>{/if}
      </div>

      <div class="field">
        <label for={bindId}>{W['folder.bind']}</label>
        <input
          id={bindId}
          class="medium"
          type="text"
          value={wizard.settings.bind}
          spellcheck="false"
          autocomplete="off"
          aria-invalid={bindProblem ? 'true' : undefined}
          aria-describedby={bindHelpId + (bindProblem ? ' ' + bindErrorId : '')}
          oninput={(e) => typeBind(e.currentTarget.value)}
        />
        <p class="hint" id={bindHelpId}>
          {wizard.settings.bind.trim() === '127.0.0.1' ? W['folder.bindSafe'] : W['folder.bindOther']}
        </p>
        {#if bindProblem}<p class="error" role="alert" id={bindErrorId}>{bindProblem}</p>{/if}
        {#if listensOnAll(wizard.settings.bind)}
          <p class="notice" role="note">{W['folder.bindWarning']}</p>
        {/if}
      </div>
    </div>
  </details>
</div>

<style>
  .step { display: grid; gap: var(--space-4); min-width: 0; }
  .cards { display: grid; gap: var(--space-2); }
  .field { display: grid; gap: var(--space-1); min-width: 0; }
  label { color: var(--text); font-weight: 600; }
  input[type='text'] { box-sizing: border-box; width: 100%; min-height: 44px; padding: var(--space-2) var(--space-3); border: 1px solid var(--control-line); border-radius: var(--radius-sm); background: var(--surface); color: var(--text); font: inherit; }
  input[type='text'].path { font-family: var(--mono); }
  input[type='text'].short { max-width: 140px; }
  input[type='text'].medium { max-width: 320px; }
  input[type='text']:focus-visible, input[type='checkbox']:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  input[aria-invalid='true'] { border-color: var(--bad); }
  .hint, .note { margin: 0; color: var(--text-2); overflow-wrap: anywhere; }
  .error { margin: 0; color: var(--bad); font-weight: 600; overflow-wrap: anywhere; }
  .notice { margin: 0; padding-left: var(--space-3); border-left: 3px solid var(--warn); color: var(--text); overflow-wrap: anywhere; }
  .link { display: inline-flex; align-items: center; min-width: 40px; min-height: 44px; padding: 0 var(--space-1); border: 0; background: none; color: var(--accent); font: inherit; text-decoration: underline; text-underline-offset: 3px; cursor: pointer; }
  .link:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius-sm); }
  .confirm { display: grid; gap: var(--space-1); padding: var(--space-3); border: 1px solid var(--warn); border-radius: var(--radius-sm); }
  .tick { display: flex; align-items: center; gap: var(--space-2); min-height: 44px; font-weight: 600; cursor: pointer; }
  .tick input { flex: 0 0 auto; width: 20px; height: 20px; margin: 0; accent-color: var(--accent); }
  .advanced summary { display: flex; align-items: center; width: fit-content; min-height: 44px; color: var(--accent); cursor: pointer; text-decoration: underline; text-underline-offset: 3px; }
  .advanced summary:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; border-radius: var(--radius-sm); }
  .advanced-body { display: grid; gap: var(--space-4); padding-top: var(--space-2); }
</style>
