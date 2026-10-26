<script lang="ts">
  import { wizard } from '../../lib/wizard.svelte';
  import { classifyMongo, removeConnection, sourcesView, testMongo } from '../../lib/wizardSources.svelte';
  import { DEFAULT_COLLECTION, heldCollection, heldConnection } from '../../lib/wizardConnections';
  import { databaseClass } from '../../lib/wizardSources';
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
  let line = $state('');
  let failed = $state(false);
  let uriField = $state<HTMLInputElement>();
  // The databases the person set aside on purpose, by "connection id/database". The draft does not keep them.
  let setAside = $state<Record<string, boolean>>({});
  let problems = $state<Record<string, string>>({});
  let retesting = $state<Record<string, boolean>>({});
  let retestLine = $state<Record<string, string>>({});

  const CLASS_OPTIONS: ReadonlyArray<{ value: string; label: string }> = [
    { value: '', label: SOURCES_TEXT.classUnset },
    { value: 'audit', label: SOURCES_TEXT.classAudit },
    { value: 'other', label: SOURCES_TEXT.classOther },
    { value: 'unused', label: SOURCES_TEXT.classUnused }
  ];

  const counts = (id: string): { audit: number; other: number } => {
    const own = wizard.sources.filter((s) => s.kind === 'mongo-database' && s.ref.startsWith('mongo:' + id + '/'));
    return { audit: own.filter((s) => s.classification === 'audit').length, other: own.filter((s) => s.classification === 'other').length };
  };
  const valueOf = (id: string, database: string): string => databaseClass(wizard.sources, id, database) || (setAside[id + '/' + database] ? 'unused' : '');

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
  function edited() { if (savedId !== '') { savedId = ''; savedHasPassword = false; savedTls = 'uri'; savedCa = ''; } }

  async function test() {
    if (busy) return;
    if (uri.trim() === '') { failed = true; line = SOURCES_TEXT.mongoNeedsAddress; uriField?.focus(); return; }
    if (tlsMode === 'custom' && caCertificate === '') { failed = true; line = SOURCES_TEXT.mongoCaNeeded; return; }
    busy = true; failed = false; line = SOURCES_TEXT.mongoTesting;
    const input: ConnectionInput = { uri: uri.trim(), username: username.trim(), password, authSource: authSource.trim(), tlsMode, caCertificate: tlsMode === 'custom' ? caCertificate : '', ...(savedId ? { savedConnectionId: savedId } : {}) };
    const result = await testMongo(input, '', undefined, collection);
    busy = false;
    if (!result.ok) { failed = true; line = result.text !== '' ? result.text : SOURCES_TEXT.mongoFailed; return; }
    // The connection is held now; clear the typed fields so a password does not stay in the form.
    uri = ''; username = ''; password = ''; authSource = ''; savedId = ''; savedHasPassword = false; savedTls = 'uri'; savedCa = ''; caCertificate = ''; tlsMode = 'system'; collection = DEFAULT_COLLECTION;
    line = testedText(result.databases);
    onDone?.();
  }

  async function testAgain(id: string) {
    const input = heldConnection(id);
    if (!input || retesting[id]) return;
    retesting[id] = true; retestLine[id] = SOURCES_TEXT.mongoTesting;
    const result = await testMongo(input, id, undefined, heldCollection(id));
    retesting[id] = false;
    retestLine[id] = result.ok ? testedText(result.databases) : (result.text !== '' ? result.text : SOURCES_TEXT.mongoFailed);
  }

  function choose(id: string, database: string, value: string) {
    const key = id + '/' + database;
    problems[key] = '';
    setAside[key] = value === 'unused';
    const problem = classifyMongo(id, database, value === '' ? 'unused' : value);
    if (problem) problems[key] = databaseProblemText(problem);
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
        <input id="{uid}-pass" type="password" bind:value={password} autocomplete="new-password" spellcheck="false" maxlength="500" onkeydown={onKey} />
      </div>
      <div class="field">
        <label for="{uid}-auth">{SOURCES_TEXT.mongoAuth}</label>
        <input id="{uid}-auth" type="text" bind:value={authSource} oninput={edited} autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="120" onkeydown={onKey} />
      </div>
    </div>
    <div class="actions"><button type="button" class="primary-button" disabled={busy} onclick={() => void test()}>{busy ? SOURCES_TEXT.mongoTesting : SOURCES_TEXT.mongoTest}</button></div>
    <p class="line" class:bad={failed} id="{uid}-line" role="status" hidden={line === ''}>{line}</p>
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
        <ul class="dbs">
          {#each connection.databases as database (database)}
            {@const rowId = uid + '-' + connection.id + '-' + database.replace(/[^A-Za-z0-9_-]/g, '_')}
            <li class="db">
              <label for="{rowId}-class" class="db-name">{database}<span class="visually-hidden">: {SOURCES_TEXT.classLabel}</span></label>
              <select id="{rowId}-class" value={valueOf(connection.id, database)} aria-describedby={problems[connection.id + '/' + database] ? rowId + '-problem' : undefined}
                aria-invalid={problems[connection.id + '/' + database] ? true : undefined} onchange={(event) => choose(connection.id, database, event.currentTarget.value)}>
                {#each CLASS_OPTIONS as option (option.value)}<option value={option.value}>{option.label}</option>{/each}
              </select>
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
  .field input, .field select, .db select { box-sizing: border-box; width: 100%; max-width: 100%; min-height: 44px; padding: 8px 10px; color: var(--text); background: var(--bg); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: var(--fs-4); }
  .field input:focus-visible, .field select:focus-visible, .db select:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .field input[aria-invalid='true'], .db select[aria-invalid='true'] { border-color: var(--bad); }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .line { margin: 0; color: var(--text-2); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .line.bad { color: var(--text); }
  .line[hidden], .inline-error[hidden] { display: none; }
  .conn { display: grid; gap: var(--space-2); min-width: 0; padding: var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .conn-top { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--space-2); }
  .conn-top h5 { margin: 0; font-size: var(--fs-4); font-weight: 600; overflow-wrap: anywhere; }
  .dbs { display: grid; gap: var(--space-2); margin: 0; padding: 0; list-style: none; }
  .db { display: grid; grid-template-columns: minmax(0, 1fr) minmax(160px, 240px); align-items: center; gap: var(--space-3); }
  .db-name { font-size: var(--fs-3); overflow-wrap: anywhere; }
  .db .inline-error { grid-column: 1 / -1; margin: 0; }
  @media (max-width: 520px) { .db { grid-template-columns: minmax(0, 1fr); } }
</style>
