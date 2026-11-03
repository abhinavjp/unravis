<script lang="ts">
  import { onMount, untrack, type Component } from 'svelte';

  // Renders a component whose code is fetched on first use (see lib/lazy.ts). Until it arrives nothing is drawn, so there is no placeholder to jump.
  // The props of the lazy component are passed on as they are (spread, so they stay live).
  let { load, props, ready }: { load: () => Promise<Component<any>>; props: Record<string, unknown>; ready: Component<any> | null } = $props();
  let Loaded = $state.raw<Component<any> | null>(untrack(() => ready));
  let failed = $state(false);
  let busy = $state(false);

  function attempt() {
    busy = true;
    failed = false;
    load().then(component => { Loaded = component; }, () => { failed = true; }).finally(() => { busy = false; });
  }
  // Closing hands the opener's own close back to the page, so a panel that never arrived does not leave the page locked.
  function dismiss() {
    failed = false;
    const close = props.onClose;
    if (typeof close === 'function') close();
  }
  onMount(() => { if (!Loaded) attempt(); });
</script>

{#if Loaded}<Loaded {...props} />{:else if failed}
  <div class="lazy-error" role="alert">
    <span>This part of the page could not be loaded. Check the connection, or reload the page if Unravis was updated.</span>
    <button type="button" class="secondary-button" disabled={busy} onclick={attempt}>Try again</button>
    <button type="button" class="quiet-button" onclick={dismiss}>Close</button>
  </div>
{/if}

<style>
  .lazy-error { position: fixed; left: 50%; bottom: 16px; z-index: 60; transform: translateX(-50%); display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; width: min(560px, calc(100vw - 32px)); padding: 10px 14px; color: var(--text); font-size: 13px; background: var(--surface); border: 1px solid var(--bad); border-radius: var(--radius-sm); box-shadow: 0 12px 32px color-mix(in srgb, var(--bg) 70%, transparent); }
  .lazy-error span { flex: 1 1 260px; }
</style>
