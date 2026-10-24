<script lang="ts">
  import { tick } from 'svelte';
  import { findTerm } from '../lib/copy/lookup';
  import { GLOSSARY_EVENT, hintBus } from '../lib/copy/hintState';

  // A small "?" button beside a technical word. It is a disclosure: the button has aria-expanded and aria-controls, and the explanation is a labelled, non-modal dialog.
  // The popover lives on <body> so a scrolling table or a clipped panel cannot cut it off. Because it is not next to the button in the page order, focus moves into it when it opens
  // (so a screen reader reads it at once), and Esc or Tab-away closes it and gives focus back to the button. Only one hint is open at a time. Nothing is stored and nothing leaves the browser.
  let { termId }: { termId: string } = $props();
  const term = $derived(findTerm(termId));
  const uid = $props.id();
  const popId = 'hint-' + uid;
  const titleId = 'hint-title-' + uid;
  let open = $state(false);
  let trigger = $state<HTMLButtonElement>();
  let pop = $state<HTMLElement>();
  let pos = $state({ left: 12, top: 0 });

  function portal(node: HTMLElement) {
    document.body.appendChild(node);
    return { destroy() { node.remove(); } };
  }
  function place() {
    if (!trigger || !pop) return;
    const anchor = trigger.getBoundingClientRect();
    const width = pop.offsetWidth, height = pop.offsetHeight;
    const left = Math.max(12, Math.min(anchor.left, window.innerWidth - width - 12));
    const below = anchor.bottom + 6;
    const top = below + height > window.innerHeight - 8 && anchor.top - 6 - height >= 8 ? anchor.top - 6 - height : below;
    pos = { left, top: Math.max(8, top) };
  }
  // The shared bus keeps one hint open: opening another one flips this one to closed.
  $effect(() => {
    const stop = hintBus.subscribe(id => { open = id === uid; });
    return () => { stop(); hintBus.close(uid); };
  });
  async function toggle() {
    if (open) { close(true); return; }
    hintBus.open(uid);
    await tick();
    place();
    pop?.focus({ preventScroll: true });
  }
  function close(returnFocus: boolean) {
    hintBus.close(uid);
    if (returnFocus) trigger?.focus();
  }
  // Close first and give focus back to the button, so the glossary drawer returns focus to a control that still exists.
  function glossary() {
    close(true);
    window.dispatchEvent(new CustomEvent(GLOSSARY_EVENT, { detail: { termId } }));
  }
  // Focus left both the button and the popover (Tab, or a click on another control): close without stealing focus.
  function leave(event: FocusEvent) {
    const next = event.relatedTarget as Node | null;
    if (!next || trigger?.contains(next) || pop?.contains(next)) return;
    close(false);
  }
  // Tab past either end of the popover: put focus back on the button first, so the browser carries on from the button's place in the page.
  function popKey(event: KeyboardEvent) {
    if (event.key !== 'Tab' || !pop) return;
    const stops = [pop, ...pop.querySelectorAll<HTMLElement>('button:not(:disabled), [href]')];
    const index = stops.indexOf(document.activeElement as HTMLElement);
    if (event.shiftKey ? index <= 0 : index === stops.length - 1) close(true);
  }
  $effect(() => {
    if (!open) return;
    // Enter and Space toggle through the button's own click. Esc is caught in the capture phase, so it closes this hint before the page treats it as "go up one level".
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.preventDefault(); event.stopPropagation();
      const active = document.activeElement;
      close(active == null || active === document.body || active === trigger || !!pop?.contains(active));
    };
    const away = (event: PointerEvent) => {
      const target = event.target as Node;
      if (!trigger?.contains(target) && !pop?.contains(target)) close(false);
    };
    const move = () => place();
    window.addEventListener('keydown', escape, true);
    window.addEventListener('pointerdown', away);
    window.addEventListener('resize', move);
    window.addEventListener('scroll', move, true);
    return () => {
      window.removeEventListener('keydown', escape, true);
      window.removeEventListener('pointerdown', away);
      window.removeEventListener('resize', move);
      window.removeEventListener('scroll', move, true);
    };
  });
</script>

{#if term}
  <span class="hint-wrap">
    <button type="button" class="hint-button" bind:this={trigger} aria-label={'What does ' + term.term + ' mean?'} aria-expanded={open} aria-controls={open ? popId : undefined} onclick={toggle} onfocusout={leave}><span aria-hidden="true">?</span></button>
  </span>
  {#if open}
    <div class="hint-popover" id={popId} role="dialog" aria-labelledby={titleId} tabindex="-1" bind:this={pop} use:portal onfocusout={leave} onkeydown={popKey} style:left={pos.left + 'px'} style:top={pos.top + 'px'}>
      <strong class="hint-term" id={titleId}>{term.term}</strong>
      <p class="hint-line">{term.hint}</p>
      <p>{term.definition}</p>
      <p class="hint-not"><span class="hint-not-label">What it does not mean</span>{term.notMeaning}</p>
      <button type="button" class="hint-more" onclick={glossary}>Learn more in the glossary</button>
    </div>
  {/if}
{/if}

<style>
  .hint-wrap { display: inline-flex; vertical-align: middle; margin-left: 4px; font-weight: 400; text-transform: none; letter-spacing: 0; }
  .hint-button { position: relative; display: inline-grid; place-items: center; width: 18px; height: 18px; padding: 0; color: var(--text-2); background: transparent; border: 1px solid var(--control-line); border-radius: 50%; font-size: 11px; font-weight: 650; line-height: 1; }
  /* The visible circle stays small; the tap target reaches 26px. */
  .hint-button::after { content: ""; position: absolute; inset: -4px; }
  .hint-button:hover, .hint-button[aria-expanded="true"] { color: var(--text); border-color: var(--accent); background: var(--surface-2); }
  .hint-popover { position: fixed; z-index: 60; width: min(340px, calc(100vw - 24px)); max-height: calc(100vh - 16px); overflow: auto; padding: 12px 14px; color: var(--text); background: var(--surface-2); border: 1px solid var(--line); border-radius: var(--radius); box-shadow: 0 12px 32px color-mix(in srgb, var(--bg) 70%, transparent); font-size: 13px; font-weight: 400; line-height: 1.45; text-align: left; text-transform: none; letter-spacing: 0; white-space: normal; animation: reveal var(--dur) var(--ease); }
  .hint-popover p { margin: 6px 0 0; }
  .hint-term { font-size: 13px; font-weight: 650; }
  .hint-line { font-weight: 600; }
  .hint-not { padding-top: 8px; border-top: 1px solid var(--line); color: var(--text-2); }
  .hint-not-label { display: block; margin-bottom: 2px; color: var(--text); font-size: 11px; font-weight: 650; letter-spacing: .09em; text-transform: uppercase; }
  .hint-more { min-height: 26px; margin: 8px 0 0; padding: 4px 0; color: var(--accent); background: transparent; border: 0; font-size: 12px; font-weight: 600; text-align: left; text-decoration: underline; text-underline-offset: 3px; }
  .hint-more:hover { color: var(--text); }
</style>
