<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../lib/api';
  import { addByAddress, sourcesView } from '../../lib/wizardSources.svelte';
  import { blankSqlForm, formFromSaved, sqlFormProblem, sqlInput } from '../../lib/sqlConnection';
  import type { SqlForm } from '../../lib/sqlConnection';
  import type { SavedSqlConnection } from '../../lib/types';
  import { SOURCES_TEXT, addressProblemText, reachedText, unreachedText } from '../../lib/copy/wizardSourcesCopy';

  // SQL Server the way SQL Server Management Studio asks for it. The server is added to the case (and checked for a reply on its port, as before);
  // the rest of the details are kept on this computer for later. Nothing connects with them yet. The password never enters the wizard draft: it goes
  // straight to the kept file, and only when "Remember password" is ticked.
  let { onDone }: { onDone?: () => void } = $props();

  const uid = $props.id();
  let form = $state<SqlForm>(blankSqlForm());
  let saved = $state<SavedSqlConnection[]>([]);
  let busy = $state(false);
  let line = $state('');
  let failed = $state(false);
  let field = $state<HTMLInputElement>();

  async function loadSaved() {
    try { saved = await api.sqlConnections(); } catch { saved = []; /* demo mode or an unreadable file: the form still works */ }
  }
  onMount(() => { void loadSaved(); });

  function fail(text: string, target?: HTMLInputElement) { failed = true; line = text; target?.focus(); }

  async function add() {
    if (busy) return;
    const problem = sqlFormProblem(form);
    if (problem === 'server') return fail(SOURCES_TEXT.sqlNeedServer, field);
    if (problem === 'username') return fail(SOURCES_TEXT.sqlNeedUser);
    busy = true; failed = false; line = SOURCES_TEXT.addrChecking;
    const shown = form.server.trim();
    const result = await addByAddress('sql-server', shown);
    if (!result.ok && result.problem !== 'duplicate') {
      busy = false;
      return fail(result.problem === 'full' ? SOURCES_TEXT.limitFull : addressProblemText(result.problem), field);
    }
    const reachLine = result.ok
      ? (result.reached === true ? reachedText(shown) : unreachedText(shown, sourcesView.reach[result.key]?.state === 'failed' ? (sourcesView.reach[result.key] as { reason: string }).reason : 'unknown') + ' ' + SOURCES_TEXT.addrFlagNote)
      : SOURCES_TEXT.alreadyChosen;
    const withPassword = form.auth === 'sql' && form.rememberPassword && form.password !== '';
    try {
      await api.saveSqlConnection(sqlInput(form));
      line = reachLine + ' ' + (withPassword ? SOURCES_TEXT.sqlKeptWithPassword : SOURCES_TEXT.sqlKept);
      failed = result.ok && result.reached !== true;
      form = blankSqlForm();
      await loadSaved();
    } catch (error) {
      failed = true;
      line = reachLine + ' ' + SOURCES_TEXT.sqlNotKept + (error instanceof Error ? error.message : '');
      form.password = '';
    }
    busy = false;
    onDone?.();
  }

  async function forget(id: string) {
    try { await api.forgetSqlConnection(id); } catch { /* it stays on the list; try again */ }
    await loadSaved();
  }

  function use(item: SavedSqlConnection) { form = formFromSaved(item); failed = false; line = ''; field?.focus(); }

  function onKey(event: KeyboardEvent) {
    if (event.key === 'Enter' && !event.isComposing) { event.preventDefault(); event.stopPropagation(); void add(); }
  }
</script>

