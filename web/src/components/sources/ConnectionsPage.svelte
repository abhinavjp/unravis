<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../lib/api';
  import {
    CONNECTION_LIMIT, canSaveAnother, mongoRow, sqlRow, type ConnectionRow, type ConnectionWord
  } from '../../lib/lanes';
  import { CONN_TEXT, HUB_TEXT, KEEPER_TEXT, NUTSHELL } from '../../lib/copy/sourcesCopy';
  import { isKeeperDown, keeperPreview, keeperView, type KeeperStatus } from '../../lib/keeper';
  import { connectedText } from '../../lib/copy/healthCopy';
  import { sqlTestInput, formFromSaved } from '../../lib/sqlConnection';
  import type { RecentConnection, SavedSqlConnection } from '../../lib/types';
  import SqlConnectionForm from './SqlConnectionForm.svelte';
  import StateSticker from './StateSticker.svelte';
  import KeeperCard from './KeeperCard.svelte';
  import { app } from '../../lib/appState.svelte';
  import { announceConnectionsChanged, dropTest, recallTests, rememberTest, type TestResult } from './connectionMemory';
  import './sources.css';

  // Screens 52 and 53: every saved connection in one list, SQL Server and MongoDB. Test says only Works, Failed or Not tested, and the last
  // test is kept in memory only (it is not a saved fact). A saved password is never shown or sent back: a row only says whether one is saved.
  // Forget asks once. At 20 SQL connections the server refuses the 21st (409); this page says so first and disables the add button.
  // primary: the one button this page may offer as the main action. It is outlined by default, so a case home that already has its own main
  // button keeps a single one; the guided shell passes primary where this page is the whole screen.
  let { onMongoAdd, primary: promoted = false, readOnly = false }: { onMongoAdd?: () => void; primary?: boolean; readOnly?: boolean } = $props();

  let sql = $state.raw<SavedSqlConnection[]>([]);
  let mongo = $state.raw<RecentConnection[]>([]);
  let loaded = $state(false);
  let loadFailed = $state(false);
  let demo = $state(false);
  let tests = $state.raw<Record<string, TestResult>>(recallTests());
  let testing = $state('');
  let asking = $state('');
  let editing = $state<SavedSqlConnection | null>(null);
  let adding = $state(false);
  let note = $state('');
  let notice = $state('');
  let askedMongo = $state(false);
  // The key keeper (P14): null until asked, and on a PC without it the status says "user" and nothing about the keeper shows.
  let keeper = $state.raw<KeeperStatus | null>(null);
  let moveLater = $state(false);

  const rows = $derived<ConnectionRow[]>([
    ...sql.map(item => sqlRow(item, tests['sql:' + item.id])),
    ...mongo.map(item => mongoRow(item, tests['mongo:' + item.id]))
  ]);
  const full = $derived(!canSaveAnother(sql.length));
  const shared = $derived(keeper?.mode === 'shared');
  const kView = $derived(keeperView(keeper));
  // While the move card shows, it holds the page's one main button.
  const offerMove = $derived(kView === 'migrate' && !moveLater);
  // The add button is the main one only where the shell promotes this page and no move card holds that place.
  const primary = $derived(promoted && !offerMove);
  const pwText = (p: ConnectionRow['password']) => (p === 'saved' ? CONN_TEXT.pwSaved : p === 'none' ? CONN_TEXT.pwNone : CONN_TEXT.pwNotNeeded);

  // withKeeper false keeps the keeper status the last move or removal answered (in the demo the preview would otherwise put the old state back).
  async function load(withKeeper = true) {
    loadFailed = false;
    const [a, b, k] = await Promise.allSettled([api.sqlConnections(), api.recentConnections(), withKeeper ? api.keeperStatus(keeperPreview()) : Promise.resolve(keeper)]);
    if (a.status === 'fulfilled' && Array.isArray(a.value)) sql = a.value;
    if (b.status === 'fulfilled' && Array.isArray(b.value)) mongo = b.value;
    keeper = k.status === 'fulfilled' && k.value && typeof k.value === 'object' ? k.value : null;
    // A stopped or untrusted keeper also answers 503: that is the keeper card's to say, not the demo's.
    const keeperDown = [a, b].some(r => r.status === 'rejected' && isKeeperDown(r.reason));
    const refused = (r: PromiseSettledResult<unknown>) => r.status === 'rejected' && (r.reason as { status?: number } | null)?.status === 503;
    demo = refused(a) && refused(b) && !keeperDown;
    loadFailed = a.status === 'rejected' && b.status === 'rejected' && !demo && !keeperDown;
    loaded = true;
  }
  onMount(() => { void load(); });

  const remember = (key: string, word: ConnectionWord, problem: string) => {
    tests = rememberTest(key, { word, at: new Date().toLocaleString(), problem });
  };

  async function test(row: ConnectionRow) {
    if (testing) return;
    note = ''; notice = '';
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
    if (app.info?.demo === true) { notice = CONN_TEXT.testDemo; return; }
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
    asking = ''; note = ''; notice = '';
    try {
      if (row.kind === 'sql') await api.forgetSqlConnection(row.id); else await api.forgetConnection(row.id);
      tests = dropTest(row.key);
      announceConnectionsChanged();
    } catch (error) { note = error instanceof Error ? error.message : CONN_TEXT.loadFailed; }
    await load();
  }

  function edit(row: ConnectionRow) {
    notice = '';
    if (row.kind !== 'sql') return;
    editing = sql.find(s => s.id === row.id) ?? null;
    adding = editing !== null;
  }
  function saved(item: SavedSqlConnection) {
    adding = false; editing = null;
    notice = CONN_TEXT.saved;
    tests = dropTest('sql:' + item.id);
    announceConnectionsChanged();
    void load();
  }
  async function keeperChanged(next: KeeperStatus) { keeper = next; await load(false); }
  // A stopped or untrusted keeper cannot use a saved password, so the row says so instead of only "held".
  const keeperUnusable = $derived(kView === 'stopped' || kView === 'impostor');
