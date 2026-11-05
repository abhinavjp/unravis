<script lang="ts">
  import { onMount, type Snippet } from 'svelte';
  import { POPUP_TEXT } from '../../copy/importPopupCopy';
  import type { Query } from '../../types';
  import { createPopupState, type PopupContext, type PopupState } from './popupState.svelte';

  // The shell of the "Import files" popup: scrim, title row, stepper, progress line, body, footer and the discard confirmation.
  // The steps themselves are drawn by the `body` and `footer` snippets, which get the popup state and read `state.step`.
  // Focus: the popup takes focus when it opens; Tab stays inside it (inside the confirmation while that is open); Esc and a click outside
  // ask to close, and answer "Keep them" when the confirmation is already open. `state.focus` moves focus to a `data-focus` element.
  const FOCUSABLE = 'button:not(:disabled), input:not(:disabled), select:not(:disabled), [href], [tabindex]:not([tabindex="-1"])';

  let {
    caseId,
    caseTitle,
    query,
    zone,
    onClose,
    onOpenTimeline,
    retryServerName = null,
    popup = createPopupState(),
    body,
    footer
  }: {
    caseId: string;
    caseTitle: string;
    query: Query;
    zone: string;
    onClose: () => void;
    /** Called just before onClose when the person chose "Open timeline". */
    onOpenTimeline?: () => void;
    retryServerName?: string | null;
    popup?: PopupState;
    body?: Snippet<[PopupContext]>;
    footer?: Snippet<[PopupContext]>;
  } = $props();

  let popupEl = $state<HTMLElement>();
  let veilEl = $state<HTMLElement>();
  const context = $derived<PopupContext>({ state: popup, caseId, caseTitle, query, zone });

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
    void popup.report; void popup.checking; void popup.step; void popup.discard;
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

  <div class="popup-body">{#if body}{@render body(context)}{/if}</div>
  <div class="popup-foot">{#if footer}{@render footer(context)}{/if}</div>

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
