<script lang="ts">
  import { tick } from 'svelte';
  import type { Source } from '../lib/types';

  // The source selector. It lives behind a header button so the audit sources are one tap away without a permanent sidebar.
  type Group = { id: string; label: string; sources: Source[] };
  let { groups, selectedIds, total, demo, busyDelete, sourceError, sourceSuccess, savedCount, onToggle, onDelete, onManage, onAdd }: {
    groups: Group[]; selectedIds: string[]; total: number; demo: boolean; busyDelete: string; sourceError: string; sourceSuccess: string; savedCount: number;
    onToggle: (id: string, checked: boolean) => void; onDelete: (source: Source) => void; onManage: () => void; onAdd: () => void;
  } = $props();
  let open = $state(false);
  let wrap = $state<HTMLDivElement>();
  let trigger = $state<HTMLButtonElement>();
  const summary = $derived(total === 0 ? 'No sources' : selectedIds.length === 0 ? 'Choose sources' : selectedIds.length === 1 ? (groups.flatMap(group => group.sources).find(source => source.id === selectedIds[0])?.database ?? '1 source') : selectedIds.length + ' sources');

  async function toggle() {
    open = !open;
    if (open) { await tick(); wrap?.querySelector<HTMLElement>('.popover input, .popover button')?.focus(); }
  }
  function close(returnFocus: boolean) { open = false; if (returnFocus) trigger?.focus(); }
  function onKey(event: KeyboardEvent) {
    if (event.key === 'Escape' && open) { event.preventDefault(); event.stopPropagation(); close(true); }
  }
  $effect(() => {
    if (!open) return;
    const away = (event: PointerEvent) => { if (wrap && !wrap.contains(event.target as Node)) close(false); };
    window.addEventListener('pointerdown', away);
    return () => window.removeEventListener('pointerdown', away);
  });
</script>

<div class="popover-wrap" bind:this={wrap} onkeydown={onKey} role="presentation">
  <button type="button" class="header-button" bind:this={trigger} aria-haspopup="dialog" aria-expanded={open} aria-label={'Audit sources: ' + summary} onclick={toggle}>
    <span class="dot" class:none={!selectedIds.length} aria-hidden="true"></span><span class="label">{summary}</span>
  </button>
  {#if open}
    <div class="popover" role="dialog" aria-label="Audit sources">
      <h2>Sources</h2>
      <p class="source-count">{selectedIds.length} selected · {total} available</p>
      {#if sourceError}<div class="inline-error" role="alert">{sourceError}</div>{/if}
      {#if sourceSuccess}<div class="connection-status" role="status">{sourceSuccess}</div>{/if}
      <div class="source-list">
        {#each groups as group (group.id)}
          <section class="source-group" aria-label={group.label}>
            <div class="source-group-heading"><strong>{group.label}</strong><small>{group.sources.length} audit source{group.sources.length === 1 ? '' : 's'}</small></div>
            {#each group.sources as source (source.id)}
              <div class="source-item">
                <label class="source-choice"><input type="checkbox" checked={selectedIds.includes(source.id)} onchange={(event) => onToggle(source.id, event.currentTarget.checked)} /><span><strong>{source.database}</strong><small>{source.collection}</small></span></label>
                {#if !demo}<button type="button" class="icon-button" aria-label={'Remove ' + source.label} title="Remove source" disabled={busyDelete === source.id} onclick={() => onDelete(source)}>×</button>{/if}
              </div>
            {/each}
          </section>
        {:else}
          <div class="source-empty"><strong>{demo ? 'No sample sources' : 'No connections yet'}</strong><p>{demo ? 'This demo has no audit sources configured.' : 'Add a connection or load saved settings, then choose audit databases.'}</p></div>
        {/each}
      </div>
      <div class="popover-foot">
        {#if demo}<p class="demo-note">Demo sources are provided by the sample repository.</p>
        {:else}
          <button type="button" class="quiet-button" onclick={() => { close(false); onAdd(); }}>Add a connection</button>
          <button type="button" class="quiet-button" onclick={() => { close(false); onManage(); }}>Manage connections{savedCount ? ' (' + savedCount + ' saved)' : ''}</button>
        {/if}
      </div>
    </div>
  {/if}
</div>
