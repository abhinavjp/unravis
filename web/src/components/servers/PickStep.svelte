<script lang="ts">
  import StepFooter from './StepFooter.svelte';
  import type { ServersFlow } from '../../lib/serversFlow.svelte';
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';
  import { MAX_MACHINES, accessCommands, machineChoice, machineCounts } from '../../lib/servers';
  import CommandBlock from './CommandBlock.svelte';

  // Step 3. The machines the Salt master manages. Only an accepted machine known to be online can be ticked.
  // Unravis never accepts or removes a machine in Salt.
  let { flow }: { flow: ServersFlow } = $props();

  const eligiblePicks = $derived(flow.picks.filter((id) => flow.machines.some((m) => m.id === id && m.state === "accepted" && m.online === true)));
  $effect(() => { for (const id of [...flow.picks]) if (!eligiblePicks.includes(id)) flow.toggle(id, false); });
  const counts = $derived(machineCounts(flow.machines));
  const server = $derived(flow.server);
  const commandInput = $derived(server ? { os: server.os, host: server.host, port: server.port, user: server.user, machines: flow.picks, folders: server.folders, sudo: flow.sudo } : null);
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
  {:else if flow.machines.length === 0}
    <p class="sv-note">{T.pickEmpty}</p>
    <div class="sv-actions"><button type="button" class="secondary-button" disabled={!!flow.busy} onclick={() => flow.loadMachines()}>{T.retry}</button></div>
  {:else}
    <p class="sv-note">{T.pickCount(counts.accepted, counts.online, counts.offline, counts.waiting + counts.refused)}</p>
    <div class="sv-table-wrap"><table class="sv-table" aria-label={T.pickTitle}>
      <thead><tr><th></th><th>{T.machineColumn}</th><th>{T.osColumn}</th><th>{T.statusColumn}</th><th></th></tr></thead><tbody>
      {#each flow.machines as m (m.id)}
        {@const c = machineChoice(m)}
        <tr><td><input type="checkbox" aria-label={m.id} checked={eligiblePicks.includes(m.id)} disabled={!c.selectable || m.online !== true} onchange={(e) => flow.toggle(m.id, e.currentTarget.checked)} /></td>
          <td><strong>{m.id}</strong></td><td>—</td><td><span class="sv-chip sv-tone-{c.tone}">{m.state === "accepted" && m.online == null ? T.statusUnknown : c.label}</span></td><td class="sv-note">{m.state === "accepted" && m.online == null ? T.unknownNote : c.note}</td></tr>
      {/each}
    </tbody></table></div>
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
</style>




