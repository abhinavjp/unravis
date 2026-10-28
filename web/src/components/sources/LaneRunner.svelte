<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../lib/api';
  import { DEFAULT_COLLECTION } from '../../lib/wizardConnections';
  import {
    overlapProblem, runMongoLane, runNotice, runProblem, runSqlLane, utcInput, utcWindow, type LaneDay, type LaneRunResult, type LaneWindow
  } from '../../lib/lanes';
  import { RUN_TEXT, DAY_TEXT } from '../../lib/copy/sourcesCopy';
  import type { RecentConnection, SavedSqlConnection } from '../../lib/types';
  import './sources.css';

  // Reads one day from a saved SQL Server or MongoDB connection and adds it to the case. Read-only: the server runs fixed checks and nothing typed
  // here is ever run as a query. A password typed for this run goes out once and is dropped. Times are UTC and read strictly. A day that overlaps
  // one already added (for the same server and database) is refused here and again by the server, with nothing read.
  let {
    caseId, kind, days = [], database = '', incident = null, reference = null, lockDatabase = false, connectionLabel = '', runLabel = '', primary = false, ownNotice = false, onRan, onGone
  }: {
    caseId: string; kind: 'sql' | 'mongo'; days?: LaneDay[]; database?: string; incident?: LaneWindow | null; reference?: LaneWindow | null;
    lockDatabase?: boolean; connectionLabel?: string; runLabel?: string; primary?: boolean; ownNotice?: boolean; onRan?: (result: LaneRunResult) => void; onGone?: () => void;
  } = $props();

  const uid = $props.id();
  let sqlKept = $state.raw<SavedSqlConnection[]>([]);
  let mongoKept = $state.raw<RecentConnection[]>([]);
  let loaded = $state(false);
  let pickId = $state('');
  let password = $state('');
  let db = $state('');
  let collection = $state(DEFAULT_COLLECTION);
  let incStart = $state('');
  let incEnd = $state('');
  let refStart = $state('');
  let refEnd = $state('');
  let busy = $state(false);
  let problem = $state('');
  let done = $state('');

  const sqlPicked = $derived(sqlKept.find(item => item.id === pickId) ?? null);
  const mongoPicked = $derived(mongoKept.find(item => item.id === pickId) ?? null);
  const options = $derived(kind === 'sql' ? sqlKept.map(i => ({ id: i.id, label: i.label || i.server })) : mongoKept.map(i => ({ id: i.id, label: i.label })));
  const needsPassword = $derived(
    kind === 'sql' ? sqlPicked !== null && sqlPicked.auth === 'sql' && !sqlPicked.hasPassword
      : mongoPicked !== null && mongoPicked.username !== '' && !mongoPicked.hasCredentials
  );

  onMount(() => {
    db = database; incStart = incident ? utcInput(incident.start) : ''; incEnd = incident ? utcInput(incident.end) : '';
    refStart = reference ? utcInput(reference.start) : ''; refEnd = reference ? utcInput(reference.end) : '';
    const mine = new AbortController();
    void (async () => {
      const [a, b] = await Promise.allSettled([kind === 'sql' ? api.sqlConnections() : Promise.resolve([]), kind === 'mongo' ? api.recentConnections() : Promise.resolve([])]);
      if (mine.signal.aborted) return;
      if (a.status === 'fulfilled' && Array.isArray(a.value)) sqlKept = a.value as SavedSqlConnection[];
      if (b.status === 'fulfilled' && Array.isArray(b.value)) mongoKept = b.value as RecentConnection[];
      const want = connectionLabel;
      const first = kind === 'sql' ? sqlKept.find(i => (i.label || i.server) === want) ?? (sqlKept.length === 1 ? sqlKept[0] : undefined) : mongoKept.length === 1 ? mongoKept[0] : undefined;
      if (first) pickId = first.id;
      if (mongoPicked && mongoPicked.collection) collection = mongoPicked.collection;
      loaded = true;
    })();
    return () => mine.abort();
  });

  /** The server name a day is held under: the connection's name for SQL, and the fixed word MongoDB for MongoDB. */
  const heldLabel = (): string => (kind === 'sql' ? (sqlPicked ? sqlPicked.label || sqlPicked.server : '') : 'MongoDB');

  async function run() {
    if (busy) return;
    done = ''; problem = '';
    if (pickId === '' || (kind === 'sql' ? sqlPicked === null : mongoPicked === null)) { problem = RUN_TEXT.needPick; return; }
    if (db.trim() === '') { problem = RUN_TEXT.needDatabase; return; }
    const inc = utcWindow(incStart, incEnd);
    if (!inc) { problem = RUN_TEXT.needIncident; return; }
    const ref = utcWindow(refStart, refEnd);
    if (!ref) { problem = RUN_TEXT.needReference; return; }
    if (overlapProblem(days, kind, heldLabel(), db.trim(), inc)) { problem = RUN_TEXT.overlap; return; }
    busy = true;
    const windows = { incident: inc, reference: ref };
    try {
      let result: LaneRunResult;
      if (kind === 'sql') {
        result = await runSqlLane(caseId, { savedId: pickId, ...(password !== '' ? { password } : {}), database: db.trim() }, windows);
      } else {
        const item = mongoPicked!;
        result = await runMongoLane(caseId, {
          savedConnectionId: item.id, uri: item.uri, username: item.username, password, authSource: item.authSource, tlsMode: item.tlsMode,
          caCertificate: item.caCertificate, database: db.trim(), collection: collection.trim() || DEFAULT_COLLECTION
        }, windows);
      }
      password = '';
      // ownNotice: the page that mounts this runner says what happened itself (it may remove the runner when the day list changes).
      if (!ownNotice) {
        const said = runNotice(result);
        if (said.good) done = said.text; else problem = said.text;
      }
      onRan?.(result);
    } catch (error) {
      const failure = runProblem(error);
      if (failure.gone) onGone?.();
      problem = failure.text;
      password = '';
    } finally { busy = false; }
  }
