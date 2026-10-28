<script lang="ts">
  import { onMount } from 'svelte';
  import OrphanFiles from './OrphanFiles.svelte';
  import { api, ApiError } from '../api';
  import { CaseApiError, isCaseGone } from '../cases';
  import { fetchHealthRuns, runHealth, sortedFindings, countByStatus, type HealthRun, type HealthReport, type HealthTarget } from '../health';
  import { HEALTH_TEXT, HEALTH_STATUS_LABEL, HEALTH_STATUS_MARK, overallText, connectedText } from '../copy/healthCopy';
  import { DEFAULT_COLLECTION } from '../wizardConnections';
  import type { RecentConnection, SavedSqlConnection, SqlTestRequest } from '../types';
  import { app } from '../appState.svelte';
  import { onConnectionsChanged } from '../../components/sources/connectionMemory';

  // Read-only checks of a SQL Server or MongoDB the case depends on. Nothing here runs by itself: a check starts only on "Run checks".
  // The server writes the checks; nothing typed here is ever run as a query. A password typed for a run is sent once and not kept.
  // onRan tells the page around it that a run was made, so a list built from the case's runs (the case story) can read them again.
  let { caseId, onGone, onRan }: { caseId: string; onGone?: () => void; onRan?: () => void } = $props();

  let sqlKept = $state.raw<SavedSqlConnection[]>([]);
  let mongoKept = $state.raw<RecentConnection[]>([]);
  let runs = $state.raw<HealthRun[]>([]);
  let loaded = $state(false);

  let kind = $state<'sql' | 'mongo'>('sql');
  let sqlId = $state('');
  let sqlPassword = $state('');
  let mongoId = $state('');
  let mongoPassword = $state('');
  let database = $state('');
  let collection = $state(DEFAULT_COLLECTION);
  let databases = $state.raw<string[]>([]);
  let typed = $state(false);

  let busy = $state<'' | 'test' | 'run'>('');
  let testNote = $state('');
  let problem = $state('');
  let shown = $state.raw<HealthRun | null>(null);
  let openId = $state(0);
  let controller: AbortController | null = null;

  const sqlPicked = $derived(sqlKept.find(item => item.id === sqlId) ?? null);
  const mongoPicked = $derived(mongoKept.find(item => item.id === mongoId) ?? null);
  const picked = $derived(kind === 'sql' ? sqlPicked !== null : mongoPicked !== null);
  const needsSqlPassword = $derived(sqlPicked !== null && sqlPicked.auth === 'sql' && !sqlPicked.hasPassword);
  const needsMongoPassword = $derived(mongoPicked !== null && !mongoPicked.hasCredentials && mongoPicked.username !== '');
  const report = $derived<HealthReport | null>(shown?.report ?? null);
  const demo = $derived(app.info?.demo === true);

  onMount(() => {
    const mine = new AbortController();
    controller = mine;
    void (async () => {
      const [sql, mongo, history] = await Promise.allSettled([api.sqlConnections(), api.recentConnections(), fetchHealthRuns(caseId, mine.signal)]);
      if (mine.signal.aborted) return;
      if (sql.status === 'fulfilled' && Array.isArray(sql.value)) sqlKept = sql.value;
      if (mongo.status === 'fulfilled' && Array.isArray(mongo.value)) mongoKept = mongo.value;
      if (history.status === 'fulfilled') runs = history.value;
      else if (isCaseGone(history.reason)) onGone?.();
      if (sqlKept.length === 0 && mongoKept.length > 0) kind = 'mongo';
      loaded = true;
    })();
    const stop = onConnectionsChanged(() => { void reloadKept(mine.signal); });
    return () => { stop(); mine.abort(); controller?.abort(); };
  });

  // A connection added, saved or forgotten on the connections page: read both lists again, and let go of a pick that no longer exists.
  async function reloadKept(signal: AbortSignal) {
    const [sql, mongo] = await Promise.allSettled([api.sqlConnections(), api.recentConnections()]);
    if (signal.aborted) return;
    if (sql.status === 'fulfilled' && Array.isArray(sql.value)) {
      sqlKept = sql.value;
      if (sqlId !== '' && !sqlKept.some(item => item.id === sqlId)) { sqlId = ''; sqlPassword = ''; reset(); }
    }
    if (mongo.status === 'fulfilled' && Array.isArray(mongo.value)) {
      mongoKept = mongo.value;
      if (mongoId !== '' && !mongoKept.some(item => item.id === mongoId)) { mongoId = ''; mongoPassword = ''; reset(); }
    }
  }

  function reset() { databases = []; database = ''; typed = false; testNote = ''; problem = ''; }
  function setKind(next: 'sql' | 'mongo') { if (next !== kind) { kind = next; reset(); } }
  function pickSql(id: string) { sqlId = id; sqlPassword = ''; reset(); }
  function pickMongo(id: string) {
    mongoId = id; mongoPassword = ''; reset();
    const found = mongoKept.find(item => item.id === id);
    collection = found && found.collection ? found.collection : DEFAULT_COLLECTION;
  }

  function sqlRequest(): SqlTestRequest {
    return { savedId: sqlId, ...(sqlPassword !== '' ? { password: sqlPassword } : {}), database: database.trim() };
  }
  function mongoRequest() {
    const item = mongoPicked!;
    return {
      savedConnectionId: item.id, uri: item.uri, username: item.username, password: mongoPassword, authSource: item.authSource,
      tlsMode: item.tlsMode, caCertificate: item.caCertificate
    };
  }

  function failure(error: unknown): string {
    if (isCaseGone(error)) onGone?.();
    const status = error instanceof CaseApiError || error instanceof ApiError ? error.status : 0;
    if (status === 409) return HEALTH_TEXT.busy;
    if (status === 429) return HEALTH_TEXT.limited;
    if (status === 423) return HEALTH_TEXT.locked;
    if (status === 503) return HEALTH_TEXT.demo;
    if (status === 400 && error instanceof Error && error.message) return error.message;
    return HEALTH_TEXT.failed;
  }

  async function test() {
    if (busy || !picked) return;
    busy = 'test'; problem = ''; testNote = '';
    try {
      if (kind === 'sql') {
        const result = await api.testSqlConnection({ ...sqlRequest(), database: '' });
        databases = result.databases ?? [];
        typed = databases.length === 0;
        testNote = result.databases.length > 0 ? `${connectedText(result.version, result.edition)} ${result.databases.length} databases found.` : HEALTH_TEXT.noDatabases;
      } else {
        const result = await api.testConnection(mongoRequest());
        databases = result.databases ?? [];
        typed = databases.length === 0;
        testNote = databases.length > 0 ? `${HEALTH_TEXT.connectedTo} ${databases.length} databases found.` : HEALTH_TEXT.noDatabases;
      }
    } catch (error) {
      databases = []; typed = false;
      problem = error instanceof Error && error.message ? error.message : HEALTH_TEXT.failed;
    } finally { busy = ''; }
  }

  async function run() {
    if (busy) return;
    if (!picked) { problem = HEALTH_TEXT.needPick; return; }
    if (kind === 'mongo' && database.trim() === '') { problem = HEALTH_TEXT.needDatabase; return; }
    busy = 'run'; problem = '';
    const target: HealthTarget = kind === 'sql'
      ? { kind: 'sql', sql: sqlRequest() }
      : { kind: 'mongo', mongo: { ...mongoRequest(), database: database.trim(), collection: collection.trim() || DEFAULT_COLLECTION } };
    try {
      const result = await runHealth(caseId, target);
      shown = result.run; openId = result.run.id;
      runs = [result.run, ...runs.filter(item => item.id !== result.run.id)].slice(0, 20);
      if (!result.saved) problem = HEALTH_TEXT.notSaved + (result.saveError || HEALTH_TEXT.failed);
      else onRan?.();
    } catch (error) { problem = failure(error); }
    finally { busy = ''; }
  }

  function toggle(item: HealthRun) {
    if (openId === item.id) { openId = 0; return; }
    openId = item.id; shown = item;
  }

  const when = (iso: string): string => { const d = new Date(iso); return Number.isNaN(d.getTime()) ? iso : d.toLocaleString(); };
