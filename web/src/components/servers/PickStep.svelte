<script lang="ts">
  import type { ServersFlow } from '../../lib/serversFlow.svelte';
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';
  import { MAX_MACHINES, accessCommands, machineChoice, machineCounts } from '../../lib/servers';
  import CommandBlock from './CommandBlock.svelte';

  // Step 3. The machines the Salt master manages. Only an accepted machine that is not known to be off can be ticked.
  // Unravis never accepts or removes a machine in Salt.
  let { flow }: { flow: ServersFlow } = $props();

  const counts = $derived(machineCounts(flow.machines));
  const server = $derived(flow.server);
  const commandInput = $derived(server ? { os: server.os, host: server.host, port: server.port, user: server.user, machines: flow.picks, folders: server.folders, sudo: flow.sudo } : null);
</script>

<section class="sv-block" aria-labelledby="sv-pick-title">
  <h3 id="sv-pick-title">{T.pickTitle}</h3>
  <p class="sv-lead">{T.pickLead}</p>

  {#if flow.busy === 'machines'}
    <p class="sv-note" role="status">{T.pickLoading}</p>
  {:else if flow.machines.length === 0}
    <p class="sv-note">{T.pickEmpty}</p>
    <div class="sv-actions"><button type="button" class="secondary-button" disabled={!!flow.busy} onclick={() => flow.loadMachines()}>{T.retry}</button></div>
  {:else}
    <p class="sv-note">{T.pickCount(counts.accepted, counts.online, counts.offline, counts.waiting + counts.refused)}</p>
    <ul class="sv-list" aria-label={T.pickTitle}>
      {#each flow.machines as m (m.id)}
        {@const c = machineChoice(m)}
        <li class="sv-row sv-machine">
          <label class="sv-check">
            <input type="checkbox" checked={flow.picks.includes(m.id)} disabled={!c.selectable && !flow.picks.includes(m.id)} onchange={(e) => flow.toggle(m.id, e.currentTarget.checked)} />
            <strong>{m.id}</strong>
          </label>
          <span class="sv-chip sv-tone-{c.tone}">{c.label}</span>
          <span class="sv-note">{c.note}</span>
        </li>
      {/each}
    </ul>
    {#if flow.picks.length >= MAX_MACHINES}<p class="sv-note">{T.pickLimit}</p>{/if}
    {#if server?.os === 'linux'}
      <label class="sv-check"><input type="checkbox" bind:checked={flow.sudo} /> {T.pickSudo}</label>
      <p class="sv-note">{T.pickSudoHelp}</p>
    {/if}
    {#if flow.picks.length === 0}<p class="sv-note">{T.pickNone}</p>{/if}
    <div class="sv-actions">
      <button type="button" class="primary-button" disabled={flow.picks.length === 0 || !!flow.busy} onclick={() => flow.confirmMachines()}>{flow.busy === 'save-machines' ? T.working : T.pickGo}</button>
    </div>
    {#if commandInput}
      <div class="sv-help">
        <CommandBlock label={T.pickCommands} text={accessCommands('salt', commandInput)} />
        <CommandBlock label={T.pickAdmin} text={accessCommands('admin-salt', commandInput)} />
      </div>
    {/if}
  {/if}
</section>

<style>
  .sv-list { display: grid; margin: 0; padding: 0; list-style: none; }
  .sv-machine { grid-template-columns: minmax(160px, 1fr) auto; align-items: center; gap: var(--space-2) var(--space-3); }
  .sv-machine .sv-note { grid-column: 1 / -1; }
  .sv-help { display: grid; gap: var(--space-2); }
</style>
