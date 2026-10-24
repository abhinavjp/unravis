<script lang="ts">
  import type { Snippet } from 'svelte';

  // D2: the evidence frame. A right drawer on wide screens and a full view on narrow ones (the layout is in app.css). Tabs are only the ones that exist.
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
</script>

<aside class="inspector" class:off aria-labelledby={headingId}>
  <div class="inspector-head">
    <div><h2 id={headingId} tabindex="-1">{title}</h2>{#if subtitle}<p>{subtitle}</p>{/if}</div>
    <button type="button" class="inspector-close" aria-label="Close and go back" title="Close (Esc)" onclick={onClose}>×</button>
  </div>
  {#if tabs.length}
    <div class="tabs" role="tablist" aria-label="Evidence" bind:this={list} tabindex="-1" onkeydown={onKey}>
      {#each tabs as tab (tab.id)}
        <button type="button" role="tab" class="tab" id={'tab-' + tab.id} aria-selected={active === tab.id} aria-controls={'tabpanel-' + tab.id} tabindex={active === tab.id ? 0 : -1} onclick={() => onTab?.(tab.id)}>{tab.label}</button>
      {/each}
    </div>
  {/if}
  <div class="inspector-body" role={tabs.length ? 'tabpanel' : undefined} id={tabs.length ? 'tabpanel-' + active : undefined} aria-labelledby={tabs.length ? 'tab-' + active : undefined}>
    {@render children()}
  </div>
</aside>
