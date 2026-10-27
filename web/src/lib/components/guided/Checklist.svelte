<script lang="ts">
  import { GUIDED_TEXT as T } from '../../copy/guidedCopy';
  import type { GuidedStore } from '../../guided/guided.svelte';
  import { counters, groupItems, LISTS, suggestedChecks } from '../../guided/stepModel';
  import type { ChecklistItem, ChecklistList } from '../../guided/types';

  // The checklist: four lists, a counter of what is done and what is open. Notes here are the person's own words and are kept with the case.
  let { store, readOnly }: { store: GuidedStore; readOnly: boolean } = $props();

  const grouped = $derived(groupItems(store.items));
  const count = $derived(counters(store.items));
  const suggested = $derived(store.result ? suggestedChecks(store.result, store.items) : []);

  let adding = $state(false);
  let text = $state('');
  let list = $state<ChecklistList>('todo');
  let note = $state('');

  async function add() {
    const ok = await store.addItem(list, text);
    if (ok) { text = ''; adding = false; note = ''; }
  }
  async function addAllSuggested() {
    note = '';
    if (suggested.length === 0) { note = T.noSuggested; return; }
    await store.addSuggested();
  }
  const move = (item: ChecklistItem, e: Event) => { void store.moveItem(item, (e.currentTarget as HTMLSelectElement).value as ChecklistList); };
</script>

<section class="checklist" aria-labelledby="checklist-title">
  <header>
    <h3 id="checklist-title">{T.checklistHeading}</h3>
    <span class="count" aria-live="polite">{T.done} {count.done} · {T.open} {count.open}</span>
  </header>
  <p class="lead">{T.checklistLead}</p>
  {#if readOnly}<p class="notice" role="status">{T.closedNote}</p>{/if}
  {#if store.planError}<p class="inline-error" role="alert">{store.planError}</p>{/if}
  {#if !store.planLoaded}<p class="loading-state" role="status">{T.running}</p>{/if}

  {#if !readOnly}
    <div class="actions">
      <button type="button" class="secondary-button" aria-expanded={adding} onclick={() => (adding = !adding)}>{T.addItem}</button>
      <button type="button" class="secondary-button" disabled={!store.result} onclick={() => void addAllSuggested()}>{T.addSuggested}</button>
    </div>
    {#if note}<p class="note" role="status">{note}</p>{/if}
    {#if adding}
      <form class="add" onsubmit={(e) => { e.preventDefault(); void add(); }}>
        <label>{T.itemText}<input type="text" maxlength="300" bind:value={text} required /></label>
        <label>{T.itemMove}
          <select bind:value={list}>{#each LISTS as l (l.id)}<option value={l.id}>{l.label}</option>{/each}</select>
        </label>
        <div class="row">
          <button type="submit" class="secondary-button" disabled={text.trim() === ''}>{T.itemSave}</button>
          <button type="button" class="quiet-button" onclick={() => { adding = false; text = ''; }}>{T.viewCancel}</button>
        </div>
      </form>
    {/if}
  {/if}

  <div class="lists">
    {#each LISTS as l (l.id)}
      <section class="list" aria-labelledby={'list-' + l.id}>
        <h4 id={'list-' + l.id}>{l.label} <span class="n">({grouped[l.id].length})</span></h4>
        {#if grouped[l.id].length === 0}
          <p class="note">{T.itemEmpty}</p>
        {:else}
          <ul>
            {#each grouped[l.id] as item (item.id)}
              <li>
                <span class="text">{item.text}</span>
                {#if item.suspect}<span class="tag">{T.itemSuspect(item.suspect)}</span>{/if}
                {#if !readOnly}
                  <span class="controls">
                    <label class="sr">{T.itemMove}
                      <select value={item.list} onchange={(e) => move(item, e)}>{#each LISTS as t (t.id)}<option value={t.id}>{t.label}</option>{/each}</select>
                    </label>
                    <button type="button" class="quiet-button" onclick={() => void store.removeItem(item)}>{T.itemDelete}</button>
                  </span>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
      </section>
    {/each}
  </div>
</section>

<style>
  .checklist { display: grid; gap: 12px; min-width: 0; padding: 16px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  header { display: flex; flex-wrap: wrap; align-items: baseline; justify-content: space-between; gap: 4px 12px; }
  h3 { margin: 0; font-size: 16px; }
  h4 { margin: 0; font-size: 14px; }
  .n { color: var(--text-2); font-weight: 400; }
  .count { padding: 1px 8px; font-size: 12px; font-weight: 600; background: var(--surface-2); border-radius: var(--radius-sm); }
  .lead, .note { margin: 0; font-size: 13px; }
  .note { color: var(--text-2); font-size: 12px; }
  .actions, .row { display: flex; flex-wrap: wrap; gap: 8px; }
  .add { display: grid; gap: 8px; padding: 12px; background: var(--surface-2); border-radius: var(--radius-sm); }
  .add label { display: grid; gap: 4px; font-size: 13px; }
  .lists { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 240px), 1fr)); gap: 12px; }
  .list { display: grid; align-content: start; gap: 8px; min-width: 0; padding: 12px; border: 1px solid var(--line-soft); border-radius: var(--radius-sm); }
  ul { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
  li { display: grid; gap: 4px; padding-bottom: 8px; border-bottom: 1px solid var(--line-soft); font-size: 13px; }
  li:last-child { border-bottom: 0; padding-bottom: 0; }
  .text { overflow-wrap: anywhere; }
  .tag { justify-self: start; padding: 0 8px; font-size: 12px; background: var(--surface-2); border-radius: var(--radius-sm); }
  .controls { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 8px; }
  .sr { display: block; }
</style>