</script>

<section class="src panel" aria-labelledby="{uid}-head">
  <h4 id="{uid}-head">{RUN_TEXT.heading}</h4>
  {#if loaded && options.length === 0}
    <p class="say" data-none>{RUN_TEXT.pickNone}</p>
  {:else}
    <div class="field">
      <label for="{uid}-pick">{RUN_TEXT.pick}</label>
      <select id="{uid}-pick" value={pickId} onchange={(e) => { pickId = e.currentTarget.value; password = ''; }}>
        <option value="">{RUN_TEXT.pickChoose}</option>
        {#each options as item (item.id)}<option value={item.id}>{item.label}</option>{/each}
      </select>
    </div>
    {#if needsPassword}
      <div class="field">
        <label for="{uid}-pw">{RUN_TEXT.password}</label>
        <input id="{uid}-pw" type="password" autocomplete="off" maxlength="512" bind:value={password} />
        <p class="hint">{RUN_TEXT.passwordHint}</p>
      </div>
    {/if}
  {/if}
  <div class="pair">
    <div class="field">
      <label for="{uid}-db">{RUN_TEXT.database}</label>
      <input id="{uid}-db" type="text" bind:value={db} maxlength="200" readonly={lockDatabase} autocomplete="off" autocapitalize="off" spellcheck="false" />
      <p class="hint">{RUN_TEXT.databaseHint}</p>
    </div>
    {#if kind === 'mongo'}
      <div class="field">
        <label for="{uid}-col">{RUN_TEXT.collection}</label>
        <input id="{uid}-col" type="text" bind:value={collection} maxlength="200" autocomplete="off" autocapitalize="off" spellcheck="false" />
      </div>
    {/if}
  </div>
  <div class="pair">
    <div class="field"><label for="{uid}-is">{RUN_TEXT.incidentStart}</label><input id="{uid}-is" type="text" placeholder="2026-10-26 10:30" bind:value={incStart} autocomplete="off" /></div>
    <div class="field"><label for="{uid}-ie">{RUN_TEXT.incidentEnd}</label><input id="{uid}-ie" type="text" placeholder="2026-10-26 11:00" bind:value={incEnd} autocomplete="off" /></div>
    <div class="field"><label for="{uid}-rs">{RUN_TEXT.referenceStart}</label><input id="{uid}-rs" type="text" placeholder="2026-10-26 09:30" bind:value={refStart} autocomplete="off" /></div>
    <div class="field"><label for="{uid}-re">{RUN_TEXT.referenceEnd}</label><input id="{uid}-re" type="text" placeholder="2026-10-26 10:30" bind:value={refEnd} autocomplete="off" /></div>
  </div>
  <p class="hint">{RUN_TEXT.timesHint}</p>
  <p class="hint">{DAY_TEXT.checkAgain}</p>
  <div class="actions">
    <button type="button" class={primary ? 'btn-primary' : 'btn'} disabled={busy} onclick={() => void run()}>{busy ? RUN_TEXT.running : (runLabel || RUN_TEXT.run)}</button>
  </div>
  <div aria-live="polite">
    {#if problem}<p class="say bad" role="alert" data-problem>{problem}</p>{/if}
    {#if done}<p class="say good" role="status">{done}</p>{/if}
  </div>
</section>
