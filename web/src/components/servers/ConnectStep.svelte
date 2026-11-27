<script lang="ts">
  import HelpTip from './HelpTip.svelte';
  import StepFooter from './StepFooter.svelte';
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
    <form id="sv-secret-form" class="sv-form" onsubmit={submitSecret} autocomplete="off">
      {#if secretAuth === 'password'}
        <div class="sv-field"><label for="field-password">{T.fieldPassword}</label>
          <input id="field-password" type="password" bind:this={passwordEl} autocomplete="off" oninput={() => { hasSecret = !!passwordEl?.value; }} />
          <HelpTip id={flow.needSecret ? "help-saved-password" : "help-password"} label={T.helpLabel(T.fieldPassword)} text={T.fieldPasswordHelp} /></div>
      {:else}
        <div class="sv-field"><label for="field-keyfile">{T.fieldKeyFile}</label>
          <input id="field-keyfile" type="file" bind:this={keyEl} onchange={() => { hasSecret = !!keyEl?.files?.length; }} />
          <HelpTip id={flow.needSecret ? "help-saved-keyfile" : "help-keyfile"} label={T.helpLabel(T.fieldKeyFile)} text={T.fieldKeyFileHelp} /></div>
        <div class="sv-field"><label for="field-passphrase">{T.fieldPassphrase}</label>
          <input id="field-passphrase" type="password" bind:this={passphraseEl} autocomplete="off" />
          <HelpTip id={flow.needSecret ? "help-saved-passphrase" : "help-passphrase"} label={T.helpLabel(T.fieldPassphrase)} text={T.fieldPassphraseHelp} /></div>
      {/if}

    </form>
    <StepFooter back={() => flow.reset()} primary={{ label: T.secretSave, form: "sv-secret-form", disabled: !hasSecret || !!flow.busy, busyLabel: flow.busy ? T.working : undefined }} />
  {:else}
    <p class="sv-lead">{T.connectLead}</p>

    <div class="sv-routes" role="group" aria-label={T.fieldType}>
      <button type="button" class:active={form.os === 'linux'} aria-pressed={form.os === 'linux'} onclick={() => form.os = 'linux'}><strong>{T.routeLinux}</strong><span>{T.routeLinuxHelp}</span></button>
      <button type="button" class:active={form.os === 'windows'} aria-pressed={form.os === 'windows'} onclick={() => form.os = 'windows'}><strong>{T.routeWindows}</strong><span>{T.routeWindowsHelp}</span></button>
    </div>
    <div class="sv-columns">
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
      <div class="sv-promise-box"><strong>{T.promiseHeading}</strong><ul>{#each T.promises as promise}<li>{promise}</li>{/each}</ul><p>{T.readOnly}</p></div>
    </div>

    <form id="sv-connect-form" class="sv-form" onsubmit={submit} autocomplete="off" novalidate>
      <div class="sv-two">
        <div class="sv-field"><label for="field-host">{T.fieldHost}</label><HelpTip id="help-host" label={T.helpLabel(T.fieldHost)} text={T.fieldHostHelp} /><input id="field-host" type="text" bind:value={form.host} autocomplete="off" spellcheck="false" /></div>
        <div class="sv-field sv-narrow"><label for="field-port">{T.fieldPort}</label><HelpTip id="help-port" label={T.helpLabel(T.fieldPort)} text={T.fieldPortHelp} /><input id="field-port" type="text" inputmode="numeric" bind:value={form.port} autocomplete="off" /></div>
        <div class="sv-field"><label for="field-user">{T.fieldUser}</label><HelpTip id="help-user" label={T.helpLabel(T.fieldUser)} text={T.fieldUserHelp} /><input id="field-user" type="text" bind:value={form.user} autocomplete="off" spellcheck="false" /></div>
      </div>
      <fieldset class="sv-field"><legend>{T.fieldAuth}</legend><div class="segmented" role="group" aria-label={T.fieldAuth}>
        <button type="button" aria-pressed={form.auth === 'password'} onclick={() => { clearSecrets(); form.auth = 'password'; }}>{T.authPassword}</button>
        <button type="button" aria-pressed={form.auth === 'key'} onclick={() => { clearSecrets(); form.auth = 'key'; }}>{T.authKey}</button>
      </div></fieldset>
      {#if form.auth === 'password'}
        <div class="sv-field"><label for="field-password">{T.fieldPassword}</label>
          <input id="field-password" type="password" bind:this={passwordEl} autocomplete="new-password" oninput={noteSecret} />
          <HelpTip id={flow.needSecret ? "help-saved-password" : "help-password"} label={T.helpLabel(T.fieldPassword)} text={T.fieldPasswordHelp} /></div>
      {:else}
        <div class="sv-field"><label for="field-keyfile">{T.fieldKeyFile}</label>
          <input id="field-keyfile" type="file" bind:this={keyEl} onchange={noteSecret} />
          <HelpTip id={flow.needSecret ? "help-saved-keyfile" : "help-keyfile"} label={T.helpLabel(T.fieldKeyFile)} text={T.fieldKeyFileHelp} /></div>
        <div class="sv-field"><label for="field-passphrase">{T.fieldPassphrase}</label>
          <input id="field-passphrase" type="password" bind:this={passphraseEl} autocomplete="new-password" />
          <HelpTip id={flow.needSecret ? "help-saved-passphrase" : "help-passphrase"} label={T.helpLabel(T.fieldPassphrase)} text={T.fieldPassphraseHelp} /></div>
      {/if}

      {#if tried && problems.length}
        <ul class="sv-problems" role="alert">{#each problems as p (p)}<li>{p}</li>{/each}</ul>
      {/if}

    </form>
    </div>
    <StepFooter primary={{ label: T.connectGo, form: "sv-connect-form", disabled: !!flow.busy, busyLabel: flow.busy === "connect" ? T.connecting : undefined }} />
  {/if}
</section>

<style>
  .sv-routes { display:grid; grid-template-columns:repeat(2,minmax(0,1fr)); gap:12px; }
  .sv-routes button { display:grid; gap:6px; text-align:left; padding:14px; color:var(--text); background:var(--surface); border:1px solid var(--line); border-radius:var(--radius-sm); }
  .sv-routes button.active { border-color:var(--accent); }

  .sv-routes span { color:var(--text-2); font-size:var(--fs-2); }
  .sv-columns { display:grid; grid-template-columns:minmax(0, 1.4fr) minmax(0, 1fr); gap:24px; align-items:start; }
  .sv-columns .sv-saved { grid-column:2; grid-row:1; }
  .sv-columns .sv-form { grid-column:1; grid-row:1; }
  .sv-promise-box { padding:14px; border:1px solid var(--line); background:var(--surface); }
  .sv-promise-box ul { padding-left:20px; }
  @media (max-width:700px) { .sv-columns { display:flex; flex-direction:column-reverse; } .sv-routes { grid-template-columns:1fr; } }
  .sv-saved { display: grid; gap: var(--space-2); }
  .sv-saved h4 { margin: 0; font-size: var(--fs-4); font-weight: 600; }
  .sv-list { display: grid; margin: 0; padding: 0; list-style: none; }
  .sv-row { display: grid; gap: var(--space-2); padding: var(--space-3) 0; border-top: 1px solid var(--line); min-width: 0; }
  .sv-row-main { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--space-2); min-width: 0; overflow-wrap: anywhere; }
  .sv-confirm { display: grid; gap: var(--space-2); padding: var(--space-3); border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface-2); }
  .sv-confirm p { margin: 0; font-size: 13px; }
  .sv-two { display: flex; flex-wrap: wrap; gap: var(--space-3); }
  .sv-two > :first-child { flex: 1 1 220px; }
  .sv-narrow { flex: 0 1 90px; }
  .sv-problems { margin: 0; padding-left: var(--space-5); color: var(--bad); font-size: 13px; }
  .sv-chip-live { color: var(--text); border: 1px solid var(--accent); }
</style>