</script>

<section class="src" aria-labelledby="conn-head">
  <div class="nutshell"><b>{NUTSHELL}</b><p>{CONN_TEXT.intro}</p></div>
  <h3 id="conn-head">{CONN_TEXT.heading}</h3>

  <KeeperCard primary={promoted} status={offerMove || kView !== 'migrate' ? keeper : null} onStatus={(next) => void keeperChanged(next)} onRetry={() => load()} onLater={() => { moveLater = true; }} />
  {#if moveLater && kView === 'migrate'}<div class="actions" data-keeper-later><span class="hint">{KEEPER_TEXT.notNowNote}</span><button type="button" class="btn" onclick={() => { moveLater = false; }}>{KEEPER_TEXT.notNowUndo}</button></div>{/if}

  {#if adding}
    <SqlConnectionForm initial={editing} onSaved={saved} onCancel={() => { adding = false; editing = null; }} />
  {:else}
    <div class="actions">
      <button type="button" class={primary ? 'btn-primary' : 'btn'} disabled={full || readOnly} onclick={() => { editing = null; adding = true; notice = ''; }}>{CONN_TEXT.addSql}</button>
      <button type="button" class="btn" disabled={readOnly || !onMongoAdd} onclick={() => { askedMongo = true; onMongoAdd?.(); }}>{CONN_TEXT.addMongo}</button>
      <button type="button" class="btn" onclick={() => void load()}>{CONN_TEXT.refresh}</button>
      {#if askedMongo}<span class="hint" data-after-mongo>{CONN_TEXT.refreshAfterMongo}</span>{/if}
      <span class="hint" data-count>{CONN_TEXT.countSql} {sql.length} / {CONNECTION_LIMIT}. {CONN_TEXT.countMongo} {mongo.length}.</span>
    </div>
    {#if full}<p class="say bad" role="status" data-full>{CONN_TEXT.full}</p>{/if}
    <p class="hint">{onMongoAdd ? CONN_TEXT.mongoHere : CONN_TEXT.mongoNowhere}</p>
    {#if readOnly}<p class="hint" data-read-only>{HUB_TEXT.readOnlyWhy}</p>{/if}
  {/if}
  {#if notice !== ''}<p class="say" role="status" data-notice>{notice}</p>{/if}

  {#if demo}
    <div class="empty" data-demo><p class="lead">{CONN_TEXT.demo}</p></div>
  {:else if loadFailed}
    <div class="empty"><p class="say bad" role="alert">{CONN_TEXT.loadFailed}</p><div class="actions"><button type="button" class="btn" onclick={() => void load()}>{CONN_TEXT.retry}</button></div></div>
  {:else if loaded && rows.length === 0}
    <div class="empty"><h4>{CONN_TEXT.noneHeading}</h4><p class="lead">{CONN_TEXT.noneBody}</p></div>
  {:else}
    {#if loaded && sql.length === 0}<p class="hint" data-no-sql>{CONN_TEXT.noSql}</p>{/if}
    {#if loaded && mongo.length === 0}<p class="hint" data-no-mongo>{CONN_TEXT.noMongo}</p>{/if}
    <ul class="rows" aria-label={CONN_TEXT.heading}>
      {#each rows as row (row.key)}
        <li data-kind={row.kind}>
          <span class="what">
            <span class="name">{row.label}</span>
            {#if shared && row.password === 'saved'}
              <span class="hint">{row.detail}</span>
              <span><span class="src-sticker held" class:down={keeperUnusable} data-keeper-held>{keeperUnusable ? KEEPER_TEXT.heldDown : KEEPER_TEXT.held}</span></span>
            {:else}
              <span class="hint">{row.detail} · {pwText(row.password)}</span>
            {/if}
            {#if row.problem !== '' && row.word === 'Failed'}<span class="hint" role="status">{row.problem}</span>{/if}
            {#if row.word === 'Works' && row.problem !== ''}<span class="hint">{row.problem}</span>{/if}
          </span>
          <StateSticker word={row.word} />
          <button type="button" class="btn" disabled={testing !== '' || readOnly} aria-label="{CONN_TEXT.test}: {row.label}" onclick={() => void test(row)}>{testing === row.key ? CONN_TEXT.testing : CONN_TEXT.test}</button>
          {#if row.kind === 'sql'}<button type="button" class="btn" disabled={readOnly} aria-label="{CONN_TEXT.edit}: {row.label}" onclick={() => edit(row)}>{CONN_TEXT.edit}</button>{/if}
          {#if asking === row.key}
            <span class="what" role="alertdialog" aria-label={CONN_TEXT.forgetAsk}>
              <span class="hint">{CONN_TEXT.forgetAsk}</span>
              <span class="actions">
                <button type="button" class="btn" onclick={() => void forget(row)}>{CONN_TEXT.forgetYes}</button>
                <button type="button" class="btn" onclick={() => { asking = ''; }}>{CONN_TEXT.forgetNo}</button>
              </span>
            </span>
          {:else}
            <button type="button" class="btn" disabled={readOnly} aria-label="{CONN_TEXT.forget}: {row.label}" onclick={() => { asking = row.key; }}>{CONN_TEXT.forget}</button>
          {/if}
        </li>
      {/each}
    </ul>
    {#if note !== ''}<p class="say bad" role="alert">{note}</p>{/if}
  {/if}

  <p class="hint">{CONN_TEXT.wordsNote}</p>
  <p class="hint">{shared ? KEEPER_TEXT.origin : CONN_TEXT.origin}</p>
</section>
