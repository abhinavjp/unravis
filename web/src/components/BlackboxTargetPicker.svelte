<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { TARGETS_MAX, blackboxApi, pickedTargetState, targetListState, targetsProblem, toggleTarget, type SettingsProblem, type TargetInfo, type TargetKind, type TargetRef, targetRunState } from '../lib/blackbox';
  import { BB_TEXT as T } from '../lib/copy/blackboxCopy';

  // The target picker of a Blackbox, shared by Apply settings on this PC (through BlackboxSettings) and the
  // Place wizard. `targets` is the one list it edits (kind + name only). `mode` is 'pc' when the Blackbox runs
  // on this PC: the detected app pools and services are read once on mount. 'pack' (a pack for another
  // server) never fetches: names are typed, with a kind picker. `problem` is the field that is wrong, if any:
  // only 'targets' is read here, and its message shows beside the name box (D-B-50).
  let { targets = $bindable(), mode = 'pc', idPrefix = 'bb', problem = null }: { targets: TargetRef[]; mode?: 'pc' | 'pack'; idPrefix?: string; problem?: SettingsProblem | null } = $props();

  // Targets (BBX-1-12): only kind+name is ever stored. A stopped target says "not running" (W2-07);
  // one whose state could not be read says nothing of its own, and is never labelled stopped. UX-8:
  // the per-row unknown label is gone - the one overall note (targetsAllUnknown) carries that, and a
  // name absent from a capped or unreadable list stays unknown, never guessed stopped (W3-06).
  let appPools = $state<TargetInfo[]>([]);
  let services = $state<TargetInfo[]>([]);
  let detected = $state(false);
  let appPoolsState = $state<string | undefined>(undefined);
  let servicesState = $state<string | undefined>(undefined);
  let targetsRead = $state(false);
  // True when the one mount-time read was refused or never reached the server (UX-5). A failed
  // read is never shown as an empty healthy list: both detected lists say it could not be read.
  let targetsFailed = $state(false);
  let manualKind = $state<TargetKind>('appPool');
  let manualName = $state('');
  // Why the last Add was refused. The name stays in the box so it can be fixed (UX-4).
  let targetRefusal = $state('');
  // Per-group filter text (UX-6). Functions below read it live on every render, as groupState does.
  let poolFilter = $state('');
  let serviceFilter = $state('');

  onMount(() => {
    if (mode !== 'pc') return;
    let alive = true;
    blackboxApi.targets().then((answer) => {
      if (!alive) return;
      appPools = answer.appPools;
      services = answer.services;
      detected = answer.detected;
      appPoolsState = answer.appPoolsState;
      servicesState = answer.servicesState;
      targetsRead = true;
    }).catch(() => { targetsRead = true; targetsFailed = true; /* nothing could be read: the lists say so and names are typed below */ });
    return () => { alive = false; };
  });

  // What one detected list can honestly show (UX-5): loading, failed, not-found, unavailable,
  // empty or list. A function so it reads the live state on every render; the rule itself is pure.
  function groupState(kind: TargetKind) {
    return kind === 'appPool'
      ? targetListState(appPools, appPoolsState, detected, targetsRead, targetsFailed)
      : targetListState(services, servicesState, detected, targetsRead, targetsFailed);
  }

  // UX-6: a long list stays usable. The filter narrows what is shown, never the read list itself;
  // names match without regard to case, as Windows matches them. A blank filter shows everything.
  function shownTargets(kind: TargetKind): TargetInfo[] {
    const text = (kind === 'appPool' ? poolFilter : serviceFilter).trim().toLowerCase();
    const list = kind === 'appPool' ? appPools : services;
    return text ? list.filter((t) => t.name.toLowerCase().includes(text)) : list;
  }
  // What the count line under a filter box says: how many of the names are shown, or how to see
  // them all again when none match. The filter never silently hides that names exist.
  function filterNote(kind: TargetKind): string {
    const total = (kind === 'appPool' ? appPools : services).length;
    const shown = shownTargets(kind).length;
    return shown > 0 ? T.targetsFilterCount(shown, total) : T.targetsFilterNone(total);
  }
  // UX-8b: one note under the legend when ANY row's running state could not be read, so a mixed group
  // is never left without words. Still one note per group, never one per row.
  function anyUnknown(kind: TargetKind): boolean {
    const list = kind === 'appPool' ? appPools : services;
    return list.some((t) => targetRunState(t) === 'unknown');
  }
  // True when no row's running state could be read: then the rows carry no note of their own at all,
  // because the one note above them has already said it. A mixed list keeps only its per-row "not
  // running" notes (UX-8); a row whose state was not read says nothing of its own.
  function allUnknown(kind: TargetKind): boolean {
    const list = kind === 'appPool' ? appPools : services;
    return list.length > 0 && list.every((t) => targetRunState(t) === 'unknown');
  }

  const picked = $derived(targets ?? []);
  // The target-name box and the chip list: the two places focus can land when a chip's remove button
  // is destroyed under the keyboard (UX-7).
  let nameField = $state<HTMLInputElement>();
  let chipsBox = $state<HTMLUListElement>();
  // The last thing said out loud to a screen reader: which name went on or off the list. Hidden with
  // CSS only, never from assistive tech, and never behind a conditional - a live region has to be in
  // the page before its words change, or most screen readers stay silent (UX-7).
  let announcement = $state('');
  // Detected names are offered as checkboxes; the manual add row is always available alongside them,
  // so a known valid name can be added even when detection succeeded but did not list it (W3-06).
  const showLists = $derived(mode === 'pc' && detected && (appPools.length > 0 || services.length > 0));
  // The names the detected lists do not offer a checkbox for, so they can only be a chip. A function
  // because UX-7 has to read the chips both before and after one removal, in the order the page shows them.
  function notInLists(list: TargetRef[], pools: TargetInfo[], svcs: TargetInfo[]): TargetRef[] {
    return list.filter((t) => !(t.kind === 'appPool' ? pools : svcs).some((d) => d.name === t.name));
  }
  const extraPicked = $derived(notInLists(picked, appPools, services));
  // The chips on the page right now: only the names no checkbox offers when the lists are shown, every
  // picked name otherwise. Read live, not as a derived, so a removal sees the list after the change.
  function chipsShown(): TargetRef[] {
    const list = targets ?? [];
    return showLists ? notInLists(list, appPools, services) : list;
  }
  // Chips carry an honest state note only on this PC once the single read has settled; pack never fetches.
  const chipsLabelled = $derived(mode === 'pc' && targetsRead);
  const isSelected = (kind: TargetKind, name: string) => picked.some((t) => t.kind === kind && t.name === name);
  // Ticking or unticking a detected name is an add and a removal too, so it says the same plain words.
  // `box` is the checkbox that was clicked. At the limit a new tick is refused with the same words as the typed
  // Add, and the box is put back, so the screen never shows a name as picked that is not on the list.
  function pick(kind: TargetKind, name: string, box?: HTMLInputElement) {
    const added = !isSelected(kind, name);
    if (added && picked.length >= TARGETS_MAX) {
      targetRefusal = T.targetProblems.many;
      announcement = '';
      if (box) box.checked = false;
      return;
    }
    targetRefusal = '';
    targets = toggleTarget(picked, kind, name);
    announcement = added ? T.targetsAdded(name) : T.targetsRemoved(name);
  }
  function chipNote(t: TargetRef): string {
    const s = pickedTargetState(t.kind, t.name, { appPools, services, detected, appPoolsState, servicesState });
    if (s === 'stopped') return T.targetsNotRunning;
    if (s === 'not-in-list') return T.targetsNotInList;
    if (s === 'source-not-found') return T.targetsSourceNotFound;
    if (s === 'list-unreadable') return T.targetsListUnreadable;
    return '';
  }
  // The words beside the target row: why the last Add was refused, or the targets problem found at the
  // last attempt. One alert, so the row never shows two messages at once.
  function targetError(): string { return targetRefusal || (problem === 'targets' ? T.settingProblems.targets : ''); }
  // Add target is off while nothing is typed, or while the typed name is already on the list (capital and small
  // letters count as the same name, as the server reads them). A short reason is shown beside it.
  const typedName = $derived(manualName.trim());
  const alreadyPicked = $derived(
    typedName !== '' && picked.some((t) => t.kind === manualKind && t.name.toLowerCase() === typedName.toLowerCase())
  );
  const addBlocked = $derived(typedName === '' || alreadyPicked);
  const addReason = $derived(typedName === '' ? T.targetsAddNeedsName : alreadyPicked ? T.targetsAddAlready : '');
  function addTarget() {
    const name = manualName.trim();
    if (!name) return;
    // The server's own rules, checked here so a wrong name is refused beside the box instead of
    // coming back as a raw refusal. Nothing is added when the list would be wrong (UX-4).
    const why = targetsProblem([...picked, { kind: manualKind, name }]);
    if (why) { targetRefusal = T.targetProblems[why]; return; }
    if (!isSelected(manualKind, name)) {
      targets = toggleTarget(picked, manualKind, name);
      // Said only for a name that really went on the list; a refusal above announces nothing.
      announcement = T.targetsAdded(name);
    }
    targetRefusal = '';
    manualName = '';
  }
  // UX-7: which chip takes focus when the one under the finger disappears. `at` is the place the chip
  // had in the list shown before the removal, and `total` that list's length: the chip that shifts into
  // that place takes focus, the new last chip when the removed one was last, and null - the name box -
  // when no chip is left or the chip was not in this list at all.
  function focusSlot(total: number, at: number): number | null {
    if (at < 0 || total <= 1) return null;
    return Math.min(at, total - 2);
  }
  // The × of every chip on the page, in the order they read out.
  const chipXs = () => [...(chipsBox?.querySelectorAll<HTMLButtonElement>('.bb-chip-x') ?? [])];
  async function removeTarget(kind: TargetKind, name: string) {
    targetRefusal = '';
    const before = chipsShown();
    const at = before.findIndex((t) => t.kind === kind && t.name === name);
    targets = toggleTarget(picked, kind, name);
    // Cleared first and filled once the new list is rendered: the same name removed twice in a row then
    // changes the words both times, so a screen reader says it both times.
    announcement = '';
    await tick();
    announcement = T.targetsRemoved(name);
    // The focused × is gone with the chip it was on: focus goes to the next chip, or to the name box,
    // never to the removed node and never to the top of the page.
    const slot = focusSlot(before.length, at);
    const xs = chipXs();
    const next = slot === null || slot >= xs.length ? undefined : xs[slot];
    (next ?? nameField)?.focus();
  }
