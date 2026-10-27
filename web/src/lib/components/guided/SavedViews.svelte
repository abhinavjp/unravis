<script lang="ts">
  import { GUIDED_TEXT as T } from '../../copy/guidedCopy';
  import type { GuidedStore } from '../../guided/guided.svelte';
  import { MAX_VIEW_NAME, readViewSettings, toolById, viewWindows } from '../../guided/tools';
  import type { SavedView } from '../../guided/types';

  // Saved views: the windows, the API and the tool's own filters under a name. A view holds no result and no row of data.
  let { store, readOnly, tool = '', extra = {}, onOpen }: {
    store: GuidedStore; readOnly: boolean; tool?: string; extra?: { minCalls?: number; match?: string }; onOpen: (view: SavedView) => void;
  } = $props();

  let saving = $state(false);
  let name = $state('');

  async function save() {
    if (await store.saveCurrentView(name, tool, extra)) { name = ''; saving = false; }
  }
  const when = (iso: string) => (iso.length >= 16 ? iso.slice(0, 10) + ' ' + iso.slice(11, 16) : iso);
</script>

<section class="views" aria-labelledby="views-title">
  <header><h3 id="views-title">{T.savedViewsCount(store.views.length)}</h3></header>
  {#if readOnly}<p class="notice" role="status">{T.viewsClosed}</p>{/if}
  {#if store.viewError}<p class="inline-error" role="alert">{store.viewError}</p>{/if}

  {#if tool && !readOnly}
    <div>
      <button type="button" class="secondary-button" aria-expanded={saving} onclick={() => (saving = !saving)}>{T.saveView}</button>
    </div>
    {#if saving}
      <form class="save" onsubmit={(e) => { e.preventDefault(); void save(); }}>
        <label>{T.viewName}<input type="text" maxlength={MAX_VIEW_NAME} bind:value={name} required /></label>
        <p class="note">{T.viewNameNote}</p>
        <p class="note">{T.viewSaves}</p>
        <div class="row">
          <button type="submit" class="secondary-button">{T.viewSave}</button>
          <button type="button" class="quiet-button" onclick={() => { saving = false; name = ''; }}>{T.viewCancel}</button>
        </div>
      </form>
    {/if}
  {/if}

  {#if store.views.length === 0}
    <p class="note">{T.viewsEmpty}</p>
  {:else}
    <div class="table-scroll list">
      <table>
        <thead>
          <tr><th scope="col">{T.viewName}</th><th scope="col">{T.viewToolCol}</th><th scope="col">{T.viewWindowsCol}</th><th scope="col">{T.viewSavedCol}</th><th scope="col">{T.viewActionsCol}</th></tr>
        </thead>
        <tbody>
          {#each store.views as view (view.id)}
            <tr>
              <td>{view.name}</td>
              <td>{toolById(view.tool)?.title ?? view.tool}</td>
              <td>{viewWindows(readViewSettings(view.settings))}</td>
              <td>{when(view.saved)}</td>
              <td class="acts">
                <button type="button" class="quiet-button" onclick={() => onOpen(view)}>{T.viewOpen}</button>
                {#if !readOnly}<button type="button" class="quiet-button" onclick={() => void store.removeView(view)}>{T.viewDelete}</button>{/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</section>

<style>
  .views { display: grid; gap: 12px; min-width: 0; padding: 16px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  h3 { margin: 0; font-size: 16px; }
  .note { margin: 0; color: var(--text-2); font-size: 12px; }
  .save { display: grid; gap: 8px; padding: 12px; background: var(--surface-2); border-radius: var(--radius-sm); }
  .save label { display: grid; gap: 4px; font-size: 13px; }
  .row { display: flex; flex-wrap: wrap; gap: 8px; }
  table { white-space: normal; }
  .acts { white-space: nowrap; }
  /* Two hundred views must not push the rest of the page away. */
  .list { max-height: 420px; }
</style>
