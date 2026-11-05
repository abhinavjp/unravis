<script lang="ts">
  import { api, ApiError } from '../../lib/api';
  import { addDatabaseSource, conn } from '../../lib/appState.svelte';
  import { CaseApiError } from '../../lib/cases';
  import { postSourceTag } from '../../lib/caseTagsApi';
  import { AUDIT_ADD_TEXT as T } from '../../lib/copy/sourcesCopy';
  import { suggestionLine } from '../../lib/copy/wizardCreateCopy';
  import { safeServerLine } from '../../lib/copy/wizardSourcesCopy';
  import { classFromTicks, looksLikeAuditDatabase } from '../../lib/wizardSources';
  import { auditReady, cleanDatabases, collectionProblem, passwordForTest } from '../../lib/auditGap';
  import { AUDIT_GAP_TEXT as G, COLLECTION_PROBLEM_TEXT, readyHintText } from '../../lib/copy/auditGapCopy';
  import type { ConnectionInput, RecentConnection, TLSMode } from '../../lib/types';
  import './sources.css';

  // Add data > MongoDB on a case that already exists (screen 112): pick a saved connection, enter its password when one is not kept, pick the
  // database and the audit collection, tick what the database is for, and add it. An audit database is connected the way the Connections drawer
  // connects one (addDatabaseSource, which takes the same steps as its Add button) and then tagged to this case, so the timeline and the guided
  // steps read it. The password lives in this component's memory only. The Step 8 profiler is a separate screen, reached by the link at the foot.
  let { caseId, readOnly = false, onProfiler }: { caseId: string; readOnly?: boolean; onProfiler?: () => void } = $props();
  const uid = $props.id();
  const DEFAULT_COLLECTION = 'ApiAuditLog';
  const RETEST_AFTER_MS = 20 * 60 * 1000;

  let savedId = $state('');
  let password = $state('');
  let collection = $state(DEFAULT_COLLECTION);
  let databases = $state.raw<string[]>([]);
  let testing = $state(false);
  let tested = $state(false);
  let testProblem = $state('');
  // What the last test said besides the list: the server's own warning, and whether the list was cut.
  let testWarning = $state('');
  let listCut = $state(false);
  let database = $state('');
  let auditTick = $state(false);
  let appTick = $state(false);
  let suggested = $state(false);
  // What the person ticked for each database, kept by name: a database picked again keeps their choice, so the name's default does not undo an untick.
  let decided = $state<Record<string, { audit: boolean; app: boolean }>>({});
  let busy = $state(false);
  let problem = $state('');
  // Where the add stands: connected says the source exists on the server; tagged says the case was told. A failed tag keeps connected true, so the retry only tags.
  let connected = $state(false);
  let tagged = $state(false);
  let result = $state('');
  // The tested connection, kept for the moment of the add. Not reactive, never shown, never stored.
  let held: ConnectionInput | null = null;
  let testedAt = 0;
  // Counts the tests and the resets; a test answer from before the last one is ignored.
  let testGen = 0;

  const recent = $derived<RecentConnection | undefined>(conn.recentConnections.find((r) => r.id === savedId));
  const hasSavedPassword = $derived(!!recent?.hasCredentials);
  const chosenClass = $derived(classFromTicks(auditTick, appTick));
  const colProblem = $derived(collectionProblem(collection));
  const ready = $derived(auditReady({ testing, tested, testProblem: testProblem !== '', database, chosen: chosenClass, collectionProblem: colProblem, connected, tagged, addProblem: problem !== '' }));
  const canAdd = $derived(!readOnly && !busy && !testing && tested && database !== '' && chosenClass !== 'unused' && (chosenClass !== 'audit' || connected || colProblem === ''));

  function inputOf(r: RecentConnection): ConnectionInput {
    const tlsMode: TLSMode = r.tlsMode || 'uri';
    return { uri: r.uri, username: r.username, password, authSource: r.authSource, tlsMode, caCertificate: tlsMode === 'custom' ? r.caCertificate : '', savedConnectionId: r.id };
  }
  const secretsOf = (input: ConnectionInput): string[] => [input.uri, input.username, input.password, input.caCertificate].map((s) => s.trim()).filter((s) => s !== '');
  const wording = (cause: unknown, input: ConnectionInput): string => {
    if (cause instanceof CaseApiError && cause.reason === 'case-closed') return T.closedCase;
    return safeServerLine(cause instanceof Error ? cause.message : '', secretsOf(input));
  };
  // A wrong or refused collection names the collections the server read in the database: they follow the words, so the person can pick one.
  const withSuggestions = (text: string, cause: unknown): string => {
    const line = suggestionLine(cause instanceof ApiError ? cause.collections : undefined);
    return line === '' ? text : text + ' ' + line;
  };

  function resetFrom(id: string) {
    testGen++; testing = false;
    savedId = id; password = ''; held = null;
    databases = []; tested = false; testProblem = ''; testWarning = ''; listCut = false; database = ''; auditTick = false; appTick = false; suggested = false; decided = {};
    problem = ''; connected = false; tagged = false; result = '';
    const r = conn.recentConnections.find((item) => item.id === id);
    collection = r?.collection || DEFAULT_COLLECTION;
  }

  async function test() {
    if (!recent || testing) return;
    const id = recent.id;
    const input = inputOf(recent);
    // The password typed for the last test is not shown again; an empty field means the same connection, so it is reused.
    input.password = passwordForTest(input.password, held, id);
    const gen = ++testGen;
    testing = true; testProblem = ''; testWarning = ''; listCut = false; problem = ''; result = '';
    try {
      const out = await api.testConnection(input);
      if (gen !== testGen || savedId !== id) return;
      const { list, cut } = cleanDatabases(out?.databases);
      databases = list; listCut = cut; held = input; tested = true; testedAt = Date.now(); password = '';
      testWarning = typeof out?.warning === 'string' && out.warning.trim() !== '' ? safeServerLine(out.warning, secretsOf(input)) : '';
      if (database !== '' && !list.includes(database)) { database = ''; auditTick = false; appTick = false; }
      if (list.length === 0) testProblem = T.noDatabases;
    } catch (cause) {
      if (gen !== testGen || savedId !== id) return;
      tested = false; databases = []; held = null;
      testProblem = wording(cause, input);
    } finally { if (gen === testGen) testing = false; }
  }

  function pick(name: string) {
    // Once the database is connected the pick is fixed; changing it would connect a second source on a retry.
    if (connected) return;
    database = name; problem = ''; result = ''; connected = false; tagged = false;
    // A database the person already decided on gets their ticks back, not the name's default.
    const before = name !== '' ? decided[name] : undefined;
    if (before) { auditTick = before.audit; appTick = before.app; suggested = false; return; }
    // An audit-looking name starts ticked as audit logs; any other database starts with nothing ticked.
    const looks = name !== '' && looksLikeAuditDatabase(name);
    auditTick = looks; appTick = false; suggested = looks;
  }
  function tick(which: 'audit' | 'app', on: boolean) {
    if (which === 'audit') { auditTick = on; suggested = false; } else appTick = on;
    if (database !== '') decided = { ...decided, [database]: { audit: auditTick, app: appTick } };
    problem = '';
  }

  async function tag(cls: 'audit' | 'other', h: ConnectionInput | null, id: string) {
    if (!h) throw new CaseApiError('That connection is no longer held. Test it again.', 422, 'connection-lost');
    // The server accepts a database for a case only after a connection test in the last 30 minutes, so a retry tests again first.
    if (Date.now() - testedAt > RETEST_AFTER_MS) { await api.testConnection(h); testedAt = Date.now(); }
    // `id` is the case as it was when Add was pressed, not the prop: a case switch during the retest must not move the tag.
    await postSourceTag(id, { importId: 0, kind: 'mongo-database', label: database, env: '', portal: '', api: '', classification: cls });
  }

  async function add() {
    // The case, the connection and the label as they were when Add was pressed; a case switch or a later test cannot swap them.
    const id = caseId;
    const h = held;
    if (!canAdd || !h || !recent) return;
    const label = recent.label;
    const cls = chosenClass === 'audit' ? 'audit' : 'other';
    busy = true; problem = ''; result = '';
    try {
      if (cls === 'audit' && !connected) {
        try {
          await addDatabaseSource(h, { label: database, connectionLabel: label, database, collection: collection.trim() || DEFAULT_COLLECTION });
          connected = true;
        } catch (cause) { problem = withSuggestions(wording(cause, h), cause); return; }
      }
      try {
        await tag(cls, h, id);
        tagged = true;
        result = cls === 'audit' ? T.addedAudit(database) : T.addedApp(database);
        problem = '';
      } catch (cause) {
        problem = connected ? T.connectedNotTagged(database) + ' ' + wording(cause, h) : wording(cause, h);
      }
    } finally { busy = false; }
  }

  function another() {
    // Keeps the tested connection and its list of databases; only the pick is cleared.
    database = ''; auditTick = false; appTick = false; suggested = false; problem = ''; result = ''; connected = false; tagged = false;
  }