</script>

{#snippet rowNote(t: TargetInfo)}
  {#if targetRunState(t) === 'stopped'}{' '}<span class="bb-note">{T.targetsNotRunning}</span>{/if}
{/snippet}

{#snippet targetChips(list: TargetRef[])}
  {#if list.length > 0}
    <ul class="bb-chips" bind:this={chipsBox}>
      {#each list as t (t.kind + ':' + t.name)}
        <li class="bb-chip"><span>{t.kind === 'appPool' ? T.targetsKindAppPool : T.targetsKindService}: {t.name}{#if chipsLabelled && chipNote(t)} <span class="bb-note">{chipNote(t)}</span>{/if}</span>
          <button type="button" class="bb-chip-x" aria-label="{T.targetsRemove}: {t.name}" onclick={() => removeTarget(t.kind, t.name)}>×</button>
        </li>
      {/each}
    </ul>
  {/if}
{/snippet}

<div class="bb-picker">
  {#if mode === 'pc' && groupState('appPool') === 'loading'}
    <p class="bb-note" role="status">{T.targetsLoading}</p>
  {:else if mode === 'pc' && groupState('appPool') === 'failed'}
    <p class="bb-note bb-warn" role="status">{T.targetsLoadFailed}</p>
  {/if}
  {#if mode === 'pc' && targetsRead && !targetsFailed}
    {#if groupState('appPool') === 'not-found'}<p class="bb-note">{T.targetsPoolsNotFound}</p>{/if}
    {#if groupState('appPool') === 'unavailable'}<p class="bb-note bb-warn">{T.targetsPoolsUnreadable}</p>{/if}
    {#if groupState('service') === 'not-found'}<p class="bb-note">{T.targetsServicesNotFound}</p>{/if}
    {#if groupState('service') === 'unavailable'}<p class="bb-note bb-warn">{T.targetsServicesUnreadable}</p>{/if}
  {/if}
  {#if showLists}
    {#if groupState('appPool') === 'list'}
      <fieldset class="bb-checks">
        <legend>{T.targetsAppPools}</legend>
        {#if anyUnknown('appPool')}<p class="bb-note">{T.targetsAllUnknown}</p>{/if}
        <label class="bb-field bb-filter"><span>{T.targetsFilterLabel}</span>
          <input id="{idPrefix}-pools-filter" type="text" bind:value={poolFilter} autocomplete="off" spellcheck="false" aria-describedby={poolFilter.trim() ? idPrefix + '-pools-count' : undefined} />
        </label>
        {#if poolFilter.trim()}<p class="bb-note" id="{idPrefix}-pools-count" role="status">{filterNote('appPool')}</p>{/if}
        <!-- A scroll region must be reachable by keyboard, so it takes tabindex despite being a div. -->
        <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
        <div class="bb-scroll" tabindex="0" role="group" aria-label={T.targetsAppPools}>
          {#each shownTargets('appPool') as t (t.name)}
            <label class="bb-check"><input type="checkbox" checked={isSelected('appPool', t.name)} onchange={(e) => pick('appPool', t.name, e.currentTarget)} /><span>{t.name}{#if !allUnknown('appPool')}{@render rowNote(t)}{/if}</span></label>
          {/each}
        </div>
      </fieldset>
    {/if}
    {#if groupState('service') === 'list'}
      <fieldset class="bb-checks">
        <legend>{T.targetsServices}</legend>
        {#if anyUnknown('service')}<p class="bb-note">{T.targetsAllUnknown}</p>{/if}
        <label class="bb-field bb-filter"><span>{T.targetsFilterLabel}</span>
          <input id="{idPrefix}-services-filter" type="text" bind:value={serviceFilter} autocomplete="off" spellcheck="false" aria-describedby={serviceFilter.trim() ? idPrefix + '-services-count' : undefined} />
        </label>
        {#if serviceFilter.trim()}<p class="bb-note" id="{idPrefix}-services-count" role="status">{filterNote('service')}</p>{/if}
        <!-- A scroll region must be reachable by keyboard, so it takes tabindex despite being a div. -->
        <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
        <div class="bb-scroll" tabindex="0" role="group" aria-label={T.targetsServices}>
          {#each shownTargets('service') as t (t.name)}
            <label class="bb-check"><input type="checkbox" checked={isSelected('service', t.name)} onchange={(e) => pick('service', t.name, e.currentTarget)} /><span>{t.name}{#if !allUnknown('service')}{@render rowNote(t)}{/if}</span></label>
          {/each}
        </div>
      </fieldset>
    {/if}
    {@render targetChips(extraPicked)}
    <p class="bb-note">{T.targetsListNote}</p>
  {:else}
    <p class="bb-note">{T.targetsManualNote}</p>
  {/if}
  <div class="bb-target-add">
    <label class="bb-field"><span>{T.targetsKindLabel}</span>
      <select bind:value={manualKind} onchange={() => { targetRefusal = ''; announcement = ''; }}>
        <option value="appPool">{T.targetsKindAppPool}</option>
        <option value="service">{T.targetsKindService}</option>
      </select>
    </label>
    <label class="bb-field"><span>{T.targetsNameLabel}</span>
      <input id="{idPrefix}-f-targets" type="text" bind:this={nameField} bind:value={manualName} autocomplete="off" spellcheck="false" aria-invalid={targetError() ? 'true' : undefined} aria-describedby={targetError() ? idPrefix + '-targets-error' : undefined} oninput={() => { announcement = ''; if (targetRefusal) targetRefusal = ''; }} onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); addTarget(); } }} />
    </label>
    <button type="button" class="secondary-button" disabled={addBlocked} aria-describedby={addReason ? idPrefix + '-add-reason' : undefined} onclick={addTarget}>{T.targetsAdd}</button>
  </div>
  {#if addReason && !targetError()}<p class="bb-note" id="{idPrefix}-add-reason">{addReason}</p>{/if}
  {#if targetError()}<p class="inline-error" id="{idPrefix}-targets-error" role="alert">{targetError()}</p>{/if}
  {#if !showLists}
    {@render targetChips(picked)}
  {/if}
  <!-- UX-7: names going on or off the list are said out loud. Always in the page, empty until then. -->
  <p class="bb-sr" role="status" aria-live="polite">{announcement}</p>
</div>

<style>
  .bb-picker { display: grid; gap: var(--space-2); min-width: 0; margin: 0; padding: 0; border: 0; }
  .bb-warn { color: var(--text); border-left: 3px solid var(--warn); padding-left: var(--space-2); }
  .bb-picker .inline-error { margin: 0; max-width: 420px; }
  .bb-field { display: grid; gap: 4px; font-size: 13px; font-weight: 500; }
  .bb-field input[type='text'], .bb-field select { width: 100%; max-width: 420px; padding: 6px 8px; font: inherit; font-weight: 400; color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); box-sizing: border-box; }
  .bb-checks { display: grid; gap: 6px; margin: 0; padding: 0; border: 0; }
  .bb-checks legend { padding: 0; font-size: 13px; font-weight: 500; }
  .bb-check { display: flex; align-items: flex-start; gap: 8px; font-size: 13px; }
  .bb-check input { flex: 0 0 auto; margin: 3px 0 0; }
  .bb-check span { min-width: 0; overflow-wrap: anywhere; }
  /* UX-6: a long detected list scrolls inside a bounded region instead of stretching the page. */
  .bb-scroll { display: grid; gap: 6px; max-height: 240px; overflow: auto; min-width: 0; }
  .bb-filter { max-width: 240px; }
  .bb-target-add { display: flex; flex-wrap: wrap; align-items: end; gap: var(--space-2); }
  .bb-target-add .bb-field { flex: 1 1 160px; max-width: 240px; }
  .bb-chips { display: flex; flex-wrap: wrap; gap: 6px; margin: 0; padding: 0; list-style: none; }
  .bb-chip { display: inline-flex; align-items: center; gap: 6px; padding: 2px 8px; font-size: 12px; border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface); overflow-wrap: anywhere; }
  /* UX-7: the × is a control, not a 14px glyph: a 24px square is the smallest box a tap or a keystroke can be aimed at. */
  .bb-chip-x { display: inline-grid; place-items: center; min-width: 24px; min-height: 24px; border: 0; padding: 0; background: none; color: var(--text-2); font-size: 14px; line-height: 1; cursor: pointer; border-radius: var(--radius-sm); }
  .bb-chip-x:hover { color: var(--text); }
  /* The ring is drawn inside the chip, so it stays whole next to the chip's own border. */
  .bb-chip-x:focus-visible { outline-offset: -2px; }
  /* UX-7: the announcement is read but never seen: no room on the page, nothing hidden from assistive tech. */
</style>
