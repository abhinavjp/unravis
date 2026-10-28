<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { HTMLButtonAttributes } from 'svelte/elements';

  // The brand's three buttons. Outline means press me: primary (the one accent fill on a screen), secondary (outline) and quiet
  // (text only). A disabled button of any kind is a neutral control, never a faded accent.
  type Props = Omit<HTMLButtonAttributes, 'children'> & { kind?: 'primary' | 'secondary' | 'quiet'; children: Snippet };
  let { kind = 'secondary', type = 'button', children, ...rest }: Props = $props();
</script>

<button {type} class="u-button" class:primary={kind === 'primary'} class:secondary={kind === 'secondary'} class:quiet={kind === 'quiet'} {...rest}>{@render children()}</button>

<style>
  .u-button {
    border-radius: var(--radius-sm);
    padding: 6px 12px;
    font: inherit;
    font-size: 13px;
    font-weight: 600;
    line-height: 1.4;
    cursor: pointer;
    transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease), color var(--dur) var(--ease);
  }
  .primary { color: var(--accent-ink); background: var(--accent); border: 1px solid var(--accent); }
  .primary:hover:not(:disabled) { background: color-mix(in srgb, var(--accent) 88%, var(--text)); }
  .secondary { color: var(--text); background: transparent; border: 1px solid var(--line); }
  .secondary:hover:not(:disabled) { border-color: var(--text-2); background: var(--surface-2); }
  .quiet { color: var(--text-2); background: transparent; border: 1px solid transparent; }
  .quiet:hover:not(:disabled) { color: var(--text); background: var(--surface-2); }
  .u-button:disabled { color: var(--text-2); background: var(--surface-2); border-color: var(--control-line); cursor: not-allowed; }
  .u-button:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  @media (prefers-reduced-motion: reduce) { .u-button { transition: none; } }
</style>