<div class="sql">
  <p class="hint">{SOURCES_TEXT.sqlIntro}</p>
  {#if saved.length}
    <div class="group">
      <h5>{SOURCES_TEXT.sqlSavedHeading}</h5>
      <ul class="kept">
        {#each saved as item (item.id)}
          <li>
            <span class="what"><span class="name">{item.label}</span>
              <span class="hint">{item.auth === 'windows' ? SOURCES_TEXT.sqlWindowsLogin : item.username}{item.database ? ' · ' + item.database : ''} · {item.hasPassword ? SOURCES_TEXT.sqlHasPassword : SOURCES_TEXT.sqlNoPassword}</span></span>
            <button type="button" class="link-button" aria-label="{SOURCES_TEXT.sqlUse}: {item.label}" onclick={() => use(item)}>{SOURCES_TEXT.sqlUse}</button>
            <button type="button" class="link-button" aria-label="{SOURCES_TEXT.sqlForget}: {item.label}" onclick={() => void forget(item.id)}>{SOURCES_TEXT.sqlForget}</button>
          </li>
        {/each}
      </ul>
    </div>
  {/if}
  <div class="field">
    <label for="{uid}-server">{SOURCES_TEXT.sqlServer}</label>
    <input id="{uid}-server" type="text" bind:this={field} bind:value={form.server} maxlength="260" autocomplete="off" autocapitalize="off" spellcheck="false"
      aria-describedby="{uid}-server-hint {uid}-line" aria-invalid={failed && form.server.trim() === '' ? true : undefined} onkeydown={onKey} />
    <p class="hint" id="{uid}-server-hint">{SOURCES_TEXT.sqlServerHint}</p>
  </div>
  <div class="field">
    <label for="{uid}-auth">{SOURCES_TEXT.sqlAuth}</label>
    <select id="{uid}-auth" bind:value={form.auth}>
      <option value="sql">{SOURCES_TEXT.sqlAuthSql}</option>
      <option value="windows">{SOURCES_TEXT.sqlAuthWindows}</option>
    </select>
  </div>
  {#if form.auth === 'sql'}
    <div class="pair">
      <div class="field">
        <label for="{uid}-user">{SOURCES_TEXT.sqlUser}</label>
        <input id="{uid}-user" type="text" bind:value={form.username} maxlength="200" autocomplete="off" autocapitalize="off" spellcheck="false"
          aria-invalid={failed && form.username.trim() === '' ? true : undefined} onkeydown={onKey} />
      </div>
      <div class="field">
        <label for="{uid}-pass">{SOURCES_TEXT.sqlPassword}</label>
        <input id="{uid}-pass" type="password" bind:value={form.password} maxlength="512" autocomplete="off" onkeydown={onKey} />
      </div>
    </div>
    <label class="tick"><input type="checkbox" bind:checked={form.rememberPassword} aria-describedby="{uid}-remember-hint" /> <span>{SOURCES_TEXT.sqlRemember}</span></label>
    <p class="hint" id="{uid}-remember-hint">{SOURCES_TEXT.sqlRememberHint}</p>
  {/if}
  <div class="pair">
    <div class="field">
      <label for="{uid}-db">{SOURCES_TEXT.sqlDatabase}</label>
      <input id="{uid}-db" type="text" bind:value={form.database} maxlength="200" placeholder="‹default›" autocomplete="off" autocapitalize="off" spellcheck="false" onkeydown={onKey} />
      <p class="hint">{SOURCES_TEXT.sqlDatabaseHint}</p>
    </div>
    <div class="field">
      <label for="{uid}-enc">{SOURCES_TEXT.sqlEncrypt}</label>
      <select id="{uid}-enc" bind:value={form.encrypt}>
        <option value="mandatory">{SOURCES_TEXT.sqlEncryptMandatory}</option>
        <option value="optional">{SOURCES_TEXT.sqlEncryptOptional}</option>
        <option value="strict">{SOURCES_TEXT.sqlEncryptStrict}</option>
      </select>
    </div>
  </div>
  <label class="tick"><input type="checkbox" bind:checked={form.trustServerCertificate} /> <span>{SOURCES_TEXT.sqlTrust}</span></label>
  <div class="field">
    <label for="{uid}-name">{SOURCES_TEXT.sqlName}</label>
    <input id="{uid}-name" type="text" bind:value={form.label} maxlength="200" autocomplete="off" spellcheck="false" onkeydown={onKey} />
    <p class="hint">{SOURCES_TEXT.sqlNameHint}</p>
  </div>
  <div class="actions"><button type="button" class="primary-button" disabled={busy} onclick={() => void add()}>{busy ? SOURCES_TEXT.sqlAdding : SOURCES_TEXT.sqlAdd}</button></div>
  <p class="line" class:bad={failed} id="{uid}-line" role="status" hidden={line === ''}>{line}</p>
</div>

<style>
  .sql { display: grid; gap: var(--space-3); min-width: 0; max-width: 640px; }
  .field { display: grid; gap: var(--space-1); min-width: 0; }
  .field label { font-size: var(--fs-3); font-weight: 600; }
  .field input, .field select { box-sizing: border-box; width: 100%; max-width: 100%; min-height: 44px; padding: 8px 10px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-4); }
  .field input:focus-visible, .field select:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .field input[aria-invalid='true'] { border-color: var(--bad); }
  .pair { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: var(--space-3); min-width: 0; }
  .tick { display: flex; align-items: center; gap: var(--space-2); font-size: var(--fs-4); }
  .tick input { width: 22px; height: 22px; margin: 0; accent-color: var(--accent); }
  .tick input:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .group { display: grid; gap: var(--space-2); }
  .group h5 { margin: 0; font-size: var(--fs-4); font-weight: 600; }
  .kept { display: grid; gap: var(--space-2); margin: 0; padding: 0; list-style: none; }
  .kept li { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); padding: var(--space-2) var(--space-3); background: var(--bg); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .what { display: grid; flex: 1 1 200px; min-width: 0; }
  .name { font-size: var(--fs-4); font-weight: 600; overflow-wrap: anywhere; }
  .link-button { min-height: 44px; padding: 0 var(--space-1); color: var(--accent); background: transparent; border: 0; font: inherit; font-size: var(--fs-3); text-decoration: underline; cursor: pointer; }
  .link-button:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .line { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .line.bad { color: var(--text); }
  .line[hidden] { display: none; }
</style>
