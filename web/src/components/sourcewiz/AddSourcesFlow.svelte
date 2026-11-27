<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import { wizard, resetWizard, wizardIsDirty } from '../../lib/wizard.svelte';
  import { REVIEW_WINDOWS, blockers, chooseWindow, createNow, createView, progressSummary, setAck } from '../../lib/wizardCreate.svelte';
  import { importPlan } from '../../lib/wizardSubmit';
  import { getEnvironments, listSourceTags } from '../../lib/caseTagsApi';
  import { getCaseView, isCaseGone } from '../../lib/cases';
  import { sourcesView, removeChosen } from '../../lib/wizardSources.svelte';
  import { sourceKey } from '../../lib/wizardSources';
  import { groupOf } from '../../lib/discover';
  import { railGroups, canFinish } from '../../lib/sourceWizard';
  import { alreadyInCase, gateOfView, keepOnLeave } from '../../lib/wizardGuards';
  import type { CaseGate } from '../../lib/wizardGuards';
  import { addGuard } from '../../lib/wizardGuards.svelte';
  import { DISCOVER_TEXT, windowChoiceLabel } from '../../lib/copy/discoverCopy';
  import { kindLabel } from '../../lib/copy/wizardSourcesCopy';
  import { ACK_LABEL, GATING_BANNER } from '../../lib/copy/importCopy';
  import { WIZ, lastButton } from '../../lib/copy/sourceWizardCopy';
  import CreateProgress from '../wizard/CreateProgress.svelte';
  import ConfirmAsk from './ConfirmAsk.svelte';
  import SourceStep from './SourceStep.svelte';

  // Add sources to a case that is already open: the same Add sources step as the create wizard, then a short Review & add, then the run.
  // The case already exists, so the run starts with its id set and never makes a case.
  //
  // Guards (BL-148 leftovers):
  //  - Leaving the Add data tab keeps an unfinished list, or a finished run that left failures on screen, and the tab picks it up again.
  //  - The case is read again before the draft question, when the window gets focus back, before Review and before the run. A case that was
  //    closed or erased somewhere else says so, and nothing is thrown away or sent to it. A source the case already holds (added somewhere
  //    else, or earlier) is left out of the list with a line saying so, so it is never added twice.
  //  - Nothing resets the draft while a run is writing from it.
  let { caseId, readOnly = false, onDone, onFiles }: { caseId: string; readOnly?: boolean; onDone: () => void; onFiles?: () => void } = $props();

  type Screen = 'add' | 'review';
  let screen = $state<Screen>('add');
  let existing = $state<Array<{ kind: string; label: string }>>([]);
  let head = $state<HTMLHeadingElement>();
  let flowEl = $state<HTMLDivElement>();
  const NL = String.fromCharCode(10);
  let caseEnvs: string[] = [];
  const plan = $derived(importPlan(wizard.sources));
  const needsNotice = $derived(plan.scan + plan.files > 0);
  const pending = $derived(blockers());
  const started = $derived(createView.submit.caseId !== '');
  const titleOf = (s: { kind: string; ref: string }): string => {
    if (s.kind !== 'discover') return kindLabel(s.kind);
    const f = sourcesView.findings.find((x) => x.id === s.ref);
    return f ? DISCOVER_TEXT.groups[groupOf(f)] : kindLabel('discover');
  };
  const groups = $derived(railGroups(wizard.sources, titleOf));

  // This tab is picked up again (an unfinished list or a run left on screen) or started clean. A new-case draft that is not this tab's is never
  // thrown away without a word, and never while a run is writing it.
  const firstCase = untrack(() => caseId);
  const resume = (createView.appendOnly && createView.submit.caseId === firstCase) || addGuard.keptFor === firstCase;
  if (addGuard.keptFor === firstCase) addGuard.keptFor = '';
  const foreign = !resume && wizardIsDirty();
  const busyElsewhere = !resume && createView.running;
  // The draft on screen is this tab's own (not a draft of the create wizard left in the way).
  let owned = $state(resume);
  // What a fresh read of the case says. The case page's own belief (readOnly) can only make it stricter.
  let gate = $state<CaseGate>('open');
  const closed = $derived(readOnly || gate !== 'open');
  const closedNote = $derived(gate === 'gone' ? WIZ.goneCase : WIZ.closedCase);
  // Only the draft question waits for the read: every other screen is there at once.
  let checking = $state(foreign);
  let askDiscard = $state(false);
  let askAddMore = $state(false);
  // The case is read again just before Review and just before the send: the hub tabs are off for that moment (addGuard.preparing), and a read that
  // ends after this tab was left sends nothing, so a send never happens off screen.
  let gone = false;
  let leftOut = $state<string[]>([]);
  const ready = $derived(canFinish(true, wizard.sources.length, createView.running, closed) && pending.length === 0 && !addGuard.preparing);

  onMount(() => {
    // A draft from the create wizard is not carried into this case: start from an empty list (after asking, when it holds work).
    if (!resume && !foreign && !busyElsewhere) { resetWizard(); owned = true; }
    const mine = new AbortController();
    void begin(mine.signal);
    let lastLook = Date.now();
    const looksAgain = () => {
      if (document.visibilityState !== 'visible' || Date.now() - lastLook < 1500) return;
      lastLook = Date.now();
      void refresh(mine.signal);
    };
    window.addEventListener('focus', looksAgain);
    document.addEventListener('visibilitychange', looksAgain);
    return () => {
      gone = true; if (iSetPreparing) { addGuard.preparing = false; iSetPreparing = false; }
      mine.abort();
      window.removeEventListener('focus', looksAgain);
      document.removeEventListener('visibilitychange', looksAgain);
      leave();
    };
  });

  // A reload or a closed browser tab would lose the list, so the browser asks first while there is one.
  $effect(() => {
    if (!owned || !(createView.running || (!started && (wizard.sources.length > 0 || addGuard.entryDirty)))) return;
    const warn = (event: BeforeUnloadEvent) => { event.preventDefault(); };
    window.addEventListener('beforeunload', warn);
    return () => window.removeEventListener('beforeunload', warn);
  });

  /** The tab is left (another hub tab, or the case page): an unfinished list or the failures of a run are kept for the way back; a clean end is cleared. */
  function leave() {
    if (!owned || createView.running) return;
    const failures = started ? progressSummary().failures.length : 0;
    if (keepOnLeave({ sources: wizard.sources.length, started, failures, running: false })) { addGuard.keptFor = caseId; return; }
    resetWizard();
  }

  async function begin(signal: AbortSignal) {
    await Promise.all([checkCase(signal), loadCase(signal)]);
    if (signal.aborted) return;
    // The question comes after the read: a case that cannot take data would only have lost the draft for nothing.
    if (foreign) { checking = false; if (gate === 'open' && !readOnly) askDiscard = true; }
    applyEnvs();
    if (owned) dropDuplicates();
  }

  /** A fresh read of the case: still open, closed or archived somewhere else, or erased. A failed read changes nothing (the server refuses on its own). */
  async function checkCase(signal?: AbortSignal) {
    try {
      const view = await getCaseView(caseId, signal);
      if (!signal?.aborted) gate = gateOfView(view);
    } catch (error) {
      if (!signal?.aborted && isCaseGone(error)) gate = 'gone';
    }
  }

  // The case's own list and its declared environments, so a new source is checked against them. They are never written back (the run skips that step).
  async function loadCase(signal?: AbortSignal) {
    try {
      const [tags, envs] = await Promise.all([listSourceTags(caseId, signal), getEnvironments(caseId, signal).catch(() => [] as string[])]);
      if (signal?.aborted) return;
      existing = tags.map((t) => ({ kind: t.kind, label: t.label }));
      caseEnvs = [...new Set([...envs, ...tags.map((t) => t.env ?? '')].map((e) => e.trim()).filter((e) => e !== ''))];
      applyEnvs();
    } catch { /* silent on purpose: this read only fills the "already in the case" check and the saved environments; nothing is blocked, and the next focus or press reads again */ }
  }
  const applyEnvs = () => { if (owned && !askDiscard) wizard.envText = caseEnvs.join(NL); };

  /** Reads the case again while the list is open (the window got focus back): another window may have closed the case or added the same source. */
  async function refresh(signal?: AbortSignal) {
    if (createView.running || started || addGuard.preparing) return;
    await Promise.all([checkCase(signal), loadCase(signal)]);
    // A press made while this read ran starts its own read, which does the left-out check: this one must not change the list under it.
    if (signal?.aborted || !owned || addGuard.preparing) return;
    dropDuplicates();
  }

  /** Takes the sources the case already holds off the list and says so. Returns the labels left out. */
  function dropDuplicates(): string[] {
    if (createView.running || started) return [];
    const dupes = alreadyInCase(wizard.sources, existing);
    if (dupes.length === 0) return [];
    const names = dupes.map((d) => d.label || d.ref);
    for (const d of dupes) removeChosen(sourceKey(d));
    leftOut = [...leftOut, ...names];
    return names;
  }

  async function addMore() {
    if (!resetWizard()) return;
    screen = 'add'; wizard.envText = caseEnvs.join(NL); leftOut = [];
    await tick(); flowEl?.focus(); void loadCase();
  }
  // The failures of the run on screen are not saved anywhere in add mode, so clearing them asks first.
  const requestAddMore = () => { if (progressSummary().failures.length > 0) askAddMore = true; else void addMore(); };

  // Only the flow that set the flag clears it, so a flow that was left cannot switch the tabs on under a newer one that is still reading.
  let iSetPreparing = false;
  let statusEl = $state<HTMLParagraphElement>();
  /** Reads the case and its list again. While it runs the list is frozen (Back, Review and the tabs are off) and a status says why. Returns false when this tab was left meanwhile. */
  async function prepare(): Promise<boolean> {
    addGuard.preparing = true; iSetPreparing = true;
    await tick(); statusEl?.focus();
    await Promise.all([checkCase(), loadCase()]);
    if (iSetPreparing) { addGuard.preparing = false; iSetPreparing = false; }
    return !gone;
  }

  async function go(to: Screen) {
    if (to === 'review') {
      // The case is read again first: it may have closed, or gained one of these sources, since the list was made.
      if (!(await prepare())) return;
      dropDuplicates();
      if (closed || wizard.sources.length === 0) { await tick(); flowEl?.focus(); return; }
    }
    screen = to; await tick(); (to === 'review' ? head : flowEl)?.focus();
  }

  async function run() {
    // The blockers are read fresh (a scan can expire while this screen is open); the run skips them once the id is set.
    if (!ready || blockers().length > 0) return;
    const target = caseId;
    if (!(await prepare())) return;
    // The list is frozen during the read (Back is off), so what is sent is what Review showed; if the screen changed anyway, nothing is sent.
    if (screen !== 'review') return;
    const dropped = dropDuplicates();
    if (closed) { await tick(); head?.focus(); return; }
    if (dropped.length > 0) { if (wizard.sources.length === 0) await go('add'); else { await tick(); head?.focus(); } return; }
    if (wizard.sources.length === 0 || blockers().length > 0) return;
    createView.appendOnly = true;
    createView.submit.caseId = target;
    createView.submit.caseTitle = '';
    void tick().then(() => flowEl?.focus());
    await createNow();
  }

  function discardDraft() {
    if (!resetWizard()) return;
    askDiscard = false; owned = true;
    wizard.envText = caseEnvs.join(NL);
    void tick().then(() => flowEl?.focus());
  }
