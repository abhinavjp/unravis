<script lang="ts">
  import { onMount } from 'svelte';
  import { cachedEntryState, discoverClient, type DiscoverClient, type EntryState } from '../lib/discover';
  import { DISCOVER_TEXT } from '../lib/copy/discoverCopy';

  // The entry to the "This computer" panel. The server says whether scanning can work here: on Windows it is a button; elsewhere it is one plain line
  // ("Windows only"); a server build without the routes says so; any other failure shows nothing, so a flaky answer never blocks the screen.
  let { onOpen, primary = false, help = false, client = discoverClient }: { onOpen: () => void; primary?: boolean; help?: boolean; client?: DiscoverClient } = $props();

  let entry = $state.raw<EntryState>({ kind: 'loading' });

  onMount(() => {
    let alive = true;
    void cachedEntryState(client).then((state) => { if (alive) entry = state; });
    return () => { alive = false; };
  });
</script>

{#if entry.kind === 'ready'}
  <button type="button" class={primary ? 'primary-button' : 'secondary-button'} onclick={onOpen}>{DISCOVER_TEXT.entryButton}</button>
  {#if help}<p class="entry-note">{DISCOVER_TEXT.entryHelp}</p>{/if}
{:else if entry.kind === 'unsupported'}
  <p class="entry-note">{DISCOVER_TEXT.windowsOnly}</p>
{:else if entry.kind === 'unavailable'}
  <p class="entry-note">{DISCOVER_TEXT.unavailable}</p>
{/if}

<style>
  .entry-note { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; }
</style>
