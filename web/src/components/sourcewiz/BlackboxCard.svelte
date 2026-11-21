<script lang="ts">
  import { onMount } from 'svelte';
  import { timeZone } from '../../lib/timeZone.svelte';
  import { formatTime } from '../../lib/time';
  import { blackboxApi, BlackboxError } from '../../lib/blackbox';
  import { recorderEntryName } from '../../lib/wizardBlackbox';
  import type { FolderCandidate, ThisPc } from '../../lib/blackbox';
  import { wizard, setSources } from '../../lib/wizard.svelte';
  import { addSource, makeSource } from '../../lib/wizardSources';
  import { openSidePanel } from '../../lib/appState.svelte';
  import { WIZ } from '../../lib/copy/sourceWizardCopy';
  import OpenFolderButton from './OpenFolderButton.svelte';

  // Blackbox recorder card. Recorder ON: the folder comes from the service. OFF: folders on the drives, newest log first, the newest one preselected.
  // "Check it works" tests the folder; "Add this recorder" saves it and puts it in the list of sources.
  let { onDone }: { onDone: () => void } = $props();

  const ANOTHER = '__another__';
  const uid = $props.id();
  let loading = $state(true);
  let folders = $state<FolderCandidate[]>([]);
  let complete = $state(true);
  let fromService = $state(false);
  let choice = $state('');
  let typed = $state('');
  let name = $state('');
  let checking = $state(false);
  let verdict = $state<'' | 'works' | 'failed'>('');
  let problem = $state('');
  let saving = $state(false);
  let line = $state('');
  let pcInfo = $state<ThisPc | undefined>();

  const location = $derived((choice === ANOTHER ? typed : choice).trim());

  async function load() {
    loading = true; problem = ''; verdict = '';
    try {
      const pc = await blackboxApi.thisPc();
      pcInfo = pc;
      if (pc.installed && pc.running && pc.folder) {
        fromService = true;
        folders = [{ path: pc.folder, drive: '', recordings: 0, newest: '', fromService: true, recommended: true }];
        complete = true;
      } else {
        fromService = false;
        const answer = await blackboxApi.folders();
        folders = answer.folders;
        complete = answer.complete;
      }
      const best = folders.find((f) => f.recommended) ?? folders[0];
      choice = best ? best.path : ANOTHER;
    } catch (e) {
      problem = e instanceof BlackboxError ? e.refusal.error : 'Unravis could not look for recorder folders.';
      choice = ANOTHER;
    }
    if (name.trim() === '') name = 'This PC';
    loading = false;
  }
  onMount(() => { void load(); });

  async function check() {
    if (location === '' || checking) return;
    const asked = location;
    checking = true; verdict = ''; problem = '';
    // The folder can change while the check runs: an answer about an older folder is dropped.
    try { const r = await blackboxApi.test({ name: name.trim() || 'Recorder', location: asked, key: '' }); if (location === asked) verdict = r.ok ? 'works' : 'failed'; }
    catch (e) { if (location === asked) { verdict = 'failed'; problem = e instanceof BlackboxError ? e.refusal.error : ''; } }
    checking = false;
  }

  async function add() {
    const label = name.trim();
    if (label === '' || location === '' || saving) { problem = label === '' ? WIZ.blackbox.nameHint : ''; return; }
    saving = true; problem = '';
    try {
      const saved = await recorderEntryName(pcInfo && location === pcInfo.folder ? pcInfo : { listed: false }, location, label);
      const result = addSource(wizard.sources, makeSource('blackbox', saved, saved));
      if (!result.ok) { problem = WIZ.blackbox.notListed; saving = false; return; }
      setSources(result.list);
      line = WIZ.blackbox.added;
      onDone();
    } catch (e) {
      problem = e instanceof BlackboxError ? e.refusal.error : 'The recorder could not be added.';
    }
    saving = false;
  }

  const ageLine = (f: FolderCandidate) => [f.recordings > 0 ? WIZ.blackbox.recordings(f.recordings) : '', f.newest ? WIZ.blackbox.newest + ': ' + formatTime(f.newest, { zone: timeZone.zone, precision: 'minute', year: true, fallback: f.newest }) : ''].filter(Boolean).join(' · ');
</script>

