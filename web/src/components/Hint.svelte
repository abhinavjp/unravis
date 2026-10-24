<script lang="ts">
  import { tick } from 'svelte';
  import { findTerm } from '../lib/copy/lookup';

  // A small "?" button beside a technical word. It opens a short explanation taken from the copy deck: the hint, the definition and what the term does not mean.
  // The text is built in the copy deck, never here. Nothing is stored and nothing leaves the browser. Focus is never trapped.
  let { termId }: { termId: string } = $props();
  const term = $derived(findTerm(termId));
  const uid = $props.id();
  const popId = 'hint-' + uid;
  let open = $state(false);
  let trigger = $state<HTMLButtonElement>();
  let pop = $state<HTMLElement>();
  let pos = $state({ left: 12, top: 0 });

  // The popover lives on <body> so a scrolling table or a clipped panel cannot cut it off.
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
  async function toggle() {
    open = !open;
    if (open) { await tick(); place(); }
  }
  function close(returnFocus: boolean) {
    open = false;
    if (returnFocus) trigger?.focus();
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
    <button type="button" class="hint-button" bind:this={trigger} aria-label={'What does ' + term.term + ' mean?'} aria-expanded={open} aria-describedby={open ? popId : undefined} onclick={toggle}><span aria-hidden="true">?</span></button>
  </span>
  {#if open}
    <div class="hint-popover" id={popId} role="tooltip" bind:this={pop} use:portal style:left={pos.left + 'px'} style:top={pos.top + 'px'}>
      <strong class="hint-term">{term.term}</strong>
      <p class="hint-line">{term.hint}</p>
      <p>{term.definition}</p>
      <p class="hint-not"><span class="hint-not-label">What it does not mean</span>{term.notMeaning}</p>
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
</style>
