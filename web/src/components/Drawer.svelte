<script lang="ts">
  import { onDestroy, type Snippet } from 'svelte';

  // The shared modal drawer shell: the scrim, the dialog box, the title row with its close button, Esc to close and a Tab trap.
  // Each drawer keeps its own list of focusable elements so Tab cycles exactly as it did before the shell existed.
  // returnFocus is off by default because the page already hands focus back when it closes each drawer; a new drawer can turn it on.
  const defaultFocusable = 'button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), summary, [href], [tabindex]:not([tabindex="-1"])';

  let {
    title,
    titleId,
    intro = '',
    closeLabel,
    class: extra = '',
    focusable = defaultFocusable,
    stopEscape = true,
    returnFocus = false,
    element = $bindable(),
    onClose,
    children
  }: {
    title: string;
    titleId: string;
    intro?: string;
    closeLabel: string;
    class?: string;
    focusable?: string;
    stopEscape?: boolean;
    returnFocus?: boolean;
    element?: HTMLElement;
    onClose: () => void;
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

<button type="button" class="drawer-scrim" aria-label={closeLabel} onclick={onClose}></button>
<div class={extra ? 'connections-drawer ' + extra : 'connections-drawer'} bind:this={element} onkeydown={onKey} role="dialog" aria-modal="true" aria-labelledby={titleId} tabindex="-1">
  <div class="drawer-header"><div><h2 id={titleId}>{title}</h2>{#if intro}<p>{intro}</p>{/if}</div><button type="button" class="icon-button drawer-close" aria-label={closeLabel} onclick={onClose}>×</button></div>
  {@render children()}
</div>