</script>

<div class="flow" tabindex="-1" bind:this={flowEl}>
  {#if leftOut.length > 0}
    <div class="notice" role="status" data-left-out>
      <span>{WIZ.leftOutSome(leftOut)}</span>
      <button type="button" class="quiet-button" onclick={() => { leftOut = []; }}>{WIZ.leftOutDismiss}</button>
    </div>
  {/if}
  {#if addGuard.preparing}
    <p class="hint" tabindex="-1" bind:this={statusEl} data-checking-case>{WIZ.readingCase}</p>
  {/if}
  {#if checking}
    <p class="hint" role="status">{WIZ.checkingCase}</p>
  {:else if busyElsewhere}
    <p class="inline-error" role="alert">{WIZ.runBusy}</p>
  {:else if foreign && !owned}
    <!-- Another draft is in the way and this case cannot take data: nothing is thrown away. -->
    <p class="inline-error" role="alert">{closedNote}</p>
    <p class="hint">{WIZ.draftKept}</p>
  {:else if askDiscard}
    <section role="alert">
      <p>{WIZ.draftWarning}</p>
      <button type="button" class="primary-button" onclick={discardDraft}>{WIZ.draftDiscard}</button>
    </section>
  {:else if started}
    <CreateProgress adding onOpen={() => { resetWizard(); onDone(); }} onAddMore={requestAddMore} />
  {:else if screen === 'add'}
    <SourceStep adding {caseId} {closed} {closedNote} {existing} {onFiles} />
    <div class="actions">
      <button type="button" class="primary-button" disabled={wizard.sources.length === 0 || closed || addGuard.preparing} onclick={() => void go('review')}>Review</button>
    </div>
  {:else}
    <section class="review" aria-labelledby="add-review-h">
      <h3 id="add-review-h" tabindex="-1" bind:this={head}>Review &amp; add</h3>
      {#each groups as g (g.title)}
        <p class="gtitle">{g.title}</p>
        <ul>{#each g.items as s (sourceKey(s))}<li>{s.label || s.ref}</li>{/each}</ul>
      {/each}
      {#if plan.scan > 0}
        <label class="field">How far back to read the logs of this computer
          <select value={createView.window} disabled={addGuard.preparing} onchange={(e) => chooseWindow((e.currentTarget as HTMLSelectElement).value as typeof createView.window)}>
            {#each REVIEW_WINDOWS as w (w)}<option value={w}>{windowChoiceLabel(w)}</option>{/each}
          </select>
        </label>
      {/if}
      {#if needsNotice}
        <p class="hint">{GATING_BANNER}</p>
        <label class="ack"><input type="checkbox" checked={createView.ack} disabled={addGuard.preparing} onchange={(e) => setAck((e.currentTarget as HTMLInputElement).checked)} /> {ACK_LABEL}</label>
      {/if}
      {#if pending.includes('scan-busy') || pending.includes('scan-stale')}<p class="inline-error" role="alert">The list of things found on this PC is out of date. Go back and use Look again.</p>{/if}
      {#if pending.includes('files')}<p class="inline-error" role="alert">A file is missing. Go back and pick it again.</p>{/if}
      {#if pending.includes('zone')}<p class="inline-error" role="alert">Name the computer each file came from. Go back to the Files card.</p>{/if}
      {#if pending.includes('computer-busy')}<p class="inline-error" role="status">Choosing the computer for your files. One moment.</p>{/if}
      {#if closed}<p class="inline-error" role="alert">{closedNote}</p>{/if}
      <p class="hint">{WIZ.reviewStays} {WIZ.reviewStale}</p>
      <p class="hint">{WIZ.footerAdd}</p>
      <div class="actions">
        <button type="button" class="quiet-button" disabled={addGuard.preparing} onclick={() => void go('add')}>← Back</button>
        <button type="button" class="primary-button" disabled={!ready} onclick={() => void run()}>{lastButton(true, wizard.sources.length, createView.running)}</button>
      </div>
    </section>
  {/if}
  {#if askAddMore}
    <ConfirmAsk id="add-more" title={WIZ.guard.addMoreTitle} text={WIZ.guard.addMoreText} keepLabel={WIZ.guard.addMoreKeep} leaveLabel={WIZ.guard.addMoreGo} onKeep={() => { askAddMore = false; }} onLeave={() => { askAddMore = false; void addMore(); }} />
  {/if}
</div>

<style>
  .flow:focus { outline: none; }
  .flow { display: grid; gap: var(--space-4); min-width: 0; }
  .review { display: grid; gap: var(--space-3); max-width: 70ch; }
  .review h3 { margin: 0; font-size: var(--fs-5); } .review h3:focus { outline: none; }
  .review ul { margin: 0; padding-left: var(--space-5); }
  .gtitle { margin: 0; font-weight: 600; }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-3); justify-content: space-between; }
  .ack { display: flex; gap: var(--space-2); align-items: flex-start; }
  .field { display: grid; gap: var(--space-1); }
  .notice { display: flex; flex-wrap: wrap; gap: var(--space-2) var(--space-3); align-items: center; justify-content: space-between; padding: var(--space-2) var(--space-3); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-3); }
</style>
