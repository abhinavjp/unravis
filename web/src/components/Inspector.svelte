<script lang="ts">
  import type { Snippet } from 'svelte';
  import Drawer from './Drawer.svelte';
  import HelpNote from './HelpNote.svelte';

  // D2: the evidence frame, built on the shared Drawer as a side panel that leaves the page beside it usable. Wide screens show it as a right drawer with the
  // ranked list still visible on the left; below 900px it becomes a full view (the layout is in app.css). Tabs are only the ones that exist.
  interface InspectorTab { id: string; label: string }
  let { title, subtitle = '', tabs = [], active = '', off = false, onTab, onClose, children, headingId = 'inspector-title' }: {
    title: string; subtitle?: string; tabs?: InspectorTab[]; active?: string; off?: boolean; onTab?: (id: string) => void; onClose: () => void; children: Snippet; headingId?: string;
  } = $props();
  let list = $state<HTMLDivElement>();
  function onKey(event: KeyboardEvent) {
    if (!['ArrowRight', 'ArrowLeft', 'Home', 'End'].includes(event.key) || !tabs.length) return;
    event.preventDefault();
    const current = Math.max(0, tabs.findIndex(tab => tab.id === active));
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (current + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length;
    onTab?.(tabs[next].id);
    queueMicrotask(() => list?.querySelectorAll<HTMLElement>('[role="tab"]')[next]?.focus());
  }
  // On a narrow screen the tab row scrolls sideways: keep the chosen tab in view so a cut-off label is never the one you are on.
  $effect(() => {
    if (!list || !active) return;
    const tab = list.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]');
    const box = list.getBoundingClientRect(), at = tab?.getBoundingClientRect();
    if (tab && at && (at.left < box.left || at.right > box.right)) list.scrollTo({ left: list.scrollLeft + (at.left - box.left) - 12, behavior: 'smooth' });
  });
</script>

{#snippet tabRow()}
  {#if tabs.length}
    <div class="tabs" role="tablist" aria-label="Evidence" bind:this={list} tabindex="-1" onkeydown={onKey}>
      {#each tabs as tab (tab.id)}
        <button type="button" role="tab" class="tab" id={'tab-' + tab.id} aria-selected={active === tab.id} aria-controls={'tabpanel-' + tab.id} tabindex={active === tab.id ? 0 : -1} onclick={() => onTab?.(tab.id)}>{tab.label}</button>
      {/each}
    </div>
  {/if}
{/snippet}

<Drawer modal={false} class={off ? 'inspector off' : 'inspector'} {title} titleId={headingId} focusTitle intro={subtitle} closeLabel="Close and go back" closeTitle="Close (Esc)" {onClose} headerExtra={tabs.length ? tabRow : undefined}>
  <div class="inspector-body" role={tabs.length ? 'tabpanel' : undefined} id={tabs.length ? 'tabpanel-' + active : undefined} aria-labelledby={tabs.length ? 'tab-' + active : undefined}>
    <HelpNote id="inspector" />
    {@render children()}
  </div>
</Drawer>

<style>
  /* A soft fade at the right edge hints that the tab row scrolls, only when the panel is the full-width mobile view. */
  @media (max-width: 899px) {
    .tabs { -webkit-mask-image: linear-gradient(to right, #000 calc(100% - 28px), transparent); mask-image: linear-gradient(to right, #000 calc(100% - 28px), transparent); }
  }
</style>
