<script lang="ts">
  import type { ServersFlow } from '../../lib/serversFlow.svelte';
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';
  import { accessCommands } from '../../lib/servers';
  import CommandBlock from './CommandBlock.svelte';

  // Step 2. Shows the fingerprints the server gave, asks for an explicit compare tick, and pins one. A changed key is shown next to the
  // trusted one and can only be replaced with a second tick. Unravis never signs in while the key differs.
  let { flow }: { flow: ServersFlow } = $props();

  let compared = $state(false);
  let chosen = $state('');
  let replaceCompared = $state(false);

  const server = $derived(flow.server);
  const fingerprint = $derived(chosen || flow.hostKeys[0]?.fingerprint || '');
  const commandInput = $derived(server ? { os: server.os, host: server.host, port: server.port, user: server.user, machines: server.minionIds ?? [], folders: server.folders, sudo: !!server.saltSudo } : null);
</script>

<section class="sv-block" aria-labelledby="sv-trust-title">
  {#if flow.changed}
    <h3 id="sv-trust-title">{T.trustChangedTitle}</h3>
    <p class="sv-lead sv-warn">{T.trustChangedLead}</p>
    {#if flow.changed.next}<p class="sv-lead">{flow.changed.next}</p>{/if}
    <dl class="sv-prints">
      <div><dt>{T.trustPinned}</dt><dd><code>{flow.changed.pinned}</code></dd></div>
      <div><dt>{T.trustPresented}</dt><dd><code>{flow.changed.presented}</code></dd></div>
    </dl>
    <label class="sv-check"><input type="checkbox" bind:checked={replaceCompared} /> {T.trustReplaceCompared}</label>
    <div class="sv-actions">
      <button type="button" class="primary-button" disabled={!replaceCompared || !!flow.busy} onclick={() => flow.replaceKey()}>{flow.busy === 'replace' ? T.working : T.trustReplace}</button>
      <button type="button" class="quiet-button" onclick={() => { flow.changed = null; flow.reset(); }}>{T.trustChangedKeep}</button>
    </div>
  {:else}
    <h3 id="sv-trust-title">{T.trustTitle}</h3>
    <p class="sv-lead">{T.trustLead}</p>
    {#if flow.busy === 'keys'}
      <p class="sv-note" role="status">{T.trustFetching}</p>
    {:else if flow.hostKeys.length === 0}
      <div class="sv-actions"><button type="button" class="secondary-button" disabled={!!flow.busy} onclick={() => flow.fetchKeys()}>{T.trustAgain}</button></div>
    {:else}
      {#if flow.hostKeys.length > 1}
        <fieldset class="sv-field sv-radios"><legend>{T.trustPick}</legend>
          {#each flow.hostKeys as k (k.fingerprint)}
            <label><input type="radio" name="sv-fp" value={k.fingerprint} checked={fingerprint === k.fingerprint} onchange={() => { chosen = k.fingerprint; compared = false; }} /> <code>{k.fingerprint}</code> <span class="sv-note">{k.algorithm}</span></label>
          {/each}
        </fieldset>
      {:else}
        <dl class="sv-prints"><div><dt>{T.trustFingerprint}</dt><dd><code>{fingerprint}</code> <span class="sv-note">{flow.hostKeys[0].algorithm}</span></dd></div></dl>
      {/if}
      <label class="sv-check"><input type="checkbox" bind:checked={compared} /> {T.trustCompared}</label>
      <div class="sv-actions">
        <button type="button" class="primary-button" disabled={!compared || !fingerprint || !!flow.busy} onclick={() => flow.trust(fingerprint)}>{flow.busy === 'pin' ? T.working : T.trustGo}</button>
        <button type="button" class="quiet-button" disabled={!!flow.busy} onclick={() => { compared = false; chosen = ''; flow.fetchKeys(); }}>{T.trustAgain}</button>
      </div>
    {/if}
    {#if commandInput}
      <div class="sv-help">
        <CommandBlock label={T.trustCommands} text={accessCommands('key', commandInput)} />
        <CommandBlock label={T.trustAdmin} text={accessCommands('admin-key', commandInput)} />
      </div>
    {/if}
  {/if}
</section>

<style>
  .sv-prints { display: grid; gap: var(--space-2); margin: 0; font-size: 13px; }
  .sv-prints div { display: grid; gap: 2px; }
  .sv-prints dt { color: var(--text-2); }
  .sv-prints dd { margin: 0; overflow-wrap: anywhere; }
  .sv-prints code { font-family: var(--mono); font-size: 12px; }
  .sv-warn { color: var(--text); border-left: 3px solid var(--warn); padding-left: var(--space-3); }
  .sv-help { display: grid; gap: var(--space-2); }
</style>
