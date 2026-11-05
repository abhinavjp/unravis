<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { POPUP_TEXT } from '../../copy/importPopupCopy';
  import type { Query } from '../../types';
  import CheckStep from './CheckStep.svelte';
  import ChooseStep from './ChooseStep.svelte';
  import ImportingStep from './ImportingStep.svelte';
  import ResultStep from './ResultStep.svelte';
  import { createPopupState, type PopupState } from './popupState.svelte';

  // The "Import files" popup (wireframe: prototype/import-popup-WIREFRAME.html): scrim, title row, stepper, progress line, the body and
  // footer of the step the popup state is on, and the discard confirmation. The popup state decides and does the work (check, import,
  // fixes); this file draws it. Step 1 is Choose, step 2 is Check (or the check's failure), step 3 is Importing and then Result.
  // Focus: the popup takes focus when it opens; Tab stays inside it (inside the confirmation while that is open); Esc and a click outside
  // ask to close, and answer "Keep them" when the confirmation is already open. `state.focus` moves focus to a `data-focus` element.
  const FOCUSABLE = 'button:not(:disabled), input:not(:disabled), select:not(:disabled), [href], [tabindex]:not([tabindex="-1"])';

  // Not in POPUP_TEXT yet: shown only when the check itself could not finish.
  const CHECK_FAILED = 'The server could not check these files.';
  const CHECK_AGAIN = 'Check again';

  // `query` is part of the popup's contract with the page but nothing here draws it.
  let {
    caseId,
    caseTitle,
    zone,
    onClose,
    onOpenTimeline,
    retryServerName = null,
    popup = createPopupState()
  }: {
    caseId: string;
    caseTitle: string;
    query: Query;
    zone: string;
    onClose: () => void;
    /** Called just before onClose when the person chose "Open timeline". */
    onOpenTimeline?: () => void;
    /** Opens the popup on the Check step with this server's fix card open, once the files are checked. */
    retryServerName?: string | null;
    popup?: PopupState;
  } = $props();

  let popupEl = $state<HTMLElement>();
  let veilEl = $state<HTMLElement>();

  const fixes = $derived(popup.fixesNeeded());
  const count = $derived(popup.targetCount());
  // The "Add all" shortcut counts the server cards still open, so it goes away when one is left.
  const openServers = $derived(new Set((popup.report?.files ?? []).filter((verdict) => verdict.verdict === 'needs-computer' && (popup.cards[verdict.serverName] ?? 'open') === 'open').map((verdict) => verdict.serverName)).size);

  $effect.pre(() => { popup.caseId = caseId; });
  onDestroy(() => popup.dispose());

  const progKey = $derived(popup.step === 3 ? (popup.importing ? '3importing' : '3result') : popup.step);
  const progText = $derived(POPUP_TEXT.prog[progKey]);
  const progCut = $derived(progText.indexOf(' · '));

  onMount(() => {
    if (retryServerName) popup.openRetry(retryServerName);
    popupEl?.focus({ preventScroll: true });
  });

  // The element may not be drawn yet (a card appears once the check is back), so the request stays until it is found.
  $effect(() => {
    const target = popup.focus;
    void popup.report; void popup.checking; void popup.step; void popup.discard; void popup.importDone;
    if (target === null || !popupEl) return;
    const found = [...popupEl.querySelectorAll<HTMLElement>('[data-focus]')].find((element) => element.dataset.focus === target);
    if (!found) return;
    popup.focus = null;
    found.focus({ preventScroll: true });
  });

  let told = false;
  $effect(() => {
    if (!popup.closed || told) return;
    told = true;
    if (popup.timeline) onOpenTimeline?.();
    onClose();
  });

  function ask() {
    if (popup.discard) popup.keepFiles();
    else popup.requestClose();
  }

  function onKey(event: KeyboardEvent) {
    if (popup.closed) return;
    if (event.key === 'Escape') { event.preventDefault(); ask(); return; }
    if (event.key !== 'Tab') return;
    const scope = popup.discard && veilEl ? veilEl : popupEl;
    if (!scope) return;
    const items = [...scope.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((element) => element.offsetParent !== null);
    if (!items.length) return;
    const first = items[0], last = items[items.length - 1];
    const active = document.activeElement;
    const outside = !(active instanceof Node) || active === scope || !scope.contains(active);
    if (event.shiftKey) {
      if (outside || active === first) { event.preventDefault(); last.focus(); }
    } else if (outside || active === last) {
      event.preventDefault(); first.focus();
    }
  }
</script>

<svelte:window onkeydown={onKey} />

<!-- The Result step is drawn twice, its body above and its footer below, so the two share this snippet. -->
{#snippet result(part: 'body' | 'footer', done: NonNullable<PopupState['result']>)}
  <ResultStep
    {part}
    imported={done.imported}
    notImported={done.notImported}
    computers={done.computers}
    onOpenTimeline={() => popup.openTimeline()}
    onImportMore={() => popup.importMore()}
  />
{/snippet}

<button type="button" class="scrim" tabindex="-1" aria-label="Close import" onclick={ask}></button>
<div class="popup" role="dialog" aria-modal="true" aria-labelledby="popTitle" tabindex="-1" bind:this={popupEl}>
  <div class="popup-head">
    <div class="popup-titles">
      <h2 id="popTitle">{POPUP_TEXT.title}</h2>
      <span class="case-chip">Case: <b>{caseTitle}</b></span>
    </div>
    <button type="button" class="close-x" data-focus="close" aria-label="Close import" onclick={ask}>×</button>
  </div>

  <div class="stepper">
    {#each POPUP_TEXT.stepNames as name, index}
      {@const n = index + 1}
      <button type="button" class="stp" class:cur={n === popup.step} class:done={n < popup.step} aria-current={n === popup.step ? 'step' : undefined} disabled={n >= popup.step || popup.importing} onclick={() => popup.goTo(n)}><span class="n">{n}</span> {name}</button>
      {#if index < POPUP_TEXT.stepNames.length - 1}<span class="stp-sep" class:done={popup.step > n}></span>{/if}
    {/each}
  </div>
  <div class="prog-line" aria-live="polite"><b>{progText.slice(0, progCut)}</b>{progText.slice(progCut)}</div>

  <div class="popup-body">
    {#if popup.step === 1}
      <ChooseStep
        files={popup.files}
        expanded={popup.expanded}
        onAddFiles={(picked) => void popup.addFiles(picked)}
        onAddFolder={(picked) => void popup.addFiles(picked)}
        onDrop={(event) => void popup.drop(event)}
        onToggle={(group) => popup.toggle(group)}
        onContinue={() => popup.continueToCheck()}
      />
    {:else if popup.step === 2}
      {#if popup.checkError !== ''}
        <div class="problem" role="alert">
          <p>{CHECK_FAILED} {popup.checkError}</p>
          <button type="button" class="btn btn-secondary" onclick={() => popup.continueToCheck()}>{CHECK_AGAIN}</button>
        </div>
      {:else}
        <CheckStep
          report={popup.report}
          files={popup.files}
          checking={popup.checking}
          cards={popup.cards}
          expanded={popup.expanded}
          selected={popup.selected}
          kinds={popup.kinds}
          {zone}
          totalServers={openServers}
          onResolve={(card, name) => popup.resolveCard(card, name)}
          onLeaveOut={(card) => popup.leaveOut(card)}
          onUndo={(card) => popup.undoCard(card)}
          onAddAll={() => popup.addAllServers()}
          onRemoveUnsupported={(key) => popup.removeUnsupported(key)}
          onPickKind={(key, kind) => popup.setKind(key, kind)}
          onToggle={(group) => popup.toggle(group)}
          onGroupAll={(group, on) => popup.groupAll(group, on)}
          onSelect={(key, on) => popup.setSelected(key, on)}
        />
      {/if}
    {:else if popup.importing}
      <ImportingStep progress={popup.importProgress} total={popup.importTotal} groups={popup.importGroups} onCancel={() => popup.cancelImport()} />
    {:else if popup.importDone && popup.result}
      {@render result('body', popup.result)}
    {/if}
  </div>

  <div class="popup-foot">
    {#if popup.step === 1}
      <div class="foot-row">
        <span class="foot-spacer"></span>
        <span class="foot-note">{POPUP_TEXT.nothingSent}</span>
        <button type="button" class="btn btn-primary" data-focus="continue" disabled={popup.files.length === 0} onclick={() => popup.continueToCheck()}>{POPUP_TEXT.continueBtn}</button>
      </div>
    {:else if popup.step === 2 && !popup.checking && popup.checkError === ''}
      {#if fixes > 0}
        <div class="foot-hint">
          <svg class="ic" width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true"><path d="M8 2.5l6 10.5H2L8 2.5z" stroke="currentColor" stroke-width="1.3" stroke-linejoin="round" /><path d="M8 7v3" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" /><circle cx="8" cy="11.4" r=".8" fill="currentColor" /></svg>
          {POPUP_TEXT.fixesNeeded(fixes)}
        </div>
      {/if}
      <div class="foot-row">
        <label class="ack">
          <input type="checkbox" data-focus="ack" aria-label="Confirm read-only" bind:checked={popup.ack} disabled={fixes > 0} />
          <span>{POPUP_TEXT.ack}</span>
        </label>
        <span class="foot-spacer"></span>
        <button type="button" class="btn btn-primary" data-focus="import" disabled={!popup.canImport()} onclick={() => popup.startImport()}>{POPUP_TEXT.importBtn(count)}</button>
      </div>
    {:else if popup.step === 3 && popup.importing}
      <div class="foot-row">
        <span class="foot-spacer"></span>
        <button type="button" class="btn btn-secondary" onclick={() => popup.cancelImport()}>{POPUP_TEXT.cancel}</button>
      </div>
    {:else if popup.step === 3 && popup.importDone && popup.result}
      {@render result('footer', popup.result)}
    {/if}
  </div>

  {#if popup.discard}
    <div class="discard-veil" bind:this={veilEl}>
      <div class="discard-card" role="alertdialog" aria-labelledby="dc-h" aria-describedby="dc-p">
        <h3 id="dc-h">{POPUP_TEXT.discardTitle(popup.listedFiles())}</h3>
        <p id="dc-p">{POPUP_TEXT.discardBody}</p>
        <div class="discard-actions">
          <button type="button" class="btn btn-secondary" data-focus="keep" onclick={() => popup.keepFiles()}>{POPUP_TEXT.discardKeep}</button>
          <button type="button" class="btn btn-primary" onclick={() => popup.discardNow()}>{POPUP_TEXT.discardNow}</button>
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
  .scrim { position: fixed; inset: 0; z-index: 40; width: 100%; height: 100%; padding: 0; border: 0; background: var(--scrim); cursor: default; }

  .popup {
    position: fixed; top: 50%; left: 50%; transform: translate(-50%, -50%);
    width: min(960px, calc(100vw - 48px)); max-height: calc(100dvh - 32px);
    display: flex; flex-direction: column;
    color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius);
    box-shadow: 0 24px 64px color-mix(in srgb, var(--bg) 70%, transparent);
    z-index: 45; outline: none;
  }
  .popup-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 12px; padding: 16px 20px 12px; border-bottom: 1px solid var(--line); }
  .popup-titles { min-width: 0; }
  .popup-head h2 { margin: 0; font-size: 18px; font-weight: 600; letter-spacing: -.01em; }
  .case-chip { display: inline-flex; align-items: center; gap: 6px; margin-top: 6px; padding: 2px 8px; color: var(--text-2); background: var(--surface-2); border: 1px solid var(--line); border-radius: var(--radius); font-size: 12px; }
  .case-chip b { color: var(--text); font-weight: 600; }
  .close-x { flex: 0 0 auto; display: grid; place-items: center; width: 32px; height: 32px; padding: 0; color: var(--text-2); background: transparent; border: 1px solid transparent; border-radius: var(--radius); font-size: 20px; line-height: 1; cursor: pointer; }
  .close-x:hover { color: var(--text); background: var(--surface-2); }

  .stepper { display: flex; align-items: center; gap: 0; padding: 10px 20px; border-bottom: 1px solid var(--line-soft); }
  .stp { display: flex; align-items: center; gap: 8px; padding: 6px 0; color: var(--text-2); background: none; border: 0; font-size: 13px; font-weight: 500; }
  .stp:not(:disabled) { cursor: pointer; }
  .stp:not(:disabled):hover { color: var(--text); }
  .stp .n { display: grid; place-items: center; width: 22px; height: 22px; border: 1px solid var(--line); border-radius: 50%; font-family: var(--mono); font-size: 12px; font-weight: 600; }
  .stp.done { color: var(--text); }
  .stp.done .n { border-color: var(--accent); color: var(--accent); }
  .stp.cur { color: var(--text); }
  .stp.cur .n { background: var(--accent); border-color: var(--accent); color: var(--accent-ink); }
  .stp-sep { flex: 0 0 auto; width: 28px; height: 1px; margin: 0 8px; background: var(--line); }
  .stp-sep.done { background: var(--accent); }
  .prog-line { padding: 6px 20px 8px; color: var(--text-2); font-size: 12px; border-bottom: 1px solid var(--line); background: var(--surface-2); }
  .prog-line b { color: var(--text); }

  .popup-body { flex: 1 1 auto; overflow-y: auto; overscroll-behavior: contain; padding: 18px 20px; scrollbar-width: thin; }
  .popup-foot { flex: 0 0 auto; display: flex; flex-direction: column; gap: 10px; padding: 12px 20px; border-top: 1px solid var(--line); background: var(--surface); }

  .btn { display: inline-flex; align-items: center; justify-content: center; gap: 8px; min-height: 36px; padding: 8px 14px; border-radius: var(--radius); font-size: 13px; font-weight: 600; cursor: pointer; transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease), color var(--dur) var(--ease); }
  .btn-primary { color: var(--accent-ink); background: var(--accent); border: 1px solid var(--accent); }
  .btn-primary:hover { background: color-mix(in srgb, var(--accent) 88%, var(--text)); }
  .btn-secondary { color: var(--text); background: transparent; border: 1px solid var(--line); }
  .btn-secondary:hover { border-color: var(--text-2); background: var(--surface-2); }
  .btn:disabled { cursor: not-allowed; }
  .btn-primary:disabled { color: var(--text-2); background: var(--surface-2); border-color: var(--control-line); }
  .btn:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }

  .foot-hint { display: flex; align-items: center; gap: 6px; color: var(--warn); font-size: 13px; font-weight: 600; }
  .foot-row { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
  .foot-spacer { flex: 1 1 auto; }
  .foot-note { flex-basis: 100%; color: var(--text-2); font-size: 12px; }
  .ack { display: flex; align-items: flex-start; gap: 8px; font-size: 13px; font-weight: 500; }
  .ack input { flex: 0 0 auto; width: 15px; height: 15px; margin: 3px 0 0; accent-color: var(--accent); }
  .ack span { min-width: 0; }

  .problem { display: grid; justify-items: start; gap: 12px; padding: 12px; background: var(--surface-2); border: 1px solid var(--warn); border-radius: var(--radius); }
  .problem p { margin: 0; font-size: 13px; }

  /* The footer carries Cancel, as in the wireframe, so the one the Importing step draws in the body is not shown twice. */
  .popup-body :global(.cancel-row) { display: none; }

  .discard-veil { position: absolute; inset: 0; z-index: 5; display: grid; place-items: center; padding: 20px; background: color-mix(in srgb, var(--bg) 50%, transparent); }
  .discard-card { width: min(420px, 100%); padding: 16px; background: var(--surface); border: 1px solid var(--warn); border-radius: var(--radius); box-shadow: 0 12px 32px color-mix(in srgb, var(--bg) 60%, transparent); }
  .discard-card h3 { margin: 0 0 6px; font-size: 15px; font-weight: 600; letter-spacing: -.01em; }
  .discard-card p { margin: 0 0 12px; color: var(--text-2); font-size: 13px; }
  .discard-actions { display: flex; flex-wrap: wrap; gap: 8px; justify-content: flex-end; }

  @media (pointer: coarse), (max-width: 720px) {
    .btn, .close-x { min-height: 44px; }
    .stp .n { width: 24px; height: 24px; }
  }
  @media (max-width: 720px) {
    .popup { top: 0; left: 0; width: 100vw; height: 100dvh; max-height: 100dvh; transform: none; border-radius: 0; }
  }
  @media (prefers-reduced-motion: reduce) {
    *, *::before, *::after { animation-duration: .01ms !important; transition-duration: .01ms !important; }
  }
</style>
