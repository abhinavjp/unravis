<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../lib/api';
  import {
    CONNECTION_LIMIT, canSaveAnother, mongoRow, sqlRow, type ConnectionRow, type ConnectionWord
  } from '../../lib/lanes';
  import { CONN_TEXT, NUTSHELL } from '../../lib/copy/sourcesCopy';
  import { connectedText } from '../../lib/copy/healthCopy';
  import { sqlTestInput, formFromSaved } from '../../lib/sqlConnection';
  import type { RecentConnection, SavedSqlConnection } from '../../lib/types';
  import SqlConnectionForm from './SqlConnectionForm.svelte';
  import StateSticker from './StateSticker.svelte';
  import './sources.css';

  // Screens 52 and 53: every saved connection in one list, SQL Server and MongoDB. Test says only Works, Failed or Not tested, and the last
  // test is kept in memory only (it is not a saved fact). A saved password is never shown or sent back: a row only says whether one is saved.
  // Forget asks once. At 20 SQL connections the server refuses the 21st (409); this page says so first and disables the add button.
  // primary: the one button this page may offer as the main action. It is outlined by default, so a case home that already has its own main
  // button keeps a single one; the guided shell passes primary where this page is the whole screen.
  let { onMongoAdd, primary = false }: { onMongoAdd?: () => void; primary?: boolean } = $props();

  let sql = $state.raw<SavedSqlConnection[]>([]);
  let mongo = $state.raw<RecentConnection[]>([]);
  let loaded = $state(false);
  let loadFailed = $state(false);
  let demo = $state(false);
  let tests = $state.raw<Record<string, { word: ConnectionWord; at: string; problem: string }>>({});
  let testing = $state('');
  let asking = $state('');
  let editing = $state<SavedSqlConnection | null>(null);
  let adding = $state(false);
  let note = $state('');

  const rows = $derived<ConnectionRow[]>([
    ...sql.map(item => sqlRow(item, tests['sql:' + item.id])),
    ...mongo.map(item => mongoRow(item, tests['mongo:' + item.id]))
  ]);
  const full = $derived(!canSaveAnother(sql.length));
  const pwText = (p: ConnectionRow['password']) => (p === 'saved' ? CONN_TEXT.pwSaved : p === 'none' ? CONN_TEXT.pwNone : CONN_TEXT.pwNotNeeded);

  async function load() {
    loadFailed = false;
    const [a, b] = await Promise.allSettled([api.sqlConnections(), api.recentConnections()]);
    if (a.status === 'fulfilled' && Array.isArray(a.value)) sql = a.value;
    if (b.status === 'fulfilled' && Array.isArray(b.value)) mongo = b.value;
    const refused = (r: PromiseSettledResult<unknown>) => r.status === 'rejected' && (r.reason as { status?: number } | null)?.status === 503;
    demo = refused(a) && refused(b);
    loadFailed = a.status === 'rejected' && b.status === 'rejected' && !demo;
    loaded = true;
  }
  onMount(() => { void load(); });

  const remember = (key: string, word: ConnectionWord, problem: string) => {
    tests = { ...tests, [key]: { word, at: new Date().toLocaleString(), problem } };
  };

  async function test(row: ConnectionRow) {
    if (testing) return;
    note = '';
    if (row.kind === 'sql') {
      const item = sql.find(s => s.id === row.id);
      if (!item) return;
      if (item.auth === 'sql' && !item.hasPassword) { remember(row.key, 'Failed', CONN_TEXT.testNeedsPassword); return; }
      testing = row.key;
      try {
        // The saved password is used on the server through savedId. Nothing is typed or held here.
        const result = await api.testSqlConnection({ ...sqlTestInput(formFromSaved(item)), savedId: item.id });
        remember(row.key, 'Works', connectedText(result.version, result.edition));
      } catch (error) {
        remember(row.key, 'Failed', error instanceof Error ? error.message : '');
      } finally { testing = ''; }
      return;
    }
    const item = mongo.find(m => m.id === row.id);
    if (!item) return;
    if (item.username !== '' && !item.hasCredentials) { remember(row.key, 'Failed', CONN_TEXT.testNeedsPassword); return; }
    testing = row.key;
    try {
      await api.testConnection({
        uri: item.uri, username: item.username, password: '', authSource: item.authSource, tlsMode: item.tlsMode, caCertificate: item.caCertificate,
        savedConnectionId: item.id
      });
      remember(row.key, 'Works', '');
    } catch (error) {
      remember(row.key, 'Failed', error instanceof Error ? error.message : '');
    } finally { testing = ''; }
  }

  async function forget(row: ConnectionRow) {
    asking = ''; note = '';
    try {
      if (row.kind === 'sql') await api.forgetSqlConnection(row.id); else await api.forgetConnection(row.id);
    } catch (error) { note = error instanceof Error ? error.message : CONN_TEXT.loadFailed; }
    await load();
  }

  function edit(row: ConnectionRow) {
    if (row.kind !== 'sql') return;
    editing = sql.find(s => s.id === row.id) ?? null;
    adding = editing !== null;
  }
  function saved() { adding = false; editing = null; void load(); }
