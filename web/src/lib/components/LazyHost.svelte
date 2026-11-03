<script lang="ts">
  import { onMount, untrack, type Component } from 'svelte';

  // Renders a component whose code is fetched on first use (see lib/lazy.ts). Until it arrives nothing is drawn, so there is no placeholder to jump.
  // The props of the lazy component are passed on as they are (spread, so they stay live).
  let { load, props, ready }: { load: () => Promise<Component<any>>; props: Record<string, unknown>; ready: Component<any> | null } = $props();
  let Loaded = $state.raw<Component<any> | null>(untrack(() => ready));
  onMount(() => { if (!Loaded) void load().then(component => { Loaded = component; }, () => { /* a chunk that cannot load leaves the region empty, as a failed fetch of any asset would */ }); });
</script>

{#if Loaded}<Loaded {...props} />{/if}
