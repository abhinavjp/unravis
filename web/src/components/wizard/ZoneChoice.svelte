<script lang="ts">
  import { onMount, tick as afterUpdate } from 'svelte';
  import { hostNeed, timeZoneRule } from '../../lib/fileKinds';
  import { hostsView, loadHosts, saveHost, saveHostZone } from '../../lib/wizardHosts.svelte';
  import { hostProblem, labelProblem, zoneNameProblem, zoneOf, zoneSuggestions } from '../../lib/zoneChoice';
  import {
    ZONE_CHOICE_TEXT, hostOptionText, hostPickLabel, hostProblemText, labelProblemText, saveFailText, zoneProblemText
  } from '../../lib/copy/zoneChoiceCopy';

  // "Which computer wrote it?" for one file (packet P4, wireframe w38 and the review step). The server keeps one list of computers; an import
  // names one. A kind that does not say it itself needs one picked (HTTPERR, Query Store); a kind that writes that computer's own clock
  // also needs the computer's time zone (SQL Server error log, Windows event log). A computer that is not in the list can be added here.
  // Props: the kind of the file, the computer picked now ('' for none), the file's name (for the screen reader), and where to report a pick.
  // `disabled` locks every control: a drawer row that is being sent cannot be changed, so its computer cannot be changed either.
  let { kind, host, name, onChange, disabled = false }: { kind: string; host: string; name: string; onChange: (label: string) => void; disabled?: boolean } = $props();

  const uid = $props.id();
  const ADD = '__add__';
  const need = $derived(hostNeed(kind));
  const utc = $derived(timeZoneRule(kind) === 'utc');
  const entries = $derived(hostsView.entries);
  const problem = $derived(hostProblem(kind, host, entries));
  const zoneRequired = $derived(need === 'host-tz');

  let adding = $state(false);
  let newName = $state('');
  let newZone = $state('');
  let saving = $state(false);
  let failure = $state('');
  let touched = $state(false);
  let zoneFor = $state('');
  let selectEl = $state<HTMLSelectElement>();

  // The form that was open (Add a computer, or the zone of the computer picked) goes away when it ends, and the button that was pressed goes with it:
  // the focus goes to the select, so a keyboard user is not left on the page.
  async function focusSelect() {
    await afterUpdate();
    selectEl?.focus();
  }

  const nameBad = $derived(touched ? labelProblemText(labelProblem(newName, entries)) : '');
  const zoneBad = $derived(touched && (zoneRequired || newZone.trim() !== '') ? zoneProblemText(zoneNameProblem(newZone)) : '');
  const suggestions = $derived(adding || problem === 'no-zone' ? zoneSuggestions() : []);

  onMount(() => { void loadHosts(); });

  function onSelect(event: Event & { currentTarget: HTMLSelectElement }) {
    const value = event.currentTarget.value;
    failure = '';
    if (value === ADD) {
      adding = true;
      // The list is not changed by choosing "Add": the select goes back to what was picked until the new one is saved.
      event.currentTarget.value = host;
      return;
    }
    adding = false;
    onChange(value);
  }

  async function save() {
    touched = true;
    failure = '';
    if (labelProblem(newName, entries) !== '' || (zoneRequired || newZone.trim() !== '' ? zoneNameProblem(newZone) !== '' : false)) return;
    saving = true;
    // The select is locked while the save waits; this is the guard if the pick was changed anyway: the new computer is then kept in the list but not picked.
    const pickedBefore = host;
    const result = await saveHost(newName, newZone, !zoneRequired);
    saving = false;
    if (!result.ok) { failure = saveFailText(result.reason); return; }
    adding = false;
    touched = false;
    newName = '';
    newZone = '';
    if (host === pickedBefore) onChange(result.label);
    void focusSelect();
  }

  async function saveZoneOnly() {
    touched = true;
    failure = '';
    if (zoneNameProblem(zoneFor) !== '') return;
    saving = true;
    const result = await saveHostZone(host, zoneFor);
    saving = false;
    if (!result.ok) { failure = saveFailText(result.reason); return; }
    touched = false;
    zoneFor = '';
    void focusSelect();
  }

  function cancel() {
    adding = false;
    touched = false;
    failure = '';
    newName = '';
    newZone = '';
    void focusSelect();
  }

  const hint = $derived(
    problem === 'pick' ? hostProblemText('pick', need) :
    problem === 'gone' || problem === 'no-zone' ? hostProblemText(problem, need) :
    need === 'optional' && host === '' ? ZONE_CHOICE_TEXT.optionalHelp :
    utc && host !== '' ? ZONE_CHOICE_TEXT.utcNote : ''
  );
</script>

