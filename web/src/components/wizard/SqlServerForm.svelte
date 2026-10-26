<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../lib/api';
  import { addByAddress, sourcesView } from '../../lib/wizardSources.svelte';
  import { blankSqlForm, formFromSaved, sqlConnectionKey, sqlFormProblem, sqlInput, sqlTestInput, usesKeptPassword } from '../../lib/sqlConnection';
  import type { SqlForm } from '../../lib/sqlConnection';
  import type { SavedSqlConnection } from '../../lib/types';
  import { SOURCES_TEXT, addressProblemText, unreachedText } from '../../lib/copy/wizardSourcesCopy';
  import { connectedText } from '../../lib/copy/healthCopy';

  // SQL Server the way SQL Server Management Studio asks for it. "Test and list databases" connects (read only) and fills the Database list.
  // "Add and keep details" tests the connection too, adds the server to the case, and keeps the details on this computer. The form keeps its fields.
  // The password never enters the wizard draft: it goes straight to the kept file, and only when "Remember password" is ticked.
  let { onDone }: { onDone?: () => void } = $props();

  const uid = $props.id();
  let form = $state<SqlForm>(blankSqlForm());
  let saved = $state<SavedSqlConnection[]>([]);
  let busy = $state(false);
  let testing = $state(false);
  let line = $state('');
  let failed = $state(false);
  let field = $state<HTMLInputElement>();
  /** Databases from the last good test, and the connection they were listed for. */
  let databases = $state<string[]>([]);
  let listedFor = $state('');
  let listNote = $state('');
  let typedDatabase = $state(false);

  const stale = $derived(listedFor !== '' && listedFor !== sqlConnectionKey(form));
  const hasList = $derived(databases.length > 0 && !stale);
  const options = $derived(form.database !== '' && !databases.includes(form.database) ? [form.database, ...databases] : databases);

  async function loadSaved() {
    try { saved = await api.sqlConnections(); } catch { saved = []; /* demo mode or an unreadable file: the form still works */ }
  }
  onMount(() => { void loadSaved(); });

  function fail(text: string, target?: HTMLInputElement) { failed = true; line = text; target?.focus(); }

  /** Connects once. Returns the sentence for the screen and fills the database list on success. */
  async function connect(): Promise<{ ok: boolean; text: string }> {
    const input = sqlTestInput(form);
    try {
      const result = await api.testSqlConnection(input);
      databases = result.databases; listedFor = sqlConnectionKey(form); typedDatabase = false;
      listNote = result.listError ? SOURCES_TEXT.sqlTestNoList : result.databases.length + ' ' + SOURCES_TEXT.sqlTestListed;
      if (result.listError) databases = [];
      return { ok: true, text: connectedText(result.version, result.edition) + (usesKeptPassword(form) ? ' ' + SOURCES_TEXT.sqlTestKeptPassword : '') };
    } catch (error) {
      databases = []; listedFor = ''; listNote = '';
      return { ok: false, text: SOURCES_TEXT.sqlTestFailed + (error instanceof Error ? error.message : '') };
    }
  }

  function needsPassword(): boolean { return form.auth === 'sql' && form.password === '' && !usesKeptPassword(form); }

  async function test() {
    if (busy || testing) return;
    const problem = sqlFormProblem(form);
    if (problem === 'server') return fail(SOURCES_TEXT.sqlNeedServer, field);
    if (problem === 'username') return fail(SOURCES_TEXT.sqlNeedUser);
    testing = true; failed = false; line = SOURCES_TEXT.sqlTesting;
    const result = await connect();
    failed = !result.ok; line = result.text;
    testing = false;
  }

  async function add() {
    if (busy || testing) return;
    const problem = sqlFormProblem(form);
    if (problem === 'server') return fail(SOURCES_TEXT.sqlNeedServer, field);
    if (problem === 'username') return fail(SOURCES_TEXT.sqlNeedUser);
    busy = true; failed = false; line = SOURCES_TEXT.sqlTesting;
    const connected = needsPassword() ? { ok: false, text: SOURCES_TEXT.sqlTestFailed + SOURCES_TEXT.sqlTestNeedPassword } : await connect();
    line = SOURCES_TEXT.addrChecking;
    const shown = form.server.trim();
    const result = await addByAddress('sql-server', shown);
    if (!result.ok && result.problem !== 'duplicate') {
      busy = false;
      return fail(connected.text + ' ' + (result.problem === 'full' ? SOURCES_TEXT.limitFull : addressProblemText(result.problem)), field);
    }
    const reachLine = result.ok && result.reached !== true
      ? ' ' + unreachedText(shown, sourcesView.reach[result.key]?.state === 'failed' ? (sourcesView.reach[result.key] as { reason: string }).reason : 'unknown') + ' ' + SOURCES_TEXT.addrFlagNote
      : (result.ok ? '' : ' ' + SOURCES_TEXT.alreadyChosen);
    const withPassword = form.auth === 'sql' && form.rememberPassword && form.password !== '';
    try {
      const kept = await api.saveSqlConnection(sqlInput(form));
      line = connected.text + reachLine + ' ' + (withPassword ? SOURCES_TEXT.sqlKeptWithPassword : SOURCES_TEXT.sqlKept);
      failed = !connected.ok;
      if (kept.hasPassword) form = { ...form, keptId: kept.id, keptServer: kept.server, keptUser: kept.username, keptAuth: kept.auth };
      await loadSaved();
    } catch (error) {
      failed = true;
      line = connected.text + reachLine + ' ' + SOURCES_TEXT.sqlNotKept + (error instanceof Error ? error.message : '');
    }
    busy = false;
    onDone?.();
  }

  async function forget(id: string) {
    try { await api.forgetSqlConnection(id); } catch { /* it stays on the list; try again */ }
    await loadSaved();
  }

  function use(item: SavedSqlConnection) {
    form = formFromSaved(item); failed = false; line = ''; databases = []; listedFor = ''; listNote = ''; typedDatabase = false; field?.focus();
  }

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
  <div class="pair">
    <div class="field">
      <label for="{uid}-auth">{SOURCES_TEXT.sqlAuth}</label>
      <select id="{uid}-auth" bind:value={form.auth}>
        <option value="sql">{SOURCES_TEXT.sqlAuthSql}</option>
        <option value="windows">{SOURCES_TEXT.sqlAuthWindows}</option>
      </select>
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
  <div class="actions"><button type="button" class="secondary-button" disabled={busy || testing} onclick={() => void test()}>{testing ? SOURCES_TEXT.sqlTesting : SOURCES_TEXT.sqlTest}</button></div>
  <div class="field">
    <label for="{uid}-db">{SOURCES_TEXT.sqlDatabase}</label>
    {#if hasList && !typedDatabase}
      <select id="{uid}-db" bind:value={form.database}>
        <option value="">{SOURCES_TEXT.sqlDatabaseDefault}</option>
        {#each options as name (name)}<option value={name}>{name}</option>{/each}
      </select>
      <button type="button" class="link-button start" onclick={() => { typedDatabase = true; }}>{SOURCES_TEXT.sqlDatabaseTyped}</button>
    {:else}
      <input id="{uid}-db" type="text" bind:value={form.database} maxlength="200" placeholder={SOURCES_TEXT.sqlDatabaseChoose} autocomplete="off" autocapitalize="off" spellcheck="false" onkeydown={onKey} />
    {/if}
    <p class="hint">{stale ? SOURCES_TEXT.sqlTestStale : (listNote || SOURCES_TEXT.sqlDatabaseHint)}</p>
  </div>
  <div class="field">
    <label for="{uid}-name">{SOURCES_TEXT.sqlName}</label>
    <input id="{uid}-name" type="text" bind:value={form.label} maxlength="200" autocomplete="off" spellcheck="false" onkeydown={onKey} />
    <p class="hint">{SOURCES_TEXT.sqlNameHint}</p>
  </div>
  <div class="actions"><button type="button" class="primary-button" disabled={busy || testing} onclick={() => void add()}>{busy ? SOURCES_TEXT.sqlAdding : SOURCES_TEXT.sqlAdd}</button></div>
  <p class="line" class:bad={failed} class:good={!failed && line !== '' && !busy && !testing} id="{uid}-line" role="status" hidden={line === ''}>{line}</p>
</div>

<style>
  .sql { display: grid; gap: var(--space-3); min-width: 0; max-width: 640px; }
  .field { display: grid; gap: var(--space-1); min-width: 0; }
  .field label { font-size: var(--fs-3); font-weight: 600; }
  .field input, .field select { box-sizing: border-box; width: 100%; max-width: 100%; min-height: 44px; padding: 8px 10px; color: var(--text); background-color: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-4); }
  .field select {
    appearance: none; padding-right: 36px; cursor: pointer;
    background-image: linear-gradient(45deg, transparent 50%, var(--text-2) 50%), linear-gradient(135deg, var(--text-2) 50%, transparent 50%);
    background-position: calc(100% - 18px) 55%, calc(100% - 13px) 55%; background-size: 5px 5px, 5px 5px; background-repeat: no-repeat;
  }
  .field select option { color: var(--text); background: var(--bg); }
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
  .link-button.start { justify-self: start; }
  .link-button:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .secondary-button { min-height: 44px; padding: 0 var(--space-4); color: var(--text); background: transparent; border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-4); font-weight: 600; cursor: pointer; }
  .secondary-button:hover:not(:disabled) { border-color: var(--text-2); }
  .secondary-button:disabled { opacity: .55; cursor: not-allowed; }
  .secondary-button:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .line { margin: 0; padding: var(--space-2) var(--space-3); color: var(--text-2); font-size: var(--fs-3); border-left: 3px solid var(--line); overflow-wrap: anywhere; }
  .line.bad { color: var(--text); border-left-color: var(--bad); }
  .line.good { color: var(--text); border-left-color: var(--ok, var(--accent)); }
  .line[hidden] { display: none; }
</style>
