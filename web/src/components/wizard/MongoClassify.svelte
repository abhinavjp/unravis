<script lang="ts">
  import { wizard } from '../../lib/wizard.svelte';
  import { classifyMongo, removeConnection, sourcesView, testMongo } from '../../lib/wizardSources.svelte';
  import { DEFAULT_COLLECTION, heldCollection, heldConnection } from '../../lib/wizardConnections';
  import { classFromTicks, databaseClass, defaultAuditDatabases } from '../../lib/wizardSources';
  import type { ConnectionInput, RecentConnection, TLSMode } from '../../lib/types';
  import { conn } from '../../lib/appState.svelte';
  import { SOURCES_TEXT, classSummaryText, databaseProblemText, testedText } from '../../lib/copy/wizardSourcesCopy';

  // Connect to MongoDB, list its databases, and say what each one is: API audit logs, other data, or not used. Only a database that was listed
  // by a successful test can be chosen. The password is kept in memory only (wizardConnections.ts) and the draft holds an id, never the password.
  let { onDone }: { onDone?: () => void } = $props();

  const uid = $props.id();
  let uri = $state('');
  let username = $state('');
  let password = $state('');
  let authSource = $state('');
  // A saved connection (Windows-protected, from before): its address and user come back, and the server supplies the saved password.
  let savedId = $state('');
  let savedTls = $state<TLSMode>('uri');
  let savedCa = $state('');
  let savedHasPassword = $state(false);
  let tlsMode = $state<TLSMode>('system');
  let caCertificate = $state('');
  let collection = $state(DEFAULT_COLLECTION);
  let busy = $state(false);
  // Set after a good test: the form stays filled and says which connection is held. A password typed for it is kept in memory, not in the field.
  let connectedTo = $state('');
  let passwordKept = $state(false);
  let line = $state('');
  let failed = $state(false);
  let uriField = $state<HTMLInputElement>();
  // The databases the person set aside on purpose, the second tick of a database, and the ticks the name set on, by "connection id/database":
  // they live in sourcesView (setAside, alsoApp, suggested), so opening another form and coming back does not lose them and an untick sticks.
  let problems = $state<Record<string, string>>({});
  let retesting = $state<Record<string, boolean>>({});
  let retestLine = $state<Record<string, string>>({});

  const counts = (id: string): { audit: number; other: number } => {
    const own = wizard.sources.filter((s) => s.kind === 'mongo-database' && s.ref.startsWith('mongo:' + id + '/'));
    return { audit: own.filter((s) => s.classification === 'audit').length, other: own.filter((s) => s.classification === 'other').length };
  };
  const auditOn = (id: string, database: string): boolean => databaseClass(wizard.sources, id, database) === 'audit';
  const appOn = (id: string, database: string): boolean => databaseClass(wizard.sources, id, database) === 'other' || (auditOn(id, database) && !!sourcesView.alsoApp[id + '/' + database]);

  // A database that looks like an audit log starts with the audit tick on, unless the person already decided about it.
  function tickAuditByName(id: string) {
    const databases = sourcesView.connections.find((c) => c.id === id)?.databases ?? [];
    for (const database of defaultAuditDatabases(databases, (d) => databaseClass(wizard.sources, id, d) !== '' || !!sourcesView.setAside[id + '/' + d])) {
      if (classifyMongo(id, database, 'audit') === null) sourcesView.suggested[id + '/' + database] = true;
    }
  }

  function useSaved(id: string) {
    const recent: RecentConnection | undefined = conn.recentConnections.find((r) => r.id === id);
    if (!recent) { savedId = ''; savedHasPassword = false; savedTls = 'uri'; savedCa = ''; return; }
    uri = recent.uri; username = recent.username; authSource = recent.authSource; password = '';
    savedId = recent.id; savedTls = recent.tlsMode || 'uri'; savedCa = recent.caCertificate || ''; savedHasPassword = recent.hasCredentials;
    tlsMode = savedTls; caCertificate = savedCa; collection = recent.collection || DEFAULT_COLLECTION;
  }
  async function loadCa(event: Event) {
    const el = event.currentTarget as HTMLInputElement;
    const file = el.files?.[0];
    if (!file) return;
    try {
      const text = file.size > 256 * 1024 ? '' : await file.text();
      if (!/-----BEGIN CERTIFICATE-----[\s\S]+?-----END CERTIFICATE-----/.test(text) || /-----BEGIN [^-]*PRIVATE KEY-----/.test(text)) { caCertificate = ''; el.value = ''; failed = true; line = SOURCES_TEXT.mongoCaBad; return; }
      caCertificate = text; failed = false; line = '';
    } catch { caCertificate = ''; el.value = ''; failed = true; line = SOURCES_TEXT.mongoCaBad; }
  }
  // Editing the address, user or auth database makes it a new connection, so the saved id no longer applies.
  function edited() { connectedTo = ''; passwordKept = false; if (savedId !== '') { savedId = ''; savedHasPassword = false; savedTls = 'uri'; savedCa = ''; } }

  async function test() {
    if (busy) return;
    if (uri.trim() === '') { failed = true; line = SOURCES_TEXT.mongoNeedsAddress; uriField?.focus(); return; }
    if (tlsMode === 'custom' && caCertificate === '') { failed = true; line = SOURCES_TEXT.mongoCaNeeded; return; }
    busy = true; failed = false; line = SOURCES_TEXT.mongoTesting;
    const input: ConnectionInput = { uri: uri.trim(), username: username.trim(), password, authSource: authSource.trim(), tlsMode, caCertificate: tlsMode === 'custom' ? caCertificate : '', ...(savedId ? { savedConnectionId: savedId } : {}) };
    const result = await testMongo(input, '', undefined, collection);
    busy = false;
    if (!result.ok) { failed = true; line = result.text !== '' ? result.text : SOURCES_TEXT.mongoFailed; return; }
    // The connection is held now. The form keeps what was typed so it is clear which connection is in use; only the password is cleared, so it does not stay in the form.
    if (password !== '') passwordKept = true;
    password = '';
    connectedTo = uri.trim();
    line = testedText(result.databases);
    tickAuditByName(result.id);
    onDone?.();
  }

  async function testAgain(id: string) {
    const input = heldConnection(id);
    if (!input || retesting[id]) return;
    retesting[id] = true; retestLine[id] = SOURCES_TEXT.mongoTesting;
    const result = await testMongo(input, id, undefined, heldCollection(id));
    retesting[id] = false;
    retestLine[id] = result.ok ? testedText(result.databases) : (result.text !== '' ? result.text : SOURCES_TEXT.mongoFailed);
    if (result.ok) tickAuditByName(id);
  }

  // The two ticks of a database. API audit logs is a source of the analysis; Application data is named and kept; neither reads nothing.
  // A refused change (the list is full) puts the box back as it was.
  function tick(event: Event, id: string, database: string, which: 'audit' | 'app') {
    const box = event.currentTarget as HTMLInputElement;
    const key = id + '/' + database;
    const audit = which === 'audit' ? box.checked : auditOn(id, database);
    const app = which === 'app' ? box.checked : appOn(id, database);
    problems[key] = '';
    sourcesView.suggested[key] = false;
    const value = classFromTicks(audit, app);
    sourcesView.setAside[key] = value === 'unused';
    const problem = classifyMongo(id, database, value);
    if (problem) { problems[key] = databaseProblemText(problem); box.checked = which === 'audit' ? auditOn(id, database) : appOn(id, database); return; }
    sourcesView.alsoApp[key] = audit && app;
  }

  function onKey(event: KeyboardEvent) {
    if (event.key === 'Enter' && !event.isComposing) { event.preventDefault(); event.stopPropagation(); void test(); }
  }