</script>

<section class="src" aria-labelledby="conn-head">
  <div class="nutshell"><b>{NUTSHELL}</b><p>{CONN_TEXT.intro}</p></div>
  <h3 id="conn-head">{CONN_TEXT.heading}</h3>

  {#if adding}
    <SqlConnectionForm initial={editing} onSaved={saved} onCancel={() => { adding = false; editing = null; }} />
  {:else}
    <div class="actions">
      <button type="button" class={primary ? 'btn-primary' : 'btn'} disabled={full} onclick={() => { editing = null; adding = true; }}>{CONN_TEXT.addSql}</button>
      <button type="button" class="btn" onclick={() => onMongoAdd?.()}>{CONN_TEXT.addMongo}</button>
      <span class="hint" data-count>{sql.length} / {CONNECTION_LIMIT}</span>
    </div>
    {#if full}<p class="say bad" role="status" data-full>{CONN_TEXT.full}</p>{/if}
    <p class="hint">{CONN_TEXT.mongoHere}</p>
  {/if}

  {#if demo}
    <div class="empty" data-demo><p class="lead">{CONN_TEXT.demo}</p></div>
  {:else if loadFailed}
    <div class="empty"><p class="say bad" role="alert">{CONN_TEXT.loadFailed}</p><div class="actions"><button type="button" class="btn" onclick={() => void load()}>{CONN_TEXT.retry}</button></div></div>
  {:else if loaded && rows.length === 0}
    <div class="empty"><h4>{CONN_TEXT.noneHeading}</h4><p class="lead">{CONN_TEXT.noneBody}</p></div>
  {:else}
    <ul class="rows" aria-label={CONN_TEXT.heading}>
      {#each rows as row (row.key)}
        <li data-kind={row.kind}>
          <span class="what">
            <span class="name">{row.label}</span>
            <span class="hint">{row.detail} · {pwText(row.password)}</span>
            {#if row.problem !== '' && row.word === 'Failed'}<span class="hint" role="status">{row.problem}</span>{/if}
            {#if row.word === 'Works' && row.problem !== ''}<span class="hint">{row.problem}</span>{/if}
          </span>
          <StateSticker word={row.word} />
          <button type="button" class="btn" disabled={testing !== ''} aria-label="{CONN_TEXT.test}: {row.label}" onclick={() => void test(row)}>{testing === row.key ? CONN_TEXT.testing : CONN_TEXT.test}</button>
          {#if row.kind === 'sql'}<button type="button" class="btn" aria-label="{CONN_TEXT.edit}: {row.label}" onclick={() => edit(row)}>{CONN_TEXT.edit}</button>{/if}
          {#if asking === row.key}
            <span class="what" role="alertdialog" aria-label={CONN_TEXT.forgetAsk}>
              <span class="hint">{CONN_TEXT.forgetAsk}</span>
              <span class="actions">
                <button type="button" class="btn" onclick={() => void forget(row)}>{CONN_TEXT.forgetYes}</button>
                <button type="button" class="btn" onclick={() => { asking = ''; }}>{CONN_TEXT.forgetNo}</button>
              </span>
            </span>
          {:else}
            <button type="button" class="btn" aria-label="{CONN_TEXT.forget}: {row.label}" onclick={() => { asking = row.key; }}>{CONN_TEXT.forget}</button>
          {/if}
        </li>
      {/each}
    </ul>
    {#if note !== ''}<p class="say bad" role="alert">{note}</p>{/if}
  {/if}

  <p class="hint">{CONN_TEXT.wordsNote}</p>
  <p class="hint">{CONN_TEXT.origin}</p>
</section>
