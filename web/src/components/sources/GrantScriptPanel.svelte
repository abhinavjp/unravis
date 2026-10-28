<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchMongoGrantScript, fetchSqlGrantScripts, recallGrant, rememberGrant } from '../../lib/lanes';
  import { GRANT_TEXT, versionNote } from '../../lib/copy/sourcesCopy';
  import './sources.css';

  // The permissions a login needs, shown as a script for the OWNER of the server to run. Unravis never runs it: this panel only fetches the
  // text from /api/grant-scripts and offers a Copy button. SQL Server 2019 and 2022 name some permissions differently, so each has its own script.
  // The permission a single check was missing, with its own script, is shown with that check (LaneResultView): it is not repeated here.
  // What was typed or picked is kept for the session, so this panel leaving and coming back does not undo it.
  let { kind = 'sql', version = '2022', login = '', database = '' }:
    { kind?: 'sql' | 'mongo'; version?: '2019' | '2022'; login?: string; database?: string } = $props();

  let chosen = $state<'2019' | '2022'>('2022');
  let who = $state('');
  let db = $state('');
  let script = $state('');
  let loading = $state(true);
  let failed = $state(false);
  let copied = $state<'' | 'yes' | 'no'>('');
  let timer: ReturnType<typeof setTimeout> | undefined;

  onMount(() => {
    const kept = recallGrant(kind);
    chosen = kept.version ?? version; who = kept.login ?? login; db = kept.database ?? database;
    return () => { clearTimeout(timer); };
  });

  // Reload when the version or the names change; typing waits a moment so each key does not ask the server again.
  $effect(() => {
    const v = chosen; const l = who.trim(); const d = db.trim(); const k = kind;
    const mine = new AbortController();
    // The script on screen stays until the new one arrives, so a keystroke does not blank and redraw it.
    loading = true; failed = false; copied = '';
    clearTimeout(timer);
    timer = setTimeout(() => {
      const task = k === 'sql' ? fetchSqlGrantScripts(v, l, d, mine.signal).then(r => r.script) : fetchMongoGrantScript(l, d, mine.signal);
      task.then((text) => { if (!mine.signal.aborted) { script = text; loading = false; } })
        .catch(() => { if (!mine.signal.aborted) { script = ''; failed = true; loading = false; } });
    }, 250);
    return () => { mine.abort(); clearTimeout(timer); };
  });

  async function copy(text: string) {
    try { await navigator.clipboard.writeText(text); copied = 'yes'; } catch { copied = 'no'; }
  }
</script>

<section class="src panel" aria-labelledby="grant-head">
  <h4 id="grant-head">{kind === 'sql' ? GRANT_TEXT.heading : GRANT_TEXT.mongoHeading}</h4>
  <p class="lead">{GRANT_TEXT.notRun}</p>
  <p class="hint">{kind === 'sql' ? GRANT_TEXT.runAs : GRANT_TEXT.mongoRunAs}. {GRANT_TEXT.readOnly}</p>

  {#if kind === 'sql'}
    <div class="field">
      <span class="label" id="grant-version">{GRANT_TEXT.version}</span>
      <div class="actions" role="group" aria-labelledby="grant-version">
        <button type="button" class="btn" aria-pressed={chosen === '2022'} onclick={() => { chosen = '2022'; rememberGrant(kind, { version: '2022' }); }}>{GRANT_TEXT.v2022}</button>
        <button type="button" class="btn" aria-pressed={chosen === '2019'} onclick={() => { chosen = '2019'; rememberGrant(kind, { version: '2019' }); }}>{GRANT_TEXT.v2019}</button>
      </div>
      <p class="hint" data-version-note>{versionNote(chosen)}</p>
    </div>
  {/if}
  <div class="pair">
    <div class="field">
      <label for="grant-login">{kind === 'sql' ? GRANT_TEXT.login : GRANT_TEXT.mongoLogin}</label>
      <input id="grant-login" type="text" bind:value={who} oninput={() => rememberGrant(kind, { login: who })} maxlength="200" autocomplete="off" autocapitalize="off" spellcheck="false" />
      {#if kind === 'sql'}<p class="hint">{GRANT_TEXT.loginHint}</p>{/if}
    </div>
    <div class="field">
      <label for="grant-db">{kind === 'sql' ? GRANT_TEXT.database : GRANT_TEXT.mongoDatabase}</label>
      <input id="grant-db" type="text" bind:value={db} oninput={() => rememberGrant(kind, { database: db })} maxlength="200" autocomplete="off" autocapitalize="off" spellcheck="false" />
      {#if kind === 'sql'}<p class="hint">{GRANT_TEXT.databaseHint}</p>{/if}
    </div>
  </div>

  <div aria-live="polite">
    {#if loading && script === ''}<p class="hint">{GRANT_TEXT.loading}</p>{/if}
    {#if failed}<p class="say bad" role="alert">{GRANT_TEXT.loadFailed}</p>{/if}
  </div>
  {#if script !== ''}
    <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
    <pre class="script" tabindex="0" aria-busy={loading} aria-label={kind === 'sql' ? GRANT_TEXT.heading : GRANT_TEXT.mongoHeading}>{script}</pre>
    <div class="actions">
      <button type="button" class="btn" disabled={loading} onclick={() => void copy(script)}>{GRANT_TEXT.copy}</button>
      <span aria-live="polite" class="hint">{copied === 'yes' ? GRANT_TEXT.copied : copied === 'no' ? GRANT_TEXT.copyFailed : ''}</span>
    </div>
  {/if}
</section>
