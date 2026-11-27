<script lang="ts">
  import StepFooter from './StepFooter.svelte';
  import type { ServersFlow } from '../../lib/serversFlow.svelte';
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';
  import { MAX_MACHINES, accessCommands, machineChoice, machineCounts } from '../../lib/servers';
  import { PICK_FILTERS, clearShown, filterCounts, historyText, pickRows, selectShown, type PickFilter } from '../../lib/serversPick';
  import CommandBlock from './CommandBlock.svelte';

  // Step 3. The machines the Salt master manages. Only an accepted machine known to be online can be ticked.
  // Unravis never accepts or removes a machine in Salt. A long list can be searched and filtered, and machines used before come first.
  let { flow }: { flow: ServersFlow } = $props();

  let query = $state('');
  let filter = $state<PickFilter>('all');
  let leftOut = $state(0);

  const eligiblePicks = $derived(flow.picks.filter((id) => flow.machines.some((m) => m.id === id && m.state === "accepted" && m.online === true)));
  $effect(() => { for (const id of [...flow.picks]) if (!eligiblePicks.includes(id)) flow.toggle(id, false); });
  const counts = $derived(machineCounts(flow.machines));
  const server = $derived(flow.server);
  const rows = $derived(server ? pickRows(flow.machines, flow.memory, server.id, query, filter) : []);
  const filterTotals = $derived(server ? filterCounts(flow.machines, flow.memory, server.id) : { all: 0, online: 0, before: 0, blackbox: 0 });
  const filterLabel: Record<PickFilter, string> = { all: T.pickFilterAll, online: T.pickFilterOnline, before: T.pickFilterBefore, blackbox: T.pickFilterBlackbox };
  const now = $derived(rows.length >= 0 ? Date.now() : 0);
  const commandInput = $derived(server ? { os: server.os, host: server.host, port: server.port, user: server.user, machines: flow.picks, folders: server.folders, sudo: flow.sudo } : null);

  function chooseShown() {
    const next = selectShown(flow.picks, rows);
    leftOut = next.leftOut;
    for (const id of next.picks) if (!flow.picks.includes(id)) flow.toggle(id, true);
  }
  function clearAllShown() {
    leftOut = 0;
    const keep = clearShown(flow.picks, rows);
    for (const id of [...flow.picks]) if (!keep.includes(id)) flow.toggle(id, false);
  }
</script>