{#if need !== 'none'}
  <div class="choice" data-need={need}>
    {#if hostsView.loading && entries === null}
      <p class="hint" role="status">{ZONE_CHOICE_TEXT.loading}</p>
    {:else if hostsView.failed && entries === null}
      <p class="hint" role="status">{ZONE_CHOICE_TEXT.loadFailed}</p>
      <button type="button" class="quiet-button" onclick={() => void loadHosts(true)}>{ZONE_CHOICE_TEXT.retry}</button>
    {/if}

    <select bind:this={selectEl} class:invalid={problem !== ''} value={host} disabled={disabled || saving} aria-label={hostPickLabel(name)} aria-describedby={hint !== '' ? uid + '-hint' : undefined}
      aria-invalid={problem !== ''} onchange={onSelect}>
      <option value="">{need === 'optional' ? ZONE_CHOICE_TEXT.optionalNone : ZONE_CHOICE_TEXT.pick}</option>
      {#if host !== '' && !(entries ?? []).some((entry) => entry.label === host)}<option value={host}>{host}</option>{/if}
      {#each entries ?? [] as entry (entry.label)}<option value={entry.label}>{hostOptionText(entry)}</option>{/each}
      <option value={ADD}>{ZONE_CHOICE_TEXT.add}</option>
    </select>

    {#if hint !== ''}<p class="hint" id="{uid}-hint" class:bad={problem !== ''}>{hint}</p>{/if}

    {#if problem === 'no-zone'}
      <div class="form" role="group" aria-label={ZONE_CHOICE_TEXT.zoneLabel}>
        <label for="{uid}-z0">{ZONE_CHOICE_TEXT.zoneLabel}</label>
        <input id="{uid}-z0" type="text" list="{uid}-zones" autocomplete="off" spellcheck="false" disabled={disabled} bind:value={zoneFor} aria-invalid={touched && zoneNameProblem(zoneFor) !== ''} />
        <p class="hint">{ZONE_CHOICE_TEXT.zoneHelp}</p>
        {#if touched && zoneNameProblem(zoneFor) !== ''}<p class="hint bad" role="alert">{zoneProblemText(zoneNameProblem(zoneFor))}</p>{/if}
        <div class="actions">
          <button type="button" class="secondary-button" disabled={saving || disabled} onclick={() => void saveZoneOnly()}>{saving ? ZONE_CHOICE_TEXT.saving : ZONE_CHOICE_TEXT.saveZone}</button>
        </div>
      </div>
    {/if}

    {#if adding}
      <div class="form" role="group" aria-label={ZONE_CHOICE_TEXT.addHeading}>
        <p class="form-title">{ZONE_CHOICE_TEXT.addHeading}</p>
        <label for="{uid}-n">{ZONE_CHOICE_TEXT.nameLabel}</label>
        <input id="{uid}-n" type="text" autocomplete="off" spellcheck="false" disabled={disabled} bind:value={newName} aria-invalid={nameBad !== ''} aria-describedby="{uid}-nh" />
        <p class="hint" id="{uid}-nh">{ZONE_CHOICE_TEXT.nameHelp}</p>
        {#if nameBad !== ''}<p class="hint bad" role="alert">{nameBad}</p>{/if}
        <label for="{uid}-z">{zoneRequired ? ZONE_CHOICE_TEXT.zoneLabel : ZONE_CHOICE_TEXT.zoneLabelOptional}</label>
        <input id="{uid}-z" type="text" list="{uid}-zones" autocomplete="off" spellcheck="false" disabled={disabled} bind:value={newZone} aria-invalid={zoneBad !== ''} aria-describedby="{uid}-zh" />
        <p class="hint" id="{uid}-zh">{utc ? ZONE_CHOICE_TEXT.utcNote + ' ' : ''}{ZONE_CHOICE_TEXT.zoneHelp}</p>
        {#if zoneBad !== ''}<p class="hint bad" role="alert">{zoneBad}</p>{/if}
        {#if failure !== ''}<p class="hint bad" role="alert">{failure}</p>{/if}
        <div class="actions">
          <button type="button" class="secondary-button" disabled={saving || disabled} onclick={() => void save()}>{saving ? ZONE_CHOICE_TEXT.saving : ZONE_CHOICE_TEXT.save}</button>
          <button type="button" class="quiet-button" disabled={saving || disabled} onclick={cancel}>{ZONE_CHOICE_TEXT.cancel}</button>
        </div>
      </div>
    {:else if failure !== ''}
      <p class="hint bad" role="alert">{failure}</p>
    {/if}

    {#if suggestions.length > 0}
      <datalist id="{uid}-zones">{#each suggestions as zone (zone)}<option value={zone}></option>{/each}</datalist>
    {/if}
    {#if host !== '' && zoneOf(host, entries) !== '' && !utc}<p class="hint zone-now">{ZONE_CHOICE_TEXT.zoneIs} {zoneOf(host, entries)}</p>{/if}
  </div>
{/if}

<style>
  .choice { display: grid; gap: var(--space-1); min-width: 0; }
  select, input[type='text'] { box-sizing: border-box; width: 100%; min-width: 180px; max-width: 100%; min-height: 36px; padding: 4px 8px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-3); }
  select.invalid { border-color: var(--bad); }
  select:focus-visible, input:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-2); overflow-wrap: anywhere; }
  .hint.bad { color: var(--text); }
  .form { display: grid; gap: var(--space-1); margin-top: var(--space-1); padding: var(--space-2); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .form-title { margin: 0; font-size: var(--fs-3); font-weight: 600; }
  .form label { font-size: var(--fs-2); font-weight: 600; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); margin-top: var(--space-1); }
</style>
