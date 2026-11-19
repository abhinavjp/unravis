<script lang="ts">
  // The pop-up shell for the Blackbox wizard (variant A: the progress rail sits on top).
  // It owns the native <dialog>, the focus trap and focus restore, Esc, Enter and the "leave?" question.
  // It owns no copy: every word comes in through props and snippets, so the Remove and Status pop-up (BL-131) can reuse it.
  import { onDestroy, tick } from 'svelte';
  import type { Snippet } from 'svelte';
  import { enterDoesPrimary, escapeAction, wrapFocus } from '../../lib/wizardKeys';

  interface Labels {
    close: string;        // aria-label of the corner close button
    leaveTitle: string;   // the question, for example "Leave without placing it?"
    leaveBody: string;    // one plain line under it
    keepGoing: string;    // the safe button, focused first
    leave: string;        // the leaving button
    busyNote: string;     // shown when Esc or close is used while work is running
  }

  let {
    open,
    titleId,
    busy,
    finished,
    labels,
    onLeave,
    onPrimary,
    enterNeedsButton = false,
    header,
    rail,
    body,
    footer
  }: {
    open: boolean;
    titleId: string;           // the id of the h2 that the header snippet renders; also the dialog's accessible name
    busy: boolean;
    finished: boolean;
    labels: Labels;
    onLeave: () => void;       // after the owner confirms Leave, or at once when finished
    onPrimary: () => void;     // Enter, when it is allowed
    enterNeedsButton?: boolean; // true on the step whose primary button really does the work: Enter then acts only on that button itself
    header: Snippet;
    rail: Snippet;
    body: Snippet;
    footer: Snippet;
  } = $props();

  const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), summary, [tabindex]:not([tabindex="-1"])';
  const NOTE_MS = 6000;

  let dialog = $state<HTMLDialogElement | null>(null);
  let keepButton = $state<HTMLButtonElement | null>(null);
  let leaving = $state(false);      // the leave question is showing
  let note = $state('');            // the short "why nothing happened" line
  let announced = $state('');       // the step title, said out loud when focus lands on a field instead of the heading
  let opener: HTMLElement | null = null;       // what had focus before the pop-up opened
  let beforeLeave: HTMLElement | null = null;  // what had focus before the leave question opened
  let noteTimer: ReturnType<typeof setTimeout> | undefined;
  let lastEscape = -Infinity;       // one key press must count once, even if the browser also fires "cancel"

  const leaveTitleId = $derived(titleId + '-leave-title');
  const leaveBodyId = $derived(titleId + '-leave-body');

  // Puts focus where the new step starts. Called by the wizard on every step change.
  //   - A step with a first text field (Name, Keep days, Folder) marks it data-autofocus: that field takes focus so typing can start.
  //   - Every other step puts focus on the heading, so a screen reader starts at the new step.
  // Focus on a field would skip the heading, so the title is also said out loud through the polite live region.
  export function focusStep(): void {
    const heading = dialog?.querySelector<HTMLElement>('#' + CSS.escape(titleId));
    if (!heading) return;
    const field = dialog?.querySelector<HTMLElement>('.shell-body [data-autofocus]:not([disabled])');
    const scroller = dialog?.querySelector<HTMLElement>('.shell-body');
    if (field && field.getClientRects().length > 0) {
      field.focus({ preventScroll: true });
      announce(heading.textContent ?? '');
    } else {
      if (!heading.hasAttribute('tabindex')) heading.setAttribute('tabindex', '-1');
      heading.focus({ preventScroll: true });
      announce('');
    }
    scroller?.scrollTo({ top: 0 });
  }

  // Cleared first and filled on the next tick, so the same title said twice in a row is still read twice.
  function announce(text: string): void {
    announced = '';
    if (!text) return;
    void tick().then(() => { announced = text; });
  }

  function visibleFocusables(scope: ParentNode | null): HTMLElement[] {
    if (!scope) return [];
    return [...scope.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) => el.getClientRects().length > 0);
  }

  function showNote(): void {
    note = labels.busyNote;
    clearTimeout(noteTimer);
    noteTimer = setTimeout(() => { note = ''; }, NOTE_MS);
  }

  // Opens and closes the native dialog when the open prop changes, and puts focus back where it was.
  $effect(() => {
    if (!dialog) return;
    if (open && !dialog.open) {
      opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      leaving = false;
      note = '';
      dialog.showModal();
      void tick().then(focusStep);
    } else if (!open && dialog.open) {
      leaving = false;
      dialog.close();
      restoreOpener();
    }
  });

  // Work starting hides the leave question: nothing can be left while it runs.
  $effect(() => {
    if (busy && leaving) leaving = false;
    if (!busy) { note = ''; clearTimeout(noteTimer); }
  });

  // The leave question opens with "Keep going" focused, the safe answer.
  $effect(() => {
    if (leaving && keepButton) keepButton.focus();
  });

  function restoreOpener(): void {
    const target = opener;
    opener = null;
    if (target && target.isConnected) target.focus({ preventScroll: true });
  }

  onDestroy(() => {
    clearTimeout(noteTimer);
    if (dialog?.open) { dialog.close(); restoreOpener(); }
  });

  function askLeave(): void {
    beforeLeave = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    leaving = true;
  }

  function keepGoing(): void {
    leaving = false;
    const target = beforeLeave;
    beforeLeave = null;
    void tick().then(() => {
      if (target && target.isConnected && dialog?.contains(target)) target.focus({ preventScroll: true });
      else focusStep();
    });
  }

  function confirmLeave(): void {
    leaving = false;
    onLeave();
  }

  /** For a close button outside the shell (the pop-up footer): asks first, like Esc and the corner X. */
  export function requestLeave(): void { handleLeaveRequest(); }

  // Esc, the browser's own "cancel" and the close button all land here.
  function handleLeaveRequest(): void {
    const action = escapeAction(busy, finished, leaving);
    if (action === 'note') showNote();
    else if (action === 'keep') keepGoing();
    else if (action === 'leave') onLeave();
    else askLeave();
  }

  function onCancel(event: Event): void {
    event.preventDefault();
    if (event.timeStamp - lastEscape < 80) return;
    lastEscape = event.timeStamp;
    handleLeaveRequest();
  }

  // If the browser closes the dialog by itself (some browsers close on a second Esc) while the wizard is still open, open it again and ask.
  function onClose(): void {
    if (!open || !dialog || dialog.open) return;
    dialog.showModal();
    if (!finished && !busy) askLeave();
    else void tick().then(focusStep);
  }

  function onKey(event: KeyboardEvent): void {
    if (event.key === 'Escape') {
      event.preventDefault();
      event.stopPropagation();
      if (event.timeStamp - lastEscape < 80) return;
      lastEscape = event.timeStamp;
      handleLeaveRequest();
      return;
    }
    if (event.key === 'Tab') { trapTab(event); return; }
    if (event.key !== 'Enter') return;
    if (event.defaultPrevented || event.isComposing || event.repeat) return;
    if (event.ctrlKey || event.altKey || event.metaKey || event.shiftKey) return;
    if (leaving) return;   // the question's own buttons answer Enter themselves
    if (!enterDoesPrimary(event.target as HTMLElement | null, enterNeedsButton)) return;
    event.preventDefault();
    if (busy) return;      // a second Enter while work runs must not start it twice
    onPrimary();
  }

  // The browser keeps focus inside a modal dialog, but it can slip to the address bar. This keeps Tab cycling inside the dialog,
  // or inside the leave question while that is showing.
  function trapTab(event: KeyboardEvent): void {
    const scope = leaving ? dialog?.querySelector('.leave') ?? null : dialog;
    const items = visibleFocusables(scope);
    const index = items.indexOf(document.activeElement as HTMLElement);
    const to = wrapFocus(items.length, index, event.shiftKey);
    if (to === null) return;
    event.preventDefault();
    items[to]?.focus();
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
<dialog class="pop" bind:this={dialog} aria-labelledby={titleId} oncancel={onCancel} onclose={onClose} onkeydown={onKey}>
  <div class="frame">
    <div class="shell" inert={leaving}>
      <button type="button" class="x" aria-label={labels.close} onclick={handleLeaveRequest}>&times;</button>
      <div class="shell-rail">{@render rail()}</div>
      <div class="shell-head">{@render header()}</div>
      <div class="shell-body">{@render body()}</div>
      <div class="shell-foot">{@render footer()}</div>
    </div>
    {#if leaving}
      <div class="leave" role="alertdialog" aria-modal="true" aria-labelledby={leaveTitleId} aria-describedby={leaveBodyId}>
        <div class="leave-card">
          <h3 id={leaveTitleId}>{labels.leaveTitle}</h3>
          <p id={leaveBodyId}>{labels.leaveBody}</p>
          <div class="leave-actions">
            <button type="button" class="primary-button" bind:this={keepButton} onclick={keepGoing}>{labels.keepGoing}</button>
            <button type="button" class="secondary-button" onclick={confirmLeave}>{labels.leave}</button>
          </div>
        </div>
      </div>
    {/if}
    <p class="busy-note" role="status" aria-live="polite" class:shown={note !== ''}>{note}</p>
    <p class="sr-only" role="status" aria-live="polite">{announced}</p>
  </div>
</dialog>

<style>
  dialog.pop { font-size: 14px; width: min(680px, calc(100vw - 32px)); max-width: calc(100vw - 32px); max-height: calc(100vh - 48px); max-height: calc(100dvh - 48px); margin: auto; padding: 0; overflow: hidden; color: var(--text); background: var(--surface); border: 1px solid var(--hairline); border-radius: var(--radius-md); box-shadow: 0 24px 64px var(--scrim); }
  dialog.pop::backdrop { background: var(--scrim); }
  /* The drawer styles every details/summary inside it with a chevron and a gap. This dialog sits inside the drawer, so undo that here. */
  :global(dialog.pop.pop details > summary) { gap: 0; }
  :global(dialog.pop.pop details > summary::after) { content: none; display: none; }
  /* The shared target picker is made for the small settings panel. In the pop-up its rows are bigger, so a finger or a pointer can hit them. */
  :global(dialog.pop .bb-check) { align-items: center; min-height: 36px; font-size: var(--fs-4); }
  :global(dialog.pop .bb-check input) { width: 18px; height: 18px; margin: 0; accent-color: var(--accent); }
  :global(dialog.pop .bb-scroll) { gap: 0; max-height: 280px; }
  :global(dialog.pop .bb-note) { font-size: var(--fs-3); }
  dialog.pop[open] { animation: pop-in var(--dur) var(--ease); }
  @keyframes pop-in { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: none; } }

  .frame { position: relative; display: grid; max-height: inherit; }
  .shell { position: relative; display: grid; grid-template-rows: auto auto minmax(0, 1fr) auto; max-height: calc(100vh - 50px); max-height: calc(100dvh - 50px); min-height: 0; }
  /* The right side is kept clear for the close button (12px in, 40px wide, 8px gap), so the last dot never sits under it. A rail
     that is wider than what is left scrolls sideways. */
  .shell-rail { min-width: 0; padding: var(--space-5) var(--space-6) 0 var(--space-6); }
  .shell-head { display: grid; gap: var(--space-1); padding: var(--space-5) var(--space-6) var(--space-4); border-bottom: 1px solid var(--hairline); }
  .shell-body { display: grid; align-content: start; gap: var(--space-5); min-height: 0; padding: var(--space-6); overflow: auto; }
  .shell-foot { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2) var(--space-3); padding: var(--space-4) var(--space-6); border-top: 1px solid var(--hairline); background: var(--surface); }

  /* The close button sits in the top corner, 40px square. */
  .x { position: absolute; top: var(--space-3); right: var(--space-3); z-index: 2; display: grid; place-items: center; width: 40px; height: 40px; padding: 0; color: var(--text-2); background: transparent; border: 1px solid transparent; border-radius: var(--radius-sm); font-size: var(--fs-6); line-height: 1; }
  .x:hover { color: var(--text); background: var(--surface-2); }

  /* The leave question covers the pop-up. The rest of the pop-up is inert while it shows. */
  .leave { position: absolute; inset: 0; z-index: 5; display: grid; place-items: center; padding: var(--space-4); background: var(--scrim); }
  .leave-card { display: grid; gap: var(--space-3); width: min(380px, 100%); padding: var(--space-6); background: var(--surface); border: 1px solid var(--hairline); border-radius: var(--radius-md); }
  .leave-card h3 { margin: 0; font-size: var(--fs-5); line-height: 1.3; }
  .leave-card p { margin: 0; color: var(--text-2); font-size: var(--fs-4); line-height: 1.45; }
  .leave-actions { display: flex; flex-wrap: wrap; gap: var(--space-3); padding-top: var(--space-1); }
  .leave-actions button { min-height: 44px; padding: 0 var(--space-5); font-size: var(--fs-4); }

  /* The "why nothing happened" line floats above the footer and is announced politely. */
  .busy-note { position: absolute; left: var(--space-6); right: var(--space-6); bottom: 76px; z-index: 3; margin: 0; padding: var(--space-2) var(--space-3); color: var(--text); font-size: var(--fs-3); background: var(--surface-2); border-left: 3px solid var(--warn); visibility: hidden; }
  .busy-note.shown { visibility: visible; }
  /* Said but never seen: no room on the page, nothing hidden from assistive tech. */
  .sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; overflow: hidden; clip-path: inset(50%); white-space: nowrap; border: 0; }

  /* On a phone the pop-up is the whole screen. */
  @media (max-width: 640px) {
    dialog.pop { width: 100vw; max-width: 100vw; height: 100vh; height: 100dvh; max-height: 100vh; max-height: 100dvh; border: 0; border-radius: 0; box-shadow: none; }
    .frame { height: 100%; }
    .shell { height: 100%; max-height: none; }
    .shell-rail { padding: var(--space-4) calc(var(--space-3) + 40px + var(--space-2)) 0 var(--space-4); }
    .shell-head { padding-left: var(--space-4); padding-right: calc(var(--space-4) + 32px); }
    .shell-body { padding: var(--space-4); }
    .shell-foot { padding: var(--space-3) var(--space-4); }
    .busy-note { left: var(--space-4); right: var(--space-4); }
  }

  @media (prefers-reduced-motion: reduce) {
    dialog.pop[open] { animation: none; }
    dialog.pop, dialog.pop * { transition: none !important; }
  }
</style>
