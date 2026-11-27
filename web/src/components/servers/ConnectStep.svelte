<script lang="ts">
  import type { ServersFlow } from '../../lib/serversFlow.svelte';
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';
  import { connectProblems, emptyConnectForm, type AuthKind, type ServerEntry } from '../../lib/servers';

  // Step 1. The saved servers, then the form for a new one. A password or key is read from its field at the moment of sending,
  // sent once, and the field is emptied straight after, whether the call worked or not. It is never held in this page's state.
  let { flow }: { flow: ServersFlow } = $props();

  let form = $state(emptyConnectForm());
  let hasSecret = $state(false);
  let tried = $state(false);
  let passwordEl = $state<HTMLInputElement>();
  let passphraseEl = $state<HTMLInputElement>();
  let keyEl = $state<HTMLInputElement>();
  let forgetAsk = $state('');

  // The secret-only form for a saved server that has none.
  const secretAuth = $derived<AuthKind>(flow.server?.auth ?? 'password');
  const problems = $derived(connectProblems(form, hasSecret));

  function clearSecrets() {
    if (passwordEl) passwordEl.value = '';
    if (passphraseEl) passphraseEl.value = '';
    if (keyEl) keyEl.value = '';
    hasSecret = false;
  }

  function noteSecret() {
    hasSecret = form.auth === 'password' ? !!passwordEl?.value : !!keyEl?.files?.length;
  }

  async function readSecret(auth: AuthKind): Promise<string> {
    if (auth === 'password') return passwordEl?.value ?? '';
    const file = keyEl?.files?.[0];
    return file ? await file.text() : '';
  }

  async function submit(event: Event) {
    event.preventDefault();
    tried = true;
    if (problems.length) return;
    let secret = '', passphrase = '';
    try {
      secret = await readSecret(form.auth);
      passphrase = passphraseEl?.value ?? '';
      await flow.connect(form, secret, passphrase);
    } finally { secret = ''; passphrase = ''; clearSecrets(); }
  }

  async function submitSecret(event: Event) {
    event.preventDefault();
    let secret = '', passphrase = '';
    try {
      secret = await readSecret(secretAuth);
      passphrase = passphraseEl?.value ?? '';
      if (secret) await flow.saveSecret(secretAuth, secret, passphrase);
    } finally { secret = ''; passphrase = ''; clearSecrets(); }
  }

  const trustLabel = (s: ServerEntry) => (s.hostKey ? T.savedTrusted : T.savedNotTrusted);
</script>

