<script lang="ts">
  import { onDestroy, onMount, type Snippet } from 'svelte';

  // The one drawer template: a title row (title, one-line purpose, close button), optional header extras such as tabs, the body, and an optional footer of actions.
  // Modal (the default): the scrim, the dialog box, Esc to close and a Tab trap, as Glossary, Patterns, Ledger, Export and Connections use it.
  // Not modal: a side panel that leaves the page beside it usable (the inspector). It has no scrim, no focus trap and does not handle Esc; the page owns that.
  // Every new prop has a default that keeps the old behaviour, so existing drawers need no change.
  // busy (off by default) is for a drawer that is saving something: Esc, the dimmed page and the close button leave it open until the work ends, and a busy line (busyText) and bar say why.
  // Each drawer keeps its own list of focusable elements so Tab cycles exactly as it did before the shell existed.
  // returnFocus is off by default because the page already hands focus back when it closes each drawer; a new drawer can turn it on.
  const defaultFocusable = 'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), summary, [href], [tabindex]:not([tabindex="-1"])';

  let {
    title,
    titleId,
    intro = '',
    closeLabel,
    closeTitle = '',
    class: extra = '',
    focusable = defaultFocusable,
    stopEscape = true,
    returnFocus = false,
    modal = true,
    focusTitle = false,
    focusOnOpen = false,
    busy = false,
    busyText = '',
    element = $bindable(),
    onClose,
    headerExtra,
    footer,
    children
  }: {
    title: string;
    titleId: string;
    intro?: string;
    closeLabel: string;
    closeTitle?: string;
    class?: string;
    focusable?: string;
    stopEscape?: boolean;
    returnFocus?: boolean;
    modal?: boolean;
    focusTitle?: boolean;
    focusOnOpen?: boolean;
    busy?: boolean;
    busyText?: string;
    element?: HTMLElement;
    onClose: () => void;
    headerExtra?: Snippet;
    footer?: Snippet;
    children: Snippet;
  } = $props();

  const opener = typeof document !== 'undefined' && document.activeElement instanceof HTMLElement ? document.activeElement : null;
  onDestroy(() => { if (returnFocus) opener?.focus(); });
  // focusOnOpen (off by default) puts focus on the title when the drawer opens, so a keyboard user starts inside it and not on the page behind.
  let titleElement = $state<HTMLElement>();
  onMount(() => { if (focusOnOpen) titleElement?.focus({ preventScroll: true }); });

  // While busy nothing closes the drawer. Esc is still taken (so the page behind does not step up either), but the close is skipped.
  const close = () => { if (!busy) onClose(); };

  function onKey(event: KeyboardEvent) {
    if (event.key === 'Escape') { event.preventDefault(); if (stopEscape) event.stopPropagation(); close(); return; }
    if (event.key !== 'Tab' || !element) return;
    // Only controls that are on screen count: a hidden one (display:none) can never take focus, so as the "last" item it stopped the wrap and Tab left the dialog.
    const items = [...element.querySelectorAll<HTMLElement>(focusable)].filter((item) => item.getClientRects().length > 0);
    if (!items.length) return;
    const first = items[0], last = items[items.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }
</script>

{#if modal}<button type="button" class="drawer-scrim" aria-label={closeLabel} onclick={close}></button>{/if}
<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<svelte:element this={modal ? 'div' : 'aside'} class={modal ? (extra ? 'connections-drawer ' + extra : 'connections-drawer') : extra} bind:this={element} onkeydown={modal ? onKey : undefined} role={modal ? 'dialog' : undefined} aria-modal={modal ? 'true' : undefined} aria-labelledby={titleId} tabindex={modal ? -1 : undefined}>
  <div class="drawer-header" class:panel={!modal} class:has-extra={!!headerExtra}>
    <div class="drawer-heading"><h2 id={titleId} bind:this={titleElement} tabindex={focusTitle || focusOnOpen ? -1 : undefined}>{title}</h2>{#if intro}<p>{intro}</p>{/if}</div>
    <button type="button" class="drawer-close" class:busy aria-label={closeLabel} aria-disabled={busy ? 'true' : undefined} title={closeTitle || undefined} onclick={close}>×</button>
  </div>
  {#if headerExtra}{@render headerExtra()}{/if}
  {#if busy}
    {#if busyText}<div class="drawer-busy" role="status"><span>{busyText}</span></div>{/if}
    <div class="drawer-busy-bar" aria-hidden="true"></div>
  {/if}
  {@render children()}
  {#if footer}<div class="drawer-footer" class:panel={!modal}>{@render footer()}</div>{/if}
</svelte:element>

<style>
  /* The title row. The 32px close button clears the 24px minimum target size. */
  .drawer-header { display: flex; justify-content: space-between; align-items: flex-start; gap: var(--space-3); padding-bottom: var(--space-3); border-bottom: 1px solid var(--hairline); }
  /* A modal drawer keeps its title row and close button in view while the body scrolls, also at 400 % zoom (SETUP-A11Y-20). It pins past the drawer's own padding, like the footer does at the bottom. */
  .drawer-header:not(.panel) { position: sticky; top: -22px; z-index: 3; margin: -22px -26px 0; padding: 22px 26px var(--space-3); background: var(--surface); }
  .drawer-header.panel { flex: 0 0 auto; padding: var(--space-4) var(--space-5) 0; border-bottom: 0; }
  .drawer-header.panel:not(.has-extra) { padding-bottom: var(--space-3); border-bottom: 1px solid var(--hairline); }
  .drawer-heading { min-width: 0; }
  .drawer-header h2 { margin: 0; font-size: var(--fs-6); font-weight: 600; letter-spacing: -.01em; line-height: 1.2; overflow-wrap: anywhere; }
  .drawer-header h2:focus-visible { outline-offset: 3px; border-radius: var(--radius-sm); }
  .drawer-header p { margin: var(--space-1) 0 0; color: var(--text-2); font-size: var(--fs-3); line-height: 1.45; overflow-wrap: anywhere; }
  .drawer-close { flex: 0 0 auto; display: grid; place-items: center; width: 32px; height: 32px; padding: 0; color: var(--text-2); background: transparent; border: 1px solid transparent; border-radius: var(--radius-sm); font-size: 20px; line-height: 1; }
  .drawer-close:hover { color: var(--text); background: var(--surface-2); }
  .drawer-close.busy { opacity: .45; cursor: default; }
  .drawer-close.busy:hover { color: var(--text-2); background: transparent; }
  /* The busy line and the moving hatched bar (the approved prototype). The bar stands still when the person asked for less motion. */
  .drawer-busy { display: flex; align-items: center; gap: var(--space-3); margin: var(--space-3) 0 0; padding: var(--space-2) var(--space-3); color: var(--text); background: var(--surface-2); border: 1px solid var(--control-line); border-radius: var(--radius-sm); font-size: var(--fs-3); }
  .drawer-busy-bar { height: 4px; margin-top: var(--space-3); background-color: var(--surface-2); background-image: var(--hatch); background-size: 12px 12px; border-radius: 1px; animation: drawer-busy-slide 1.2s linear infinite; }
  @keyframes drawer-busy-slide { to { background-position: 12px 0; } }
  @media (prefers-reduced-motion: reduce) { .drawer-busy-bar { animation: none; } }

  /* The footer holds the drawer's actions. It stays at the bottom edge of a modal drawer and sits under a hairline. */
  .drawer-footer { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2) var(--space-3); margin-top: auto; padding: var(--space-3) 0 0; border-top: 1px solid var(--hairline); }
  .drawer-footer:not(.panel) { position: sticky; bottom: -30px; margin: auto -26px -30px; padding: var(--space-3) 26px; background: var(--surface); }
  .drawer-footer.panel { flex: 0 0 auto; padding: var(--space-3) var(--space-5); background: var(--bg); }
  @media (max-width: 720px) {
    .drawer-footer:not(.panel) { bottom: -24px; margin: auto -15px -24px; padding: var(--space-3) 15px; }
    .drawer-header:not(.panel) { top: -17px; margin: -17px -15px 0; padding: 17px 15px var(--space-3); }
  }
  @media (max-width: 899px) {
    .drawer-header.panel { padding-left: 0; padding-right: 0; }
    .drawer-footer.panel { padding-left: 0; padding-right: 0; }
  }

  /* One look for what drawers hold, so a drawer need not restyle it: sections under hairlines, chevron disclosures and findings as plain rows. */
  :global(.connections-drawer .drawer-section) { padding-top: var(--space-4); border-top: 1px solid var(--hairline); }
  /* The chevron itself is the app-wide rule in app.css (summary::before); a drawer only lines it up with the text. */
  :global(.connections-drawer details > summary) { display: flex; align-items: center; cursor: pointer; }
  :global(.connections-drawer .finding) { padding: var(--space-3) 0; background: transparent; border: 0; border-top: 1px solid var(--hairline); border-radius: 0; }
  :global(.connections-drawer .low-n) { padding: 0; color: var(--text-2); border: 0; border-radius: 0; font-style: italic; }
</style>
