<script lang="ts">
  import { onDestroy, type Snippet } from 'svelte';

  // The one drawer template: a title row (title, one-line purpose, close button), optional header extras such as tabs, the body, and an optional footer of actions.
  // Modal (the default): the scrim, the dialog box, Esc to close and a Tab trap, as Glossary, Patterns, Ledger, Export and Connections use it.
  // Not modal: a side panel that leaves the page beside it usable (the inspector). It has no scrim, no focus trap and does not handle Esc; the page owns that.
  // Every new prop has a default that keeps the old behaviour, so existing drawers need no change.
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
    element?: HTMLElement;
    onClose: () => void;
    headerExtra?: Snippet;
    footer?: Snippet;
    children: Snippet;
  } = $props();

  const opener = typeof document !== 'undefined' && document.activeElement instanceof HTMLElement ? document.activeElement : null;
  onDestroy(() => { if (returnFocus) opener?.focus(); });

  function onKey(event: KeyboardEvent) {
    if (event.key === 'Escape') { event.preventDefault(); if (stopEscape) event.stopPropagation(); onClose(); return; }
    if (event.key !== 'Tab' || !element) return;
    const items = [...element.querySelectorAll<HTMLElement>(focusable)];
    if (!items.length) return;
    const first = items[0], last = items[items.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }
</script>

{#if modal}<button type="button" class="drawer-scrim" aria-label={closeLabel} onclick={onClose}></button>{/if}
<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<svelte:element this={modal ? 'div' : 'aside'} class={modal ? (extra ? 'connections-drawer ' + extra : 'connections-drawer') : extra} bind:this={element} onkeydown={modal ? onKey : undefined} role={modal ? 'dialog' : undefined} aria-modal={modal ? 'true' : undefined} aria-labelledby={titleId} tabindex={modal ? -1 : undefined}>
  <div class="drawer-header" class:panel={!modal} class:has-extra={!!headerExtra}>
    <div class="drawer-heading"><h2 id={titleId} tabindex={focusTitle ? -1 : undefined}>{title}</h2>{#if intro}<p>{intro}</p>{/if}</div>
    <button type="button" class="drawer-close" aria-label={closeLabel} title={closeTitle || undefined} onclick={onClose}>×</button>
  </div>
  {#if headerExtra}{@render headerExtra()}{/if}
  {@render children()}
  {#if footer}<div class="drawer-footer" class:panel={!modal}>{@render footer()}</div>{/if}
</svelte:element>

<style>
  /* The title row. The 32px close button clears the 24px minimum target size. */
  .drawer-header { display: flex; justify-content: space-between; align-items: flex-start; gap: var(--space-3); padding-bottom: var(--space-3); border-bottom: 1px solid var(--hairline); }
  .drawer-header.panel { flex: 0 0 auto; padding: var(--space-4) var(--space-5) 0; border-bottom: 0; }
  .drawer-header.panel:not(.has-extra) { padding-bottom: var(--space-3); border-bottom: 1px solid var(--hairline); }
  .drawer-heading { min-width: 0; }
  .drawer-header h2 { margin: 0; font-size: var(--fs-6); font-weight: 600; line-height: 1.2; overflow-wrap: anywhere; }
  .drawer-header h2:focus-visible { outline-offset: 3px; border-radius: var(--radius); }
  .drawer-header p { margin: var(--space-1) 0 0; color: var(--text-2); font-size: var(--fs-3); line-height: 1.45; overflow-wrap: anywhere; }
  .drawer-close { flex: 0 0 auto; display: grid; place-items: center; width: 32px; height: 32px; padding: 0; color: var(--text-2); background: transparent; border: 1px solid var(--line); border-radius: var(--radius); font-size: 20px; line-height: 1; }
  .drawer-close:hover { color: var(--text); border-color: var(--text-2); background: var(--surface-2); }

  /* The footer holds the drawer's actions. It stays at the bottom edge of a modal drawer and sits under a hairline. */
  .drawer-footer { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2) var(--space-3); margin-top: auto; padding: var(--space-3) 0 0; border-top: 1px solid var(--hairline); }
  .drawer-footer:not(.panel) { position: sticky; bottom: -30px; margin: auto -26px -30px; padding: var(--space-3) 26px; background: var(--surface); }
  .drawer-footer.panel { flex: 0 0 auto; padding: var(--space-3) var(--space-5); background: var(--bg); }
  @media (max-width: 720px) {
    .drawer-footer:not(.panel) { bottom: -24px; margin: auto -15px -24px; padding: var(--space-3) 15px; }
  }
  @media (max-width: 899px) {
    .drawer-header.panel { padding-left: 0; padding-right: 0; }
    .drawer-footer.panel { padding-left: 0; padding-right: 0; }
  }

  /* One look for what drawers hold, so a drawer need not restyle it: sections under hairlines, chevron disclosures and findings as plain rows. */
  :global(.connections-drawer .drawer-section) { padding-top: var(--space-4); border-top: 1px solid var(--hairline); }
  :global(.connections-drawer details > summary) { display: flex; align-items: center; gap: var(--space-2); list-style: none; cursor: pointer; }
  :global(.connections-drawer details > summary::-webkit-details-marker) { display: none; }
  :global(.connections-drawer details > summary::after) { content: ""; flex: 0 0 auto; width: 6px; height: 6px; border-right: 1.5px solid currentColor; border-bottom: 1.5px solid currentColor; transform: rotate(45deg) translateY(-2px); transition: transform var(--dur) var(--ease); }
  :global(.connections-drawer details[open] > summary::after) { transform: rotate(-135deg) translateY(-2px); }
  :global(.connections-drawer .finding) { padding: var(--space-3) 0; background: transparent; border: 0; border-top: 1px solid var(--hairline); border-radius: 0; }
  :global(.connections-drawer .low-n) { padding: 0; color: var(--text-2); border: 0; border-radius: 0; font-style: italic; }
</style>