<section class="sv-block" aria-labelledby="sv-connect-title">
  <h3 id="sv-connect-title">{flow.needSecret ? flow.server?.name : T.connectTitle}</h3>

  {#if flow.needSecret && flow.server}
    <p class="sv-lead">{T.secretNeeded}</p>
    <form class="sv-form" onsubmit={submitSecret} autocomplete="off">
      {#if secretAuth === 'password'}
        <label class="sv-field"><span>{T.fieldPassword}</span>
          <input type="password" bind:this={passwordEl} autocomplete="off" oninput={() => { hasSecret = !!passwordEl?.value; }} />
          <small>{T.fieldPasswordHelp}</small></label>
      {:else}
        <label class="sv-field"><span>{T.fieldKeyFile}</span>
          <input type="file" bind:this={keyEl} onchange={() => { hasSecret = !!keyEl?.files?.length; }} />
          <small>{T.fieldKeyFileHelp}</small></label>
        <label class="sv-field"><span>{T.fieldPassphrase}</span>
          <input type="password" bind:this={passphraseEl} autocomplete="off" />
          <small>{T.fieldPassphraseHelp}</small></label>
      {/if}
      <div class="sv-actions">
        <button type="submit" class="primary-button" disabled={!hasSecret || !!flow.busy}>{flow.busy ? T.working : T.secretSave}</button>
        <button type="button" class="quiet-button" onclick={() => flow.reset()}>{T.back}</button>
      </div>
    </form>
  {:else}
    <p class="sv-lead">{T.connectLead}</p>

    <div class="sv-saved">
      <h4>{T.savedHeading}</h4>
      {#if flow.list === null}
        <p class="sv-note" role="status">{T.savedLoading}</p>
      {:else if flow.listError}
        <p class="sv-bad" role="alert">{T.savedFailed} {flow.listError} <button type="button" class="quiet-button" onclick={() => flow.load()}>{T.retry}</button></p>
      {:else if flow.list.length === 0}
        <p class="sv-note">{T.savedEmpty}</p>
      {:else}
        <ul class="sv-list">
          {#each flow.list as s (s.id)}
            <li class="sv-row">
              <div class="sv-row-main">
                <strong>{s.name}</strong>
                <span class="sv-chip">{trustLabel(s)}</span>
                {#if s.running}<span class="sv-chip sv-chip-live">{T.savedRunning}</span>{/if}
                <span class="sv-note">{s.user}@{s.host}:{s.port}{#if (s.minionIds?.length ?? 0) > 0} . {T.savedMachines(s.minionIds?.length ?? 0)}{/if}</span>
                {#if !s.hasSecret}<span class="sv-note">{T.savedNoSecret}</span>{/if}
              </div>
              {#if forgetAsk === s.id}
                <div class="sv-confirm" role="group" aria-label={T.savedForget + ' ' + s.name}>
                  <p>{T.savedForgetAsk}</p>
                  <div class="sv-actions">
                    <button type="button" class="secondary-button" disabled={!!flow.busy} onclick={async () => { await flow.forget(s); forgetAsk = ''; }}>{T.savedForgetYes}</button>
                    <button type="button" class="quiet-button" onclick={() => { forgetAsk = ''; }}>{T.savedForgetNo}</button>
                  </div>
                </div>
              {:else}
                <div class="sv-actions">
                  <button type="button" class="primary-button" disabled={!!flow.busy} onclick={() => flow.use(s)}>{T.savedUse}</button>
                  <button type="button" class="quiet-button" disabled={!!flow.busy || !!s.running} onclick={() => { forgetAsk = s.id; }}>{T.savedForget}</button>
                </div>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </div>

    <form class="sv-form" onsubmit={submit} autocomplete="off" novalidate>
      <fieldset class="sv-field sv-radios"><legend>{T.fieldType}</legend>
        <label><input type="radio" name="sv-os" value="linux" bind:group={form.os} /> {T.typeLinux}</label>
        <label><input type="radio" name="sv-os" value="windows" bind:group={form.os} /> {T.typeWindows}</label>
      </fieldset>
      <label class="sv-field"><span>{T.fieldName}</span><input type="text" bind:value={form.name} autocomplete="off" /><small>{T.fieldNameHelp}</small></label>
      <div class="sv-two">
        <label class="sv-field"><span>{T.fieldHost}</span><input type="text" bind:value={form.host} autocomplete="off" spellcheck="false" /><small>{T.fieldHostHelp}</small></label>
        <label class="sv-field sv-narrow"><span>{T.fieldPort}</span><input type="text" inputmode="numeric" bind:value={form.port} autocomplete="off" /><small>{T.fieldPortHelp}</small></label>
      </div>
      <label class="sv-field"><span>{T.fieldUser}</span><input type="text" bind:value={form.user} autocomplete="off" spellcheck="false" /></label>
      <fieldset class="sv-field sv-radios"><legend>{T.fieldAuth}</legend>
        <label><input type="radio" name="sv-auth" value="password" bind:group={form.auth} onchange={() => { clearSecrets(); }} /> {T.authPassword}</label>
        <label><input type="radio" name="sv-auth" value="key" bind:group={form.auth} onchange={() => { clearSecrets(); }} /> {T.authKey}</label>
      </fieldset>
      {#if form.auth === 'password'}
        <label class="sv-field"><span>{T.fieldPassword}</span>
          <input type="password" bind:this={passwordEl} autocomplete="new-password" oninput={noteSecret} />
          <small>{T.fieldPasswordHelp}</small></label>
      {:else}
        <label class="sv-field"><span>{T.fieldKeyFile}</span>
          <input type="file" bind:this={keyEl} onchange={noteSecret} />
          <small>{T.fieldKeyFileHelp}</small></label>
        <label class="sv-field"><span>{T.fieldPassphrase}</span>
          <input type="password" bind:this={passphraseEl} autocomplete="new-password" />
          <small>{T.fieldPassphraseHelp}</small></label>
      {/if}
      <label class="sv-field"><span>{T.fieldMachine}</span><input type="text" bind:value={form.seedMachine} autocomplete="off" spellcheck="false" /><small>{T.fieldMachineHelp}</small></label>

      {#if tried && problems.length}
        <ul class="sv-problems" role="alert">{#each problems as p (p)}<li>{p}</li>{/each}</ul>
      {/if}
      <div class="sv-actions">
        <button type="submit" class="primary-button" disabled={!!flow.busy}>{flow.busy === 'connect' ? T.connecting : T.connectGo}</button>
      </div>
    </form>
  {/if}
</section>

<style>
  .sv-saved { display: grid; gap: var(--space-2); }
  .sv-saved h4 { margin: 0; font-size: var(--fs-4); font-weight: 600; }
  .sv-list { display: grid; margin: 0; padding: 0; list-style: none; }
  .sv-row { display: grid; gap: var(--space-2); padding: var(--space-3) 0; border-top: 1px solid var(--line); min-width: 0; }
  .sv-row-main { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--space-2); min-width: 0; overflow-wrap: anywhere; }
  .sv-confirm { display: grid; gap: var(--space-2); padding: var(--space-3); border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface-2); }
  .sv-confirm p { margin: 0; font-size: 13px; }
  .sv-two { display: flex; flex-wrap: wrap; gap: var(--space-3); }
  .sv-two > :first-child { flex: 1 1 220px; }
  .sv-narrow { flex: 0 1 120px; }
  .sv-problems { margin: 0; padding-left: var(--space-5); color: var(--bad); font-size: 13px; }
  .sv-chip-live { color: var(--text); border: 1px solid var(--accent); }
</style>