<section class="sv-block" aria-labelledby="sv-pick-title">
  <h3 id="sv-pick-title">{T.pickTitle}</h3>
  <p class="sv-lead">{T.pickLead}</p>

  {#if server?.os === 'linux'}
    <label class="sv-check">
      <input type="checkbox" checked={flow.sudo} disabled={!!flow.busy} aria-describedby="sv-sudo-help" onchange={(e) => {
        const on = e.currentTarget.checked;
        e.currentTarget.checked = flow.sudo;
        void flow.setSudo(on);
      }} />
      {T.pickSudo}
    </label>
    <p class="sv-note" id="sv-sudo-help">{T.pickSudoHelp}</p>
  {/if}
  {#if flow.note}<p class="sv-note" role="status">{flow.note}</p>{/if}

  {#if flow.busy === 'machines'}
    <p class="sv-note" role="status">{T.pickLoading}</p>
  {:else if flow.error && !flow.busy}
    <div class="sv-actions"><button type="button" class="secondary-button" onclick={() => flow.loadMachines()}>{T.retry}</button></div>
  {:else if flow.machines.length === 0}
    <p class="sv-note">{T.pickEmpty}</p>
    <div class="sv-actions"><button type="button" class="secondary-button" disabled={!!flow.busy} onclick={() => flow.loadMachines()}>{T.retry}</button></div>
  {:else}
    <p class="sv-note">{T.pickCount(counts.accepted, counts.online, counts.offline, counts.waiting + counts.refused)}</p>

    <div class="pick-tools">
      <label class="pick-search"><span class="sv-note">{T.pickSearch}</span>
        <input type="search" placeholder={T.pickSearchHint} spellcheck="false" autocomplete="off" bind:value={query} /></label>
      <div class="pick-filters" role="group" aria-label={T.pickFilters}>
        {#each PICK_FILTERS as f (f)}
          <button type="button" class="pick-filter" class:on={filter === f} aria-pressed={filter === f} onclick={() => { filter = f; leftOut = 0; }}>{filterLabel[f]} <span class="sv-note">{filterTotals[f]}</span></button>
        {/each}
      </div>
      <div class="pick-bulk">
        <button type="button" class="secondary-button sv-small" disabled={rows.length === 0 || !!flow.busy} onclick={chooseShown}>{T.pickAll}</button>
        <button type="button" class="secondary-button sv-small" disabled={eligiblePicks.length === 0 || !!flow.busy} onclick={clearAllShown}>{T.pickClear}</button>
      </div>
    </div>
    <p class="sv-note" role="status" aria-live="polite">{T.pickShown(rows.length, flow.machines.length, eligiblePicks.length)}{#if leftOut > 0} {T.pickLeftOut(leftOut)}{/if}</p>

    {#if rows.length === 0}
      <p class="sv-note">{T.pickNoMatch}</p>
    {:else}
      <div class="sv-table-wrap pick-wrap"><table class="sv-table" aria-label={T.pickTitle}>
        <thead><tr><th></th><th>{T.pickColMachine}</th><th>{T.pickColHistory}</th><th>{T.pickColNow}</th></tr></thead><tbody>
        {#each rows as r (r.machine.id)}
          {@const m = r.machine}
          {@const c = machineChoice(m)}
          {@const history = historyText(r.memory, now)}
          <tr><td><input type="checkbox" aria-label={m.id} checked={eligiblePicks.includes(m.id)} disabled={!c.selectable || m.online !== true} onchange={(e) => flow.toggle(m.id, e.currentTarget.checked)} /></td>
            <td><strong>{m.id}</strong></td>
            <td class="sv-note">{#if r.memory.blackbox}<span class="sv-chip sv-tone-ok">{T.pickBlackbox}</span> {/if}{history || T.pickNever}</td>
            <td><span class="sv-chip sv-tone-{c.tone}">{m.state === "accepted" && m.online == null ? T.statusUnknown : c.label}</span>
              {#if !c.selectable || (m.state === "accepted" && m.online == null)}<span class="sv-note"> {m.state === "accepted" && m.online == null ? T.unknownNote : c.note}</span>{/if}</td></tr>
        {/each}
      </tbody></table></div>
    {/if}
    {#if flow.picks.length >= MAX_MACHINES}<p class="sv-note">{T.pickLimit}</p>{/if}
    {#if flow.picks.length === 0}<p class="sv-note">{T.pickNone}</p>{/if}

    {#if commandInput}
      <details class="sv-help"><summary>{T.pickHelp}</summary>
        <CommandBlock label={T.pickCommands} text={accessCommands('salt', commandInput)} />
        <CommandBlock label={T.pickAdmin} text={accessCommands('admin-salt', commandInput)} />
      </details>
    {/if}
  {/if}
  <StepFooter back={() => flow.go("trust")} summary={T.chosen(eligiblePicks.length)} primary={{ label: T.connectMachines(eligiblePicks.length), onclick: () => { for (const id of [...flow.picks]) if (!eligiblePicks.includes(id)) flow.toggle(id, false); flow.confirmMachines(); }, disabled: eligiblePicks.length === 0 || !!flow.busy, busyLabel: flow.busy === "save-machines" ? T.working : undefined }} reason={T.pickNone} />
</section>

<style>
  .sv-help { display: grid; gap: var(--space-2); }
  .pick-tools { display: flex; flex-wrap: wrap; gap: var(--space-3); align-items: flex-end; }
  .pick-search { display: grid; gap: 2px; flex: 1 1 220px; min-width: 0; }
  .pick-search input { padding: 6px 8px; font: inherit; color: var(--text); background: var(--surface); border: 1px solid var(--control-line); border-radius: var(--radius-sm); box-sizing: border-box; width: 100%; }
  .pick-filters, .pick-bulk { display: flex; flex-wrap: wrap; gap: var(--space-1); }
  .pick-filter { padding: 4px 10px; font: inherit; font-size: var(--fs-3); color: var(--text); background: var(--surface); border: 1px solid var(--control-line); border-radius: var(--radius-sm); cursor: pointer; }
  .pick-filter.on { border-color: var(--text); font-weight: 600; }
  .sv-small { padding: 4px 10px; }
  .pick-wrap { overflow-y: hidden; }
</style>
