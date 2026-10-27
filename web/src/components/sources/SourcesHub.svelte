<script lang="ts">
  import AddAnotherDay from './AddAnotherDay.svelte';
  import AddDataPanel, { type AddChoice } from './AddDataPanel.svelte';
  import ConnectionsPage from './ConnectionsPage.svelte';
  import MongoStepPanel from './MongoStepPanel.svelte';
  import SourceCombinations from './SourceCombinations.svelte';
  import SqlStepPanel from './SqlStepPanel.svelte';
  import { HUB_TEXT } from '../../lib/copy/sourcesCopy';
  import './sources.css';

  // One place for the SQL Server and MongoDB sources of the open case: saved connections, Step 7, Step 8, Add data, Add another day and the
  // combinations table. The guided shell mounts SqlStepPanel and MongoStepPanel directly; this hub keeps the lanes reachable from the case page.
  let { caseId, onGone }: { caseId: string; onGone?: () => void } = $props();
  type Tab = 'connections' | 'sql' | 'mongo' | 'add' | 'day' | 'combos';
  let tab = $state<Tab>('connections');
  const tabs: { id: Tab; label: string }[] = [
    { id: 'connections', label: HUB_TEXT.connections }, { id: 'sql', label: HUB_TEXT.sql }, { id: 'mongo', label: HUB_TEXT.mongo },
    { id: 'add', label: HUB_TEXT.add }, { id: 'day', label: HUB_TEXT.day }, { id: 'combos', label: HUB_TEXT.combos }
  ];
  function choose(what: AddChoice) {
    if (what === 'sql') tab = 'sql';
    else if (what === 'mongo') tab = 'mongo';
    else if (what === 'day') tab = 'day';
    else tab = 'add';
  }
</script>

<div class="src" data-sources-hub>
  <div class="tabs" role="tablist" aria-label={HUB_TEXT.label}>
    {#each tabs as item (item.id)}
      <button type="button" role="tab" class="btn" id="hub-tab-{item.id}" aria-selected={tab === item.id} aria-controls="hub-panel" onclick={() => { tab = item.id; }}>{item.label}</button>
    {/each}
  </div>
  <div id="hub-panel" role="tabpanel" aria-labelledby="hub-tab-{tab}">
    {#if tab === 'connections'}
      <ConnectionsPage onMongoAdd={() => { tab = 'mongo'; }} />
    {:else if tab === 'sql'}
      <SqlStepPanel {caseId} {onGone} onAddData={() => { tab = 'add'; }} />
    {:else if tab === 'mongo'}
      <MongoStepPanel {caseId} {onGone} onAddData={() => { tab = 'add'; }} />
    {:else if tab === 'add'}
      <AddDataPanel {caseId} onChoose={choose} />
    {:else if tab === 'day'}
      <AddAnotherDay {caseId} {onGone} onCancel={() => { tab = 'add'; }} />
    {:else}
      <SourceCombinations {caseId} />
    {/if}
  </div>
</div>
