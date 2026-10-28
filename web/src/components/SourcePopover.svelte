<script lang="ts">
  import { tick } from 'svelte';
  import { COVERAGE_TEXT } from '../lib/provenance';
  import { KEEPER_TEXT } from '../lib/copy/sourcesCopy';
  import { heldSourceText } from '../lib/keeper';
  import type { Source } from '../lib/types';
  import './sources/sources.css';

  // The source selector. It lives behind a header button so the audit sources are one tap away without a permanent sidebar.
  type Group = { id: string; label: string; sources: Source[] };
  let { groups, selectedIds, total, demo, busyDelete, sourceError, sourceSuccess, savedCount, onToggle, onDelete, onManage, onAdd, onCoverage }: {
    groups: Group[]; selectedIds: string[]; total: number; demo: boolean; busyDelete: string; sourceError: string; sourceSuccess: string; savedCount: number;
    onToggle: (id: string, checked: boolean) => void; onDelete: (source: Source) => void; onManage: () => void; onAdd: () => void;
    /** Opens the coverage drawer for one source. The check itself still starts only from the drawer's own button. */
    onCoverage: (source: { id: string; label: string }) => void;
  } = $props();
  let open = $state(false);
  let wrap = $state<HTMLDivElement>();
  let trigger = $state<HTMLButtonElement>();
  // Sources the key keeper opened from saved links (P14b) say so; when it is not running they say they cannot be read.
  const keeperStopped = $derived(groups.some(group => group.sources.some(source => source.keeper === 'stopped')));
  const summary = $derived(total === 0 ? 'No sources' : selectedIds.length === 0 ? 'Choose sources' : selectedIds.length === 1 ? (groups.flatMap(group => group.sources).find(source => source.id === selectedIds[0])?.database ?? '1 source') : selectedIds.length + ' sources');

  async function toggle() {
    open = !open;
    if (open) { await tick(); wrap?.querySelector<HTMLElement>('.popover input, .popover button')?.focus(); }
  }
  function close(returnFocus: boolean) { open = false; if (returnFocus) trigger?.focus(); }
  function onKey(event: KeyboardEvent) {
    if (event.key === 'Escape' && open) { event.preventDefault(); event.stopPropagation(); close(true); }
  }
  // Opens the coverage drawer from the menu: focus goes back to the trigger first, so the drawer hands it back there when it closes.
  function showCoverage(source: Source) { close(true); onCoverage({ id: source.id, label: source.connectionLabel ? source.connectionLabel + ' / ' + source.database : source.database }); }
  $effect(() => {
    if (!open) return;
    const away = (event: PointerEvent) => { if (wrap && !wrap.contains(event.target as Node)) close(false); };
    // Esc closes the menu wherever focus is (it can be on the page after a click on an inert area), and does not also step the page up a level.
    const escape = (event: KeyboardEvent) => { if (event.key === 'Escape' && !event.defaultPrevented) { event.preventDefault(); event.stopPropagation(); close(true); } };
    window.addEventListener('pointerdown', away);
    window.addEventListener('keydown', escape, true);
    return () => { window.removeEventListener('pointerdown', away); window.removeEventListener('keydown', escape, true); };
  });
</script>

<div class="popover-wrap" bind:this={wrap} onkeydown={onKey} role="presentation">
  <button type="button" class="header-button" bind:this={trigger} aria-haspopup="dialog" aria-expanded={open} aria-label={'Audit sources: ' + summary} title={summary} onclick={toggle}>
    <span class="dot" class:none={!selectedIds.length} aria-hidden="true"></span><span class="label">{summary}</span>
  </button>
  {#if open}
    <div class="popover" role="dialog" aria-label="Audit sources">
      <h2>Sources</h2>
      <p class="source-count">{selectedIds.length} selected · {total} available</p>
      {#if sourceError}<div class="inline-error" role="alert">{sourceError}</div>{/if}
      {#if sourceSuccess}<div class="connection-status" role="status">{sourceSuccess}</div>{/if}
      {#if keeperStopped}<p class="keeper-note" role="note" data-keeper-sources-stopped>{KEEPER_TEXT.sourcesStopped}</p>{/if}
      <div class="source-list">
        {#each groups as group (group.id)}
          <section class="source-group" aria-label={group.label}>
            <div class="source-group-heading"><strong>{group.label}</strong><small>{group.sources.length} audit source{group.sources.length === 1 ? '' : 's'}</small></div>
            {#each group.sources as source (source.id)}
              <div class="source-item">
                <label class="source-choice"><input type="checkbox" checked={selectedIds.includes(source.id)} onchange={(event) => onToggle(source.id, event.currentTarget.checked)} /><span><strong>{source.database}</strong><small>{source.collection}</small>{#if heldSourceText(source)}<span class="src-sticker held" class:down={source.keeper === 'stopped'} data-keeper-source={source.keeper}>{heldSourceText(source)}</span>{/if}</span></label>
                <button type="button" class="quiet-button coverage-toggle" aria-haspopup="dialog" aria-label={COVERAGE_TEXT.heading + ': ' + source.label} onclick={() => showCoverage(source)}>{COVERAGE_TEXT.heading} ›</button>
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

<style>
  .coverage-toggle { flex: 0 0 auto; white-space: nowrap; }
  .source-choice .src-sticker { display: inline-flex; justify-self: start; }
  .keeper-note { margin: 0 0 var(--space-2); color: var(--text-2); font-size: var(--fs-2); }
  /* A long source summary is cut with an ellipsis (the full text is in the aria-label) so the header row never overflows. */
  .header-button .label { max-width: 18ch; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  @media (max-width: 720px) { .header-button .label { max-width: 11ch; } }
</style>