<div class="bb">
  <h3>{WIZ.blackbox.heading}</h3>
  <p class="hint">{WIZ.blackbox.intro}</p>
  {#if loading}<p class="hint" role="status">{WIZ.blackbox.searching}</p>
  {:else}
    {#if fromService}<p class="hint">{WIZ.blackbox.recorderOn}</p>{/if}
    {#if folders.length === 0}<p class="hint">{WIZ.blackbox.none}</p>{/if}
    {#if !complete}<p class="hint">{WIZ.blackbox.incomplete}</p>{/if}
    <fieldset class="picks">
      <legend>{WIZ.blackbox.pickHeading}</legend>
      {#each folders as f (f.path)}
        <div class="opt">
          <label><input type="radio" name="{uid}-folder" value={f.path} bind:group={choice} onchange={() => { verdict = ''; }} />
            <span class="path">{f.path}</span>{#if f.recommended}<span class="tag">{WIZ.blackbox.recommended}</span>{/if}
            {#if ageLine(f)}<span class="hint">{ageLine(f)}</span>{/if}
          </label>
          <OpenFolderButton target={{ path: f.path }} what={f.path} onLine={(l) => { problem = l; }} />
        </div>
      {/each}
      <div class="opt">
        <label><input type="radio" name="{uid}-folder" value={ANOTHER} bind:group={choice} onchange={() => { verdict = ''; }} /> {WIZ.blackbox.another}</label>
      </div>
      {#if choice === ANOTHER}
        <label class="field" for="{uid}-typed">{WIZ.blackbox.anotherLabel}</label>
        <div class="opt"><input id="{uid}-typed" type="text" class="text-input" bind:value={typed} oninput={() => { verdict = ''; }} spellcheck="false" autocomplete="off" aria-describedby="{uid}-th" />
          {#if typed.trim() !== ''}<OpenFolderButton target={{ path: typed.trim() }} what={typed.trim()} onLine={(l) => { problem = l; }} />{/if}</div>
        <p class="hint" id="{uid}-th">{WIZ.blackbox.anotherHint}</p>
      {/if}
    </fieldset>

    <label class="field" for="{uid}-name">{WIZ.blackbox.nameLabel}</label>
    <input id="{uid}-name" type="text" class="text-input" bind:value={name} maxlength="60" autocomplete="off" aria-describedby="{uid}-nh" />
    <p class="hint" id="{uid}-nh">{WIZ.blackbox.nameHint}</p>

    <div class="actions">
      <button type="button" class="secondary-button" disabled={location === '' || checking} onclick={() => void check()}>{checking ? WIZ.blackbox.checking : WIZ.blackbox.check}</button>
      {#if verdict === 'works'}<span class="verdict ok" role="status">{WIZ.blackbox.works}</span>{:else if verdict === 'failed'}<span class="verdict bad" role="status">{WIZ.blackbox.failed}</span>{/if}
      <button type="button" class="primary-button" disabled={location === '' || name.trim() === '' || saving} onclick={() => void add()}>{WIZ.blackbox.add}</button>
      <button type="button" class="secondary-button" disabled={loading} onclick={() => void load()}>{WIZ.blackbox.again}</button>
    </div>
    {#if problem !== ''}<p class="inline-error" role="alert">{problem}</p>{/if}
    {#if line !== ''}<p class="hint" role="status">{line}</p>{/if}
    <p class="hint"><button type="button" class="quiet-button" onclick={() => openSidePanel('blackbox')}>{WIZ.blackbox.placeNew}</button> {WIZ.blackbox.placeNewNote}</p>
  {/if}
</div>

<style>
  .bb { display: grid; gap: var(--space-3); min-width: 0; }
  h3 { margin: 0; font-size: var(--fs-5); font-weight: 600; }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .picks { display: grid; gap: var(--space-2); margin: 0; padding: 0; border: 0; min-width: 0; }
  legend { padding: 0; font-weight: 600; }
  .opt { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; }
  .opt label { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; min-width: 0; }
  .path { font-family: var(--mono); overflow-wrap: anywhere; }
  .tag, .verdict { padding: 0 var(--space-2); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font-size: var(--fs-2); }
  .verdict.ok { font-weight: 600; } .verdict.bad { font-weight: 600; color: var(--danger, inherit); }
  .field { font-weight: 600; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; }
  input[type='radio'] { accent-color: var(--accent); width: 20px; height: 20px; }
</style>
