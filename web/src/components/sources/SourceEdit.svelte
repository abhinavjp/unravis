<script lang="ts">
  import { tick } from 'svelte';
  import { CaseApiError } from '../../lib/cases';
  import { postSourceTag, putSourceTag, type SourceTag } from '../../lib/caseTagsApi';
  import { saveHost, saveHostZone } from '../../lib/wizardHosts.svelte';
  import { zoneNameProblem, zoneSuggestions } from '../../lib/zoneChoice';
  import { zoneProblemText } from '../../lib/copy/zoneChoiceCopy';
  import { SOURCES_LIST_TEXT as T, zoneSharedText } from '../../lib/copy/sourcesListCopy';
  import type { ListedSource } from '../../lib/sourcesList';

  // The two inline edits of one source in the Sources list (wireframe w51 / srcItemHTML):
  //  - its Environment, Portal and API labels (a file nobody has tagged yet gets its tag on the first save);
  //  - the time zone of its computer, for a file read in that computer's clock. The zone belongs to the computer in Settings, so it changes
  //    for every file from it. A file whose rule was fixed when it was read (no rule, no computer) is still fixed by adding it again.
  let { caseId, row, envs, readOnly = false, onTag }: { caseId: string; row: ListedSource; envs: readonly string[]; readOnly?: boolean; onTag: (tag: SourceTag) => void } = $props();

  let editing = $state(false);
  let env = $state('');
  let portal = $state('');
  let api = $state('');
  let busy = $state(false);
  let note = $state('');
  let noteBad = $state(false);
  let zone = $state('');
  let zoneBusy = $state(false);
  let zoneNote = $state('');
  let zoneBad = $state(false);
  let envInput = $state<HTMLInputElement>();
  let openButton = $state<HTMLButtonElement>();

  const uid = $props.id();
  const zoneEditable = $derived(row.isFile && row.host !== '' && row.tz !== null && (row.tz.why === 'host' || row.tz.why === 'no-zone' || row.tz.why === 'host-gone'));
  const suggestions = zoneSuggestions();
  const dirty = $derived(env.trim() !== row.env || portal.trim() !== row.portal || api.trim() !== row.api);

  // Focus follows the editor: opening it lands on the first field, and closing it (Cancel or a saved edit) returns to the Edit button,
  // which is re-created when editing flips back, so it is focused after the render. A failed save keeps focus where it is.
  async function open() { env = row.env; portal = row.portal; api = row.api; note = ''; noteBad = false; editing = true; await tick(); envInput?.focus(); }
  async function close() { editing = false; note = ''; noteBad = false; await tick(); openButton?.focus(); }

  const failText = (error: unknown): string => {
    if (error instanceof CaseApiError) {
      if (error.status === 423) return T.labelsLocked;
      if (error.reason === 'bad-tag') return T.labelsBad;
      if (error.reason === 'case-closed') return T.labelsClosed;
      if (error.reason === 'source-not-found') return T.labelsGone;
    }
    return T.labelsFailed;
  };

  async function submit(event: Event) {
    event.preventDefault();
    if (busy || readOnly || !dirty) return;
    busy = true;
    note = '';
    const labels = { env: env.trim(), portal: portal.trim(), api: api.trim() };
    try {
      const saved = row.tagId > 0
        ? await putSourceTag(caseId, row.tagId, labels)
        : await postSourceTag(caseId, { importId: row.importId, kind: row.tagKind, label: row.tagLabel, ...labels, classification: '' });
      onTag(saved);
      editing = false;
      note = T.labelsSaved;
      noteBad = false;
      await tick();
      openButton?.focus();
    } catch (error) {
      note = failText(error);
      noteBad = true;
    } finally {
      busy = false;
    }
  }

  async function submitZone(event: Event) {
    event.preventDefault();
    if (zoneBusy || readOnly || !row.tz) return;
    const problem = zoneNameProblem(zone);
    if (problem !== '') { zoneNote = zoneProblemText(problem); zoneBad = true; return; }
    zoneBusy = true;
    zoneNote = '';
    const result = row.tz.why === 'host-gone' ? await saveHost(row.host, zone, false) : await saveHostZone(row.host, zone);
    zoneBusy = false;
    if (result.ok) { zoneNote = T.zoneSaved; zoneBad = false; zone = ''; }
    else { zoneNote = result.reason === 'locked' ? T.zoneLocked : result.reason === 'zone' ? zoneProblemText('unknown') : T.zoneFailed; zoneBad = true; }
  }
</script>

{#if !readOnly}
  <div class="edits" data-testid="srclist-edit">
    {#if editing}
      <form class="eform" onsubmit={submit} aria-label="{T.editLabels} {row.name}">
        <div class="field"><label for="{uid}-env">{T.environment}</label><input id="{uid}-env" bind:this={envInput} bind:value={env} list="{uid}-envs" maxlength="60" autocomplete="off" data-edit-env /></div>
        <div class="field"><label for="{uid}-portal">{T.portal}</label><input id="{uid}-portal" bind:value={portal} maxlength="80" autocomplete="off" data-edit-portal /></div>
        <div class="field"><label for="{uid}-api">{T.api}</label><input id="{uid}-api" bind:value={api} maxlength="80" autocomplete="off" data-edit-api /></div>
        <datalist id="{uid}-envs">{#each envs as name (name)}<option value={name}></option>{/each}</datalist>
        <div class="actions">
          <button type="submit" class="btn primary" disabled={busy || !dirty} data-edit-save>{busy ? T.saving : T.save}</button>
          <button type="button" class="btn" disabled={busy} onclick={close} data-edit-cancel>{T.cancel}</button>
        </div>
      </form>
    {:else}
      <div class="actions"><button type="button" class="btn" bind:this={openButton} onclick={open} data-edit-open>{T.editLabels}<span class="sr"> {row.name}</span></button></div>
    {/if}
    {#if note !== ''}<p class="say" class:bad={noteBad} role={noteBad ? 'alert' : 'status'} data-testid="srclist-edit-note">{note}</p>{/if}

    {#if zoneEditable && row.tz}
      <form class="eform" onsubmit={submitZone} aria-label="{T.zoneLabel} {row.host}">
        <div class="field">
          <label for="{uid}-zone">{T.zoneLabel}</label>
          <input id="{uid}-zone" bind:value={zone} list="{uid}-zones" maxlength="64" autocomplete="off" placeholder={row.tz.zone !== '' ? row.tz.zone : 'Asia/Dubai'} aria-invalid={zoneBad && zoneNote !== T.zoneSaved} data-edit-zone />
          <datalist id="{uid}-zones">{#each suggestions as name (name)}<option value={name}></option>{/each}</datalist>
        </div>
        <p class="hint">{zoneSharedText(row.host)}</p>
        <div class="actions"><button type="submit" class="btn" disabled={zoneBusy || zone.trim() === ''} data-edit-zone-save>{zoneBusy ? T.saving : T.zoneSave}</button></div>
        {#if zoneNote !== ''}<p class="say" class:bad={zoneBad} role={zoneBad ? 'alert' : 'status'} data-testid="srclist-zone-note">{zoneNote}</p>{/if}
      </form>
    {/if}
  </div>
{/if}

<style>
  .edits { display: grid; gap: var(--space-2); }
  .eform { display: grid; gap: var(--space-2); max-width: 28rem; }
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
</style>
