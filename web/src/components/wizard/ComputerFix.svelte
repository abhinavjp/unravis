<script lang="ts">
  import { hostNeed } from '../../lib/fileKinds';
  import { saveHost } from '../../lib/wizardHosts.svelte';
  import { labelProblem, zoneNameProblem, zoneSuggestions } from '../../lib/zoneChoice';
  import { COMPUTER_FIX_TEXT, labelProblemText, saveFailText, zoneProblemText } from '../../lib/copy/zoneChoiceCopy';

  // The one-click "Add this computer" for a file whose server is not on the computer list. The create screen and the import drawer use it, the
  // same way the import popup's fix card does. The server name comes from the file itself. A kind that writes its computer's own clock also asks
  // for the time zone. `onFixed` gets the name in the list: the caller picks it for the file and tries the file again.
  let { kind, serverName, fileName, disabled = false, onFixed }: { kind: string; serverName: string; fileName: string; disabled?: boolean; onFixed: (label: string) => void } = $props();

  const uid = $props.id();
  const zoneRequired = $derived(hostNeed(kind) === 'host-tz');
  let zone = $state('');
  let saving = $state(false);
  let failure = $state('');
  let touched = $state(false);

  const nameBad = $derived(touched ? labelProblemText(labelProblem(serverName, null)) : '');
  const zoneBad = $derived(touched && zoneRequired ? zoneProblemText(zoneNameProblem(zone)) : '');

  async function add() {
    touched = true;
    failure = '';
    if (labelProblem(serverName, null) !== '') return;
    if (zoneRequired && zoneNameProblem(zone) !== '') return;
    saving = true;
    const result = await saveHost(serverName, zoneRequired ? zone : '', !zoneRequired);
    saving = false;
    // A name that is already in the list is the goal reached when no zone is needed.
    if (!result.ok && !(result.reason === 'taken' && !zoneRequired)) { failure = saveFailText(result.reason); return; }
    onFixed(serverName.trim());
  }
</script>

<div class="computer-fix" data-fix="computer">
  <p class="title">{COMPUTER_FIX_TEXT.title(serverName)}</p>
  <p class="help">{zoneRequired ? COMPUTER_FIX_TEXT.helpZone : COMPUTER_FIX_TEXT.help}</p>
  {#if zoneRequired}
    <label for="{uid}-z">{COMPUTER_FIX_TEXT.zoneLabel}</label>
    <input id="{uid}-z" type="text" list="{uid}-zones" autocomplete="off" spellcheck="false" placeholder={COMPUTER_FIX_TEXT.zonePlaceholder} disabled={saving || disabled} bind:value={zone} aria-invalid={zoneBad !== ''} />
    <datalist id="{uid}-zones">{#each zoneSuggestions() as option (option)}<option value={option}></option>{/each}</datalist>
    {#if zoneBad !== ''}<p class="bad" role="alert">{zoneBad}</p>{/if}
  {/if}
  {#if nameBad !== ''}<p class="bad" role="alert">{nameBad}</p>{/if}
  {#if failure !== ''}<p class="bad" role="alert">{failure}</p>{/if}
  <button type="button" class="primary-button" disabled={saving || disabled} aria-label={COMPUTER_FIX_TEXT.addLabel(serverName, fileName)} onclick={() => void add()}>
    {saving ? COMPUTER_FIX_TEXT.saving : COMPUTER_FIX_TEXT.add(serverName)}
  </button>
</div>

<style>
  .computer-fix { display: grid; gap: var(--space-1); min-width: 0; padding: var(--space-2); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .title { margin: 0; font-size: var(--fs-3); font-weight: 600; overflow-wrap: anywhere; }
  .help { margin: 0; color: var(--text-2); font-size: var(--fs-2); overflow-wrap: anywhere; }
  label { font-size: var(--fs-2); font-weight: 600; }
  input[type='text'] { box-sizing: border-box; width: 100%; min-width: 180px; max-width: 100%; min-height: 36px; padding: 4px 8px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-3); }
  input:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .bad { margin: 0; color: var(--text); font-size: var(--fs-2); overflow-wrap: anywhere; }
  button { justify-self: start; }
</style>