</script>

<div class="mongo">
  <p class="hint">{SOURCES_TEXT.mongoIntro}</p>
  <div class="form">
    {#if conn.recentConnections.length}
      <div class="field">
        <label for="{uid}-saved">{SOURCES_TEXT.mongoSaved}</label>
        <select id="{uid}-saved" value={savedId} onchange={(e) => useSaved((e.currentTarget as HTMLSelectElement).value)}>
          <option value="">{SOURCES_TEXT.mongoSavedNone}</option>
          {#each conn.recentConnections as recent (recent.id)}<option value={recent.id}>{recent.label}{recent.hasCredentials ? ' ' + SOURCES_TEXT.mongoSavedHasPassword : ''}</option>{/each}
        </select>
        {#if savedId !== '' && savedHasPassword}<p class="hint">{SOURCES_TEXT.mongoSavedPasswordUsed}</p>{/if}
      </div>
    {/if}
    <div class="field">
      <label for="{uid}-uri">{SOURCES_TEXT.mongoUri}</label>
      <input id="{uid}-uri" type="text" bind:this={uriField} bind:value={uri} oninput={edited} autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="2000"
        aria-describedby="{uid}-uri-hint {uid}-line" aria-invalid={failed && uri.trim() === '' && line !== '' ? true : undefined} onkeydown={onKey} />
      <p class="hint" id="{uid}-uri-hint">{SOURCES_TEXT.mongoUriHint}</p>
    </div>
    <div class="field">
      <label for="{uid}-tls">{SOURCES_TEXT.mongoTls}</label>
      <select id="{uid}-tls" bind:value={tlsMode}>
        <option value="system">{SOURCES_TEXT.mongoTlsSystem}</option>
        <option value="custom">{SOURCES_TEXT.mongoTlsCustom}</option>
        <option value="insecure">{SOURCES_TEXT.mongoTlsInsecure}</option>
        <option value="uri">{SOURCES_TEXT.mongoTlsUri}</option>
        <option value="off">{SOURCES_TEXT.mongoTlsOff}</option>
      </select>
      {#if tlsMode === 'custom'}<label for="{uid}-ca">{SOURCES_TEXT.mongoCa}</label><input id="{uid}-ca" type="file" accept=".pem,.crt,application/x-pem-file" onchange={loadCa} />{/if}
      {#if tlsMode === 'insecure'}<p class="hint" role="alert">{SOURCES_TEXT.mongoTlsInsecureWarning}</p>{/if}
      {#if tlsMode === 'off'}<p class="hint" role="alert">{SOURCES_TEXT.mongoTlsOffWarning}</p>{/if}
    </div>
    <div class="field">
      <label for="{uid}-coll">{SOURCES_TEXT.mongoCollection}</label>
      <input id="{uid}-coll" type="text" bind:value={collection} autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="120" onkeydown={onKey} />
      <p class="hint">{SOURCES_TEXT.mongoCollectionHint}</p>
    </div>
    <div class="pair">
      <div class="field">
        <label for="{uid}-user">{SOURCES_TEXT.mongoUser}</label>
        <input id="{uid}-user" type="text" bind:value={username} oninput={edited} autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="200" onkeydown={onKey} />
      </div>
      <div class="field">
        <label for="{uid}-pass">{SOURCES_TEXT.mongoPassword}</label>
        <input id="{uid}-pass" type="password" bind:value={password} oninput={() => { passwordKept = false; }} placeholder={passwordKept || (savedId !== '' && savedHasPassword) ? SOURCES_TEXT.mongoPasswordKept : ''} autocomplete="new-password" spellcheck="false" maxlength="500" onkeydown={onKey} />
      </div>
      <div class="field">
        <label for="{uid}-auth">{SOURCES_TEXT.mongoAuth}</label>
        <input id="{uid}-auth" type="text" bind:value={authSource} oninput={edited} autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="120" onkeydown={onKey} />
      </div>
    </div>
    <div class="actions"><button type="button" class="primary-button" disabled={busy} onclick={() => void test()}>{busy ? SOURCES_TEXT.mongoTesting : SOURCES_TEXT.mongoTest}</button></div>
    <p class="line" class:bad={failed} id="{uid}-line" role="status" hidden={line === ''}>{line}</p>
    {#if connectedTo !== '' && !failed}<p class="hint connected">{SOURCES_TEXT.mongoConnectedTo}: {connectedTo}</p>{/if}
  </div>

  <p class="hint">{SOURCES_TEXT.mongoNow}</p>

  {#each sourcesView.connections as connection (connection.id)}
    {@const done = counts(connection.id)}
    <section class="conn" aria-labelledby="{uid}-{connection.id}-h">
      <div class="conn-top">
        <h5 id="{uid}-{connection.id}-h">{SOURCES_TEXT.mongoListHeading}: {connection.host}</h5>
        <div class="actions">
          <button type="button" class="secondary-button" disabled={retesting[connection.id]} aria-label="{SOURCES_TEXT.mongoTestAgain}: {connection.host}" onclick={() => void testAgain(connection.id)}>{SOURCES_TEXT.mongoTestAgain}</button>
          <button type="button" class="quiet-button" aria-label="{SOURCES_TEXT.mongoRemove}: {connection.host}" onclick={() => { removeConnection(connection.id); onDone?.(); }}>{SOURCES_TEXT.mongoRemove}</button>
        </div>
      </div>
      <p class="line" role="status" hidden={!retestLine[connection.id]}>{retestLine[connection.id] ?? ''}</p>
      {#if connection.warning !== ''}<p class="hint">{connection.warning}</p>{/if}
      {#if connection.databases.length === 0}
        <p class="hint">{SOURCES_TEXT.mongoNoDatabases}</p>
      {:else}
        <p class="hint">{SOURCES_TEXT.mongoClassifyHelp} {SOURCES_TEXT.mongoAuditOptional}</p>
        <p class="hint">{SOURCES_TEXT.mongoChecksHint}</p>
        <ul class="dbs">
          {#each connection.databases as database, i (database)}
            {@const rowId = uid + '-' + connection.id + '-' + i}
            {@const audit = auditOn(connection.id, database)}
            {@const app = appOn(connection.id, database)}
            <li class="db">
              <fieldset class="db-set" aria-describedby={problems[connection.id + '/' + database] ? rowId + '-problem' : undefined}>
                <legend class="db-name">{database}</legend>
                <span class="hint">{SOURCES_TEXT.mongoWhatIsIn}</span>
                <label class="tick" for="{rowId}-audit"><input id="{rowId}-audit" type="checkbox" checked={audit} onchange={(event) => tick(event, connection.id, database, 'audit')} /> {SOURCES_TEXT.mongoTickAudit} <span class="hint">{SOURCES_TEXT.mongoTickAuditNote}</span></label>
                <label class="tick" for="{rowId}-app"><input id="{rowId}-app" type="checkbox" checked={app} onchange={(event) => tick(event, connection.id, database, 'app')} /> {SOURCES_TEXT.mongoTickApp} <span class="hint">{SOURCES_TEXT.mongoTickAppNote}</span></label>
                {#if audit && sourcesView.suggested[connection.id + '/' + database]}<p class="hint">{SOURCES_TEXT.mongoAuditSuggested}</p>{/if}
                {#if audit && app}<p class="hint">{SOURCES_TEXT.mongoBothTicked}</p>{/if}
                {#if !audit && !app}<p class="hint">{SOURCES_TEXT.mongoNeitherTicked}</p>{/if}
              </fieldset>
              <p class="inline-error" id="{rowId}-problem" role="alert" hidden={!problems[connection.id + '/' + database]}>{problems[connection.id + '/' + database] ?? ''}</p>
            </li>
          {/each}
        </ul>
        <p class="hint" role="status">{classSummaryText(done.audit, done.other)}</p>
      {/if}
    </section>
  {/each}
</div>

<style>
  .mongo { display: grid; gap: var(--space-3); min-width: 0; max-width: 760px; }
  .form { display: grid; gap: var(--space-3); min-width: 0; }
  .pair { display: grid; grid-template-columns: repeat(auto-fit, minmax(190px, 1fr)); gap: var(--space-3); }
  .field { display: grid; gap: var(--space-1); align-content: start; min-width: 0; }
  .field label { font-size: var(--fs-3); font-weight: 600; }
  .field input, .field select { box-sizing: border-box; width: 100%; max-width: 100%; min-height: 44px; padding: 8px 10px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-4); }
  .field input:focus-visible, .field select:focus-visible, .tick input:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .field input[aria-invalid='true'] { border-color: var(--bad); }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .line { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .line.bad { color: var(--text); }
  .connected { color: var(--text); font-weight: 600; }
  .line[hidden], .inline-error[hidden] { display: none; }
  .conn { display: grid; gap: var(--space-2); min-width: 0; padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .conn-top { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--space-2); }
  .conn-top h5 { margin: 0; font-size: var(--fs-4); font-weight: 600; overflow-wrap: anywhere; }
  .dbs { display: grid; gap: var(--space-2); margin: 0; padding: 0; list-style: none; }
  .db { display: grid; gap: var(--space-1); min-width: 0; }
  .db-set { display: grid; gap: var(--space-1); min-width: 0; margin: 0; padding: var(--space-2) var(--space-3); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .db-name { padding: 0 var(--space-1); font-size: var(--fs-4); font-weight: 600; overflow-wrap: anywhere; }
  .tick { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); min-height: 44px; font-size: var(--fs-3); }
  .tick input { width: 20px; height: 20px; margin: 0; }
  .db .inline-error { margin: 0; }
</style>
