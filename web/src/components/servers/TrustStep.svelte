<script lang="ts">
  import StepFooter from './StepFooter.svelte';
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
    <StepFooter back={() => { flow.changed = null; flow.reset(); }} backLabel={T.trustChangedKeep} primary={{ label: T.trustReplace, onclick: () => flow.replaceKey(), disabled: !replaceCompared || !!flow.busy, busyLabel: flow.busy === "replace" ? T.working : undefined }} />
  {:else}
    <h3 id="sv-trust-title">{T.trustTitle}</h3>
    <p class="sv-lead">{T.trustLead}</p>
    {#if flow.busy === 'keys'}
      <p class="sv-note" role="status">{T.trustFetching}</p>
    {:else if flow.hostKeys.length === 0}
      <div class="sv-actions"><button type="button" class="secondary-button" disabled={!!flow.busy} onclick={() => flow.fetchKeys()}>{T.trustAgain}</button></div>
    {:else}
      <div class="sv-panel">
        <header><h4>{server ? server.host + (server.port !== 22 ? ':' + server.port : '') : ''}</h4><span class="sv-sp"></span><span class="sv-sub">{T.trustKeyType}: {flow.hostKeys.find((k) => k.fingerprint === fingerprint)?.algorithm ?? flow.hostKeys[0].algorithm}</span></header>
        <div class="sv-in sv-stack">
          {#if flow.hostKeys.length > 1}
            <fieldset class="sv-field sv-radios" aria-describedby="sv-key-match"><legend>{T.trustPick}</legend>
              <p class="sv-note" id="sv-key-match">{T.trustOneMatch}</p>
              {#each flow.hostKeys as k (k.fingerprint)}
                <label class:sv-selected={fingerprint === k.fingerprint}>
                  <input type="radio" name="sv-fp" value={k.fingerprint} checked={fingerprint === k.fingerprint} onchange={() => { chosen = k.fingerprint; compared = false; }} />
                  <span class="sv-note">{k.algorithm}</span>
                  <code>{k.fingerprint}</code>
                </label>
              {/each}
            </fieldset>
          {:else}
            <div class="sv-sub">{T.trustFingerprint}</div>
            <div class="sv-print"><code>{fingerprint}</code></div>
          {/if}
          <label class="sv-check"><input type="checkbox" bind:checked={compared} /> {T.trustCompared}</label>
        </div>
      </div>
      <p class="sv-note">{T.trustLater} <button type="button" class="quiet-button" disabled={!!flow.busy} onclick={() => { compared = false; chosen = ''; flow.fetchKeys(); }}>{T.trustAgain}</button></p>
    {/if}
    {#if commandInput}
      <div class="sv-panel">
        <header><h4>{T.trustHelp}</h4><span class="sv-sp"></span><span class="sv-sub">{T.trustHelpSub}</span></header>
        <div class="sv-in sv-stack">
          <CommandBlock label={T.trustCommands} text={accessCommands('key', commandInput)} />
          <CommandBlock label={T.trustAdmin} text={accessCommands('admin-key', commandInput)} />
          <p class="sv-note">{T.trustGood}</p>
        </div>
      </div>
    {/if}    <StepFooter back={() => flow.go("connect")} primary={{ label: T.trustGo, onclick: () => flow.trust(fingerprint), disabled: !compared || !fingerprint || !!flow.busy, busyLabel: flow.busy === "pin" ? T.working : undefined }} reason={T.trustReason} />
  {/if}
</section>

<style>
  .sv-prints { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 260px), 1fr)); gap: var(--space-2); margin: 0; font-size: 13px; }
  .sv-prints div { display: grid; gap: 2px; }
  .sv-prints dt { color: var(--text-2); }
  .sv-prints dd { margin: 0; padding:12px; border:1px solid var(--line); background:var(--surface); overflow-wrap: anywhere; }
  .sv-prints code { font-family: var(--mono); font-size: 12px; }
  .sv-warn { color: var(--text); }
  .sv-stack { display: grid; gap: var(--space-2); }
  .sv-print { padding: 12px; border: 1px solid var(--line); background: var(--surface-2, var(--surface)); overflow-wrap: anywhere; }
  .sv-print code { font-family: var(--mono); font-size: 13px; }
  .sv-field.sv-radios { gap: var(--space-2); }
  .sv-radios label { display: grid; grid-template-columns: auto minmax(0, 1fr); gap: 2px var(--space-2); align-items: start; padding: var(--space-2) var(--space-3); border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface); cursor: pointer; }
  .sv-radios label.sv-selected { border-color: var(--accent); background: var(--surface-2); }
  .sv-radios input { grid-row: span 2; margin: 2px 0 0; accent-color: var(--accent); }
  .sv-radios code { grid-column: 2; font-family: var(--mono); font-size: 13px; overflow-wrap: anywhere; }
</style>