</script>

<section class="tools" aria-labelledby="health-heading">
  <h3 id="health-heading">{HEALTH_TEXT.heading}</h3>
  <p class="note">{HEALTH_TEXT.intro}</p>
  <p class="note">{HEALTH_TEXT.clickNote}</p>

  <div class="tabs" role="group" aria-label={HEALTH_TEXT.heading}>
    <button type="button" class="secondary-button" aria-pressed={kind === 'sql'} onclick={() => setKind('sql')}>{HEALTH_TEXT.sqlHeading}</button>
    <button type="button" class="secondary-button" aria-pressed={kind === 'mongo'} onclick={() => setKind('mongo')}>{HEALTH_TEXT.mongoHeading}</button>
  </div>

  {#if kind === 'sql'}
    {#if loaded && sqlKept.length === 0}
      <p class="empty">{HEALTH_TEXT.sqlNone}</p>
    {:else}
      <label class="field"><span>{HEALTH_TEXT.sqlPick}</span>
        <select value={sqlId} onchange={(e) => pickSql(e.currentTarget.value)}>
          <option value="">–</option>
          {#each sqlKept as item (item.id)}<option value={item.id}>{item.label}</option>{/each}
        </select>
      </label>
      {#if needsSqlPassword}
        <label class="field"><span>{HEALTH_TEXT.sqlPassword}</span>
          <input type="password" autocomplete="off" bind:value={sqlPassword} />
          <small>{HEALTH_TEXT.sqlPasswordHint}</small>
        </label>
      {/if}
    {/if}
  {:else}
    {#if loaded && mongoKept.length === 0}
      <p class="empty">{HEALTH_TEXT.mongoNone}</p>
    {:else}
      <label class="field"><span>{HEALTH_TEXT.mongoPick}</span>
        <select value={mongoId} onchange={(e) => pickMongo(e.currentTarget.value)}>
          <option value="">–</option>
          {#each mongoKept as item (item.id)}<option value={item.id}>{item.label}</option>{/each}
        </select>
      </label>
      {#if needsMongoPassword}
        <label class="field"><span>{HEALTH_TEXT.mongoPassword}</span>
          <input type="password" autocomplete="off" bind:value={mongoPassword} />
          <small>{HEALTH_TEXT.sqlPasswordHint}</small>
        </label>
      {/if}
    {/if}
  {/if}

  {#if picked}
    <div class="actions"><button type="button" class="secondary-button" disabled={busy !== ''} onclick={test}>{busy === 'test' ? HEALTH_TEXT.testing : HEALTH_TEXT.test}</button></div>
    <div aria-live="polite">{#if testNote}<p class="line">{testNote}</p>{/if}</div>

    <label class="field"><span>{kind === 'sql' ? HEALTH_TEXT.sqlDatabase : HEALTH_TEXT.mongoDatabase}</span>
      {#if databases.length > 0 && !typed}
        <select bind:value={database}>
          <option value="">{kind === 'sql' ? HEALTH_TEXT.sqlDatabaseAny : HEALTH_TEXT.mongoDatabaseChoose}</option>
          {#each databases as name (name)}<option value={name}>{name}</option>{/each}
        </select>
      {:else}
        <input type="text" autocomplete="off" placeholder={HEALTH_TEXT.typeDatabase} bind:value={database} />
      {/if}
    </label>
    {#if kind === 'mongo'}
      <label class="field"><span>{HEALTH_TEXT.mongoCollection}</span><input type="text" autocomplete="off" bind:value={collection} /></label>
    {/if}
  {/if}

  <div class="actions"><button type="button" class="primary-button" disabled={busy !== '' || !picked || demo} onclick={run}>{busy === 'run' ? HEALTH_TEXT.running : HEALTH_TEXT.run}</button>{#if demo}<span class="note" data-demo>{HEALTH_TEXT.demo}</span>{/if}</div>
  <div aria-live="polite">{#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}</div>

  {#if report}
    {@const counts = countByStatus(report)}
    <h4>{HEALTH_TEXT.resultHeading}</h4>
    <p class="line">{report.label}{report.database ? ` · ${report.database}` : ''}{report.version ? ` · ${report.version}` : ''}</p>
    <p class="note">{overallText(report.overall, counts)}</p>
    <ul class="findings">
      {#each sortedFindings(report) as finding (finding.id)}
        <li class={finding.status}>
          <div class="head"><span class="chip {finding.status}"><span aria-hidden="true">{HEALTH_STATUS_MARK[finding.status]}</span> {HEALTH_STATUS_LABEL[finding.status]}</span><strong>{finding.title}</strong></div>
          <p class="sum">{finding.summary}</p>
          {#if finding.why}<p class="why"><span>{HEALTH_TEXT.why}:</span> {finding.why}</p>{/if}
          {#if finding.columns.length > 0 && finding.rows.length > 0}
            <details>
              <summary>{HEALTH_TEXT.rowsHeading} ({finding.rows.length})</summary>
              <div class="table-wrap">
                <table>
                  <thead><tr>{#each finding.columns as column, i (i)}<th scope="col">{column}</th>{/each}</tr></thead>
                  <tbody>{#each finding.rows as row, r (r)}<tr>{#each row as cell, c (c)}<td>{cell}</td>{/each}</tr>{/each}</tbody>
                </table>
              </div>
            </details>
          {/if}
        </li>
      {/each}
    </ul>
    {#if report.kind === 'sql'}<p class="note">{HEALTH_TEXT.cumulative}</p>{/if}
  {/if}

  <h4>{HEALTH_TEXT.historyHeading}</h4>
  {#if runs.length === 0}
    <p class="empty">{HEALTH_TEXT.historyNone}</p>
  {:else}
    <ul class="history">
      {#each runs as item (item.id)}
        <li>
          <span class="chip {item.status}"><span aria-hidden="true">{HEALTH_STATUS_MARK[item.status]}</span> {HEALTH_STATUS_LABEL[item.status]}</span>
          <span class="when">{when(item.at)} · {item.label}</span>
          <button type="button" class="secondary-button" aria-expanded={openId === item.id} onclick={() => toggle(item)}>{openId === item.id ? HEALTH_TEXT.historyClose : HEALTH_TEXT.historyOpen}</button>
        </li>
      {/each}
    </ul>
  {/if}
  <OrphanFiles {caseId} />
</section>

<style>
  .tools { display: grid; gap: var(--space-2); min-width: 0; padding-top: var(--space-4); border-top: 1px solid var(--hairline); }
  h3 { margin: 0; font-size: var(--fs-4); font-weight: 600; letter-spacing: -.01em; }
  h4 { margin: var(--space-2) 0 0; color: var(--text-2); font-size: var(--fs-2); font-weight: 500; }
  .note { margin: 0; color: var(--text-2); font-size: var(--fs-2); line-height: 1.5; }
  .line { margin: 0; font-size: var(--fs-3); font-weight: 600; overflow-wrap: anywhere; }
  .empty { margin: 0; padding: 8px 0; border-top: 1px solid var(--line); font-size: var(--fs-3); }
  .actions, .tabs { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .tabs button[aria-pressed='true'] { border-color: var(--accent, currentColor); font-weight: 600; }
  .field { display: grid; gap: 4px; min-width: 0; font-size: var(--fs-2); color: var(--text-2); }
  .field select, .field input { min-width: 0; max-width: 28rem; }
  .field small { font-size: var(--fs-2); color: var(--text-2); }
  .findings, .history { display: grid; gap: 0; margin: 0; padding: 0; list-style: none; }
  .findings li { display: grid; gap: 4px; min-width: 0; padding: 10px 0 10px 10px; border-top: 1px solid var(--line-soft); border-left: 3px solid var(--line); }
  .findings li.bad { border-left-color: var(--bad); }
  .findings li.warn { border-left-color: var(--warn); }
  .findings li.note { border-left-color: var(--accent); }
  .head { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; min-width: 0; }
  .head strong { font-size: var(--fs-3); overflow-wrap: anywhere; }
  .chip { display: inline-flex; gap: 4px; padding: 1px 8px; border: 1px solid var(--line); border-radius: 999px; font-size: var(--fs-2); white-space: nowrap; }
  .chip.bad { border-color: var(--bad); }
  .chip.warn { border-color: var(--warn); }
  .sum, .why { margin: 0; font-size: var(--fs-3); line-height: 1.5; overflow-wrap: anywhere; }
  .why { color: var(--text-2); }
  .why span { font-weight: 500; }
  .history li { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; padding: 6px 0; border-top: 1px solid var(--line-soft); min-width: 0; }
  .when { flex: 1 1 12rem; font-size: var(--fs-2); color: var(--text-2); overflow-wrap: anywhere; }
  details summary { width: fit-content; color: var(--text-2); font-size: var(--fs-2); cursor: pointer; }
  .table-wrap { overflow-x: auto; }
  table { width: 100%; border-collapse: collapse; font-size: var(--fs-2); }
  th, td { padding: 6px 8px; text-align: left; border-bottom: 1px solid var(--line-soft); vertical-align: top; overflow-wrap: anywhere; }
  th { color: var(--text-2); font-weight: 500; border-bottom-color: var(--line); }
</style>
