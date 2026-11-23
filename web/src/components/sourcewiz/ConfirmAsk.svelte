<script lang="ts">
  import { onMount } from 'svelte';

  // One small question in a real pop-up, for the Add sources guards: leave a form that holds typed input, start over, look again. The page behind
  // is inert; Escape and the dark backdrop both mean "keep working". Keeping is the focused, primary choice, so a stray Enter loses nothing.
  // Nothing typed is read or shown here: the question only says that something would be lost.
  let { title, text, keepLabel, leaveLabel, onKeep, onLeave, id = '' }: { title: string; text: string; keepLabel: string; leaveLabel: string; onKeep: () => void; onLeave: () => void; id?: string } = $props();

  const uid = $props.id();
  let dialog = $state<HTMLDialogElement>();
  let keep = $state<HTMLButtonElement>();
  // Focus goes back to what had it, so a keyboard user is where they were.
  const opener = typeof document !== 'undefined' ? (document.activeElement as HTMLElement | null) : null;

  onMount(() => {
    if (dialog && !dialog.open) dialog.showModal();
    keep?.focus();
    return () => { if (dialog?.open) dialog.close(); if (opener && opener.isConnected) opener.focus({ preventScroll: true }); };
  });
</script>

<dialog class="ask" bind:this={dialog} data-confirm-ask={id || undefined} aria-labelledby="{uid}-t" aria-describedby="{uid}-x" oncancel={(e) => { e.preventDefault(); onKeep(); }} onclick={(e) => { if (e.target === e.currentTarget) onKeep(); }}>
  <strong id="{uid}-t">{title}</strong>
  <p id="{uid}-x">{text}</p>
  <div class="actions">
    <button type="button" class="primary-button" bind:this={keep} onclick={onKeep}>{keepLabel}</button>
    <button type="button" class="secondary-button" onclick={onLeave}>{leaveLabel}</button>
  </div>
</dialog>

<style>
  .ask { box-sizing: border-box; width: min(440px, calc(100vw - 32px)); padding: var(--space-5); color: var(--text); background: var(--surface); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; }
  .ask[open] { display: grid; gap: var(--space-3); }
  .ask::backdrop { background: var(--scrim); }
  .ask strong { font-size: var(--fs-5); }
  .ask p { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); justify-content: flex-end; }
</style>
