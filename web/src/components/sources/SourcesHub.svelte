<script lang="ts">
  import { createView } from '../../lib/wizardCreate.svelte';
  import AddAnotherDay from './AddAnotherDay.svelte';
  import AddSourcesFlow from '../sourcewiz/AddSourcesFlow.svelte';
  import ConnectionsPage from './ConnectionsPage.svelte';
  import MongoStepPanel from './MongoStepPanel.svelte';
  import SourceCombinations from './SourceCombinations.svelte';
  import SourcesList from './SourcesList.svelte';
  import SqlStepPanel from './SqlStepPanel.svelte';
  import { HUB_TEXT } from '../../lib/copy/sourcesCopy';
  import { SOURCES_LIST_TEXT } from '../../lib/copy/sourcesListCopy';
  import { sourcesNav } from '../../lib/sourcesNav.svelte';
  import { untrack } from 'svelte';
  import { tabAfterKey } from '../../lib/lanes';
  import './sources.css';

  // One place for the SQL Server and MongoDB sources of the open case: saved connections, Step 7, Step 8, Add data, Add another day and the
  // combinations table. The guided shell mounts SqlStepPanel and MongoStepPanel directly; this hub keeps the lanes reachable from the case page.
  // onAddFiles and onAddBlackbox open the pages that add those sources. Files is offered only when its page is handed in; the Blackbox card is always on.
  // onAddMongo opens the window where a MongoDB connection is saved; without it the + MongoDB button is off.
  let { caseId, readOnly = false, onGone, onAddFiles, onAddMongo }: { caseId: string; readOnly?: boolean; onGone?: () => void; onAddFiles?: () => void; onAddMongo?: () => void } = $props();
  type Tab = 'list' | 'connections' | 'sql' | 'mongo' | 'add' | 'day' | 'combos';
  let tab = $state<Tab>('connections');
  // The Investigation timeline asks for the Sources tab from its time-zone notice (packet P11).
  $effect(() => { if (sourcesNav.wantList) { sourcesNav.wantList = false; tab = 'list'; } });
  // The import drawer's Add data cards (screen 112) ask for the SQL tab, Add another day, or the page that adds a Mongo audit database.
  $effect(() => {
    const want = sourcesNav.wantTab;
    if (!want) return;
    sourcesNav.wantTab = '';
    untrack(() => { tab = want === 'sql' ? 'sql' : want === 'day' ? 'day' : 'add'; });
  });
  const tabs: { id: Tab; label: string }[] = [
    { id: 'connections', label: HUB_TEXT.connections }, { id: 'list', label: SOURCES_LIST_TEXT.tab }, { id: 'sql', label: HUB_TEXT.sql }, { id: 'mongo', label: HUB_TEXT.mongo },
    { id: 'add', label: HUB_TEXT.add }, { id: 'day', label: HUB_TEXT.day }, { id: 'combos', label: HUB_TEXT.combos }
  ];
  // One tab stop for the row (the selected tab); the arrow keys, Home and End move between tabs and open the one they land on.
  function roving(event: KeyboardEvent) {
    const to = tabAfterKey(event.key, tabs.findIndex(item => item.id === tab), tabs.length);
    if (to < 0) return;
    event.preventDefault();
    tab = tabs[to].id;
    document.getElementById('hub-tab-' + tab)?.focus();
  }
</script>

<div class="src" data-sources-hub>
  <div class="tabs" role="tablist" aria-label={HUB_TEXT.label}>
    {#each tabs as item (item.id)}
      <button type="button" role="tab" class="btn" id="hub-tab-{item.id}" aria-selected={tab === item.id} disabled={createView.running} tabindex={tab === item.id ? 0 : -1} aria-controls="hub-panel" onkeydown={roving} onclick={() => { tab = item.id; }}>{item.label}</button>
    {/each}
  </div>
  <div id="hub-panel" role="tabpanel" aria-labelledby="hub-tab-{tab}">
    {#if tab === 'list'}
      <SourcesList {caseId} {readOnly} {onAddFiles} />
    {:else if tab === 'connections'}
      <ConnectionsPage {readOnly} onMongoAdd={onAddMongo} />
    {:else if tab === 'sql'}
      <SqlStepPanel {caseId} {readOnly} {onGone} onAddData={() => { tab = 'add'; }} />
    {:else if tab === 'mongo'}
      <MongoStepPanel {caseId} {readOnly} {onGone} onAddData={() => { tab = 'add'; }} />
    {:else if tab === 'add'}
      <AddSourcesFlow {caseId} {readOnly} onFiles={onAddFiles} onDone={() => { tab = 'list'; }} />
    {:else if tab === 'day'}
      <AddAnotherDay {caseId} {readOnly} {onGone} onCancel={() => { tab = 'add'; }} onReadSql={() => { tab = 'sql'; }} />
    {:else}
      <SourceCombinations {caseId} />
    {/if}
  </div>
</div>