</script>

<section class="src" aria-labelledby="{uid}-head" data-audit-add>
  <div class="nutshell"><b id="{uid}-head">{T.heading}</b><p>{T.lead}</p></div>
  {#if readOnly}<p class="hint" data-read-only>{T.closedCase}</p>{/if}

  {#if conn.recentConnections.length === 0}
    <div class="empty">
      <p>{T.noSaved}</p>
    </div>
  {:else}
    <div class="panel">
      <div class="field">
        <label for="{uid}-saved">{T.savedLabel}</label>
        <select id="{uid}-saved" value={savedId} disabled={readOnly || busy || testing} onchange={(e) => resetFrom((e.currentTarget as HTMLSelectElement).value)}>
          <option value="">{T.savedNone}</option>
          {#each conn.recentConnections as r (r.id)}<option value={r.id}>{r.label}</option>{/each}
        </select>
      </div>

      {#if recent}
        <div class="field">
          <label for="{uid}-pass">{T.passwordLabel}</label>
          <input id="{uid}-pass" type="password" bind:value={password} autocomplete="new-password" spellcheck="false" maxlength="500" disabled={readOnly || busy || testing}
            placeholder={tested ? T.passwordKept : ''} aria-describedby="{uid}-pass-note" />
          <p id="{uid}-pass-note" class="hint">{hasSavedPassword ? T.passwordSaved : T.passwordTyped}</p>
        </div>
        <div class="actions">
          <button type="button" class="btn" disabled={readOnly || busy || testing} onclick={test}>{testing ? T.testing : T.test}</button>
        </div>
        <p class="say {testProblem ? 'bad' : 'good'}" role="status" hidden={!testProblem && !tested}>{testProblem || (tested ? T.testedLine(databases.length) : '')}</p>
        {#if testWarning}<p class="hint" data-test-warning>{G.warningLead} {testWarning}</p>{/if}
        {#if listCut}<p class="hint" data-list-cut>{G.cutNote}</p>{/if}
      {/if}

      {#if tested && databases.length}
        <div class="pair">
          <div class="field">
            <label for="{uid}-db">{T.databaseLabel}</label>
            <select id="{uid}-db" value={database} disabled={readOnly || busy || connected} onchange={(e) => pick((e.currentTarget as HTMLSelectElement).value)}>
              <option value="">{T.databaseNone}</option>
              {#each databases as name (name)}<option value={name}>{name}</option>{/each}
            </select>
          </div>
          <div class="field">
            <label for="{uid}-col">{T.collectionLabel}</label>
            <input id="{uid}-col" type="text" bind:value={collection} maxlength="120" spellcheck="false" disabled={readOnly || busy || connected || tagged}
              aria-invalid={colProblem !== ''} aria-describedby="{uid}-col-note" />
            <p id="{uid}-col-note" class="hint" class:warn={colProblem !== ''} data-collection-check={colProblem === '' ? 'ok' : colProblem}>{colProblem === '' ? G.collectionOk + ' ' + G.collectionNote : COLLECTION_PROBLEM_TEXT[colProblem]}</p>
          </div>
        </div>

        {#if database !== ''}
          <fieldset class="uses" disabled={readOnly || busy || connected || tagged}>
            <label class="tick"><input type="checkbox" checked={auditTick} onchange={(e) => tick('audit', (e.currentTarget as HTMLInputElement).checked)} /> <span>{T.tickAudit}</span></label>
            <p class="hint">{T.tickAuditNote}</p>
            {#if suggested && auditTick}<p class="hint" data-suggested>{T.suggested}</p>{/if}
            <label class="tick"><input type="checkbox" checked={appTick} onchange={(e) => tick('app', (e.currentTarget as HTMLInputElement).checked)} /> <span>{T.tickApp}</span></label>
            <p class="hint">{T.tickAppNote}</p>
            {#if auditTick && appTick}<p class="hint">{T.bothTicked}</p>{/if}
            {#if !auditTick && !appTick}<p class="hint" data-neither>{T.neitherTicked}</p>{/if}
          </fieldset>
          <div class="actions">
            {#if tagged}
              <button type="button" class="btn" onclick={another}>{T.addAnother}</button>
            {:else}
              <button type="button" class="btn-primary" disabled={!canAdd} onclick={add}>{busy ? T.adding : connected ? T.tagAgain : T.add}</button>
            {/if}
          </div>
        {/if}
      {/if}

      {#if recent}
        <div class="readiness" data-testid="audit-ready" data-ready={ready.cls}>
          <span class="label">{G.readyLabel}</span>
          <span class="src-sticker {ready.cls}" data-testid="audit-ready-chip">{ready.word}</span>
          <span class="hint" data-testid="audit-ready-hint">{readyHintText(ready)}</span>
        </div>
      {/if}

      {#if problem}<p class="say bad" role="alert" data-add-problem>{problem}</p>{/if}
      {#if result}<p class="say good" role="status" data-add-done>{result}</p>{/if}
    </div>
  {/if}

  <div class="panel" data-profiler-route>
    <h4>{T.profilerHeading}</h4>
    <p class="hint">{T.profilerBody}</p>
    <div class="actions">
      <button type="button" class="btn" disabled={!onProfiler} onclick={() => onProfiler?.()}>{T.profilerOpen}</button>
      <button type="button" class="link" disabled={!onProfiler} data-testid="audit-check-permission" onclick={() => onProfiler?.()}>{G.permissionLink}</button>
    </div>
    <p class="hint">{G.permissionHelp}</p>
  </div>
</section>

<style>
  .uses { display: grid; gap: var(--space-2); margin: 0; padding: 0; border: 0; min-width: 0; }
  .readiness { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .pair { align-items: start; }
  .readiness .label { font-weight: 600; font-size: var(--fs-3); }
  .hint.warn { color: var(--text); font-weight: 600; }
</style>
