<script lang="ts">
  import { api } from '../../lib/api';
  import { blankSqlForm, formFromSaved, sqlFormProblem, sqlInput, sqlTestInput, usesKeptPassword, type SqlForm } from '../../lib/sqlConnection';
  import { CONN_TEXT } from '../../lib/copy/sourcesCopy';
  import { SOURCES_TEXT } from '../../lib/copy/wizardSourcesCopy';
  import { connectedText } from '../../lib/copy/healthCopy';
  import type { SavedSqlConnection } from '../../lib/types';
  import StateSticker from './StateSticker.svelte';
  import './sources.css';

  // Screen 54: one SQL Server connection, filled in the way SQL Server Management Studio asks for it. "Save connection" keeps the details on
  // this PC. A password is kept only when "Remember password" is ticked, sealed to the Windows account, and is never shown again: on edit the
  // password field starts empty and an empty field keeps the saved one. The 21st connection is refused by the server (409), and its sentence is shown.
  let { initial = null, primary = false, onSaved, onCancel }: { initial?: SavedSqlConnection | null; primary?: boolean; onSaved?: (saved: SavedSqlConnection) => void; onCancel?: () => void } = $props();

  const uid = $props.id();
  let form = $state<SqlForm>(blankSqlForm());
  let busy = $state<'' | 'test' | 'save'>('');
  let line = $state('');
  let word = $state<'' | 'Works' | 'Failed'>('');
  let failed = $state(false);
  let server = $state<HTMLInputElement>();

  $effect(() => { form = initial ? formFromSaved(initial) : blankSqlForm(); });

  function invalid(): boolean {
    const problem = sqlFormProblem(form);
    if (problem === 'server') { failed = true; word = ''; line = SOURCES_TEXT.sqlNeedServer; server?.focus(); return true; }
    if (problem === 'username') { failed = true; word = ''; line = SOURCES_TEXT.sqlNeedUser; return true; }
    return false;
  }

  async function test() {
    if (busy || invalid()) return;
    if (form.auth === 'sql' && form.password === '' && !usesKeptPassword(form)) { failed = true; word = 'Failed'; line = CONN_TEXT.testNeedsPassword; return; }
    busy = 'test'; failed = false; word = ''; line = '';
    try {
      const result = await api.testSqlConnection(sqlTestInput(form));
      word = 'Works'; line = connectedText(result.version, result.edition) + (usesKeptPassword(form) ? ' ' + SOURCES_TEXT.sqlTestKeptPassword : '');
    } catch (error) {
      failed = true; word = 'Failed'; line = error instanceof Error && error.message ? error.message : SOURCES_TEXT.sqlTestFailed;
    } finally { busy = ''; }
  }

  async function save() {
    if (busy || invalid()) return;
    busy = 'save'; failed = false; line = '';
    try {
      const kept = await api.saveSqlConnection(sqlInput(form));
      line = CONN_TEXT.saved; word = '';
      onSaved?.(kept);
    } catch (error) {
      failed = true; word = ''; line = error instanceof Error && error.message ? error.message : SOURCES_TEXT.sqlNotKept;
    } finally { busy = ''; }
  }

  function onKey(event: KeyboardEvent) {
    if (event.key === 'Enter' && !event.isComposing) { event.preventDefault(); void save(); }
  }
</script>

<section class="src panel" aria-labelledby="{uid}-head">
  <h4 id="{uid}-head">{CONN_TEXT.sqlFormHeading}</h4>
  <p class="lead">{SOURCES_TEXT.sqlIntro}</p>
  <div class="field">
    <label for="{uid}-name">{SOURCES_TEXT.sqlName}</label>
    <input id="{uid}-name" type="text" bind:value={form.label} maxlength="200" autocomplete="off" spellcheck="false" onkeydown={onKey} />
    <p class="hint">{SOURCES_TEXT.sqlNameHint}</p>
  </div>
  <div class="field">
    <label for="{uid}-server">{SOURCES_TEXT.sqlServer}</label>
    <input id="{uid}-server" type="text" bind:this={server} bind:value={form.server} maxlength="260" autocomplete="off" autocapitalize="off" spellcheck="false"
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
        <input id="{uid}-pass" type="password" bind:value={form.password} maxlength="512" autocomplete="off" aria-describedby="{uid}-pass-hint" onkeydown={onKey} />
        {#if initial?.hasPassword}<p class="hint" id="{uid}-pass-hint">{CONN_TEXT.keepPasswordNote}</p>{/if}
      </div>
    </div>
    <label class="tick"><input type="checkbox" bind:checked={form.rememberPassword} aria-describedby="{uid}-remember-hint" /> <span>{SOURCES_TEXT.sqlRemember}</span></label>
    <p class="hint" id="{uid}-remember-hint">{SOURCES_TEXT.sqlRememberHint}</p>
  {/if}
  <div class="field">
    <label for="{uid}-db">{SOURCES_TEXT.sqlDatabase}</label>
    <input id="{uid}-db" type="text" bind:value={form.database} maxlength="200" autocomplete="off" autocapitalize="off" spellcheck="false" onkeydown={onKey} />
    <p class="hint">{SOURCES_TEXT.sqlDatabaseHint}</p>
  </div>

  <div class="actions">
    <button type="button" class={primary ? 'btn-primary' : 'btn'} disabled={busy !== ''} onclick={() => void save()}>{busy === 'save' ? CONN_TEXT.saving : CONN_TEXT.save}</button>
    <button type="button" class="btn" disabled={busy !== ''} onclick={() => void test()}>{busy === 'test' ? CONN_TEXT.testing : CONN_TEXT.test}</button>
    <button type="button" class="btn" onclick={() => onCancel?.()}>{CONN_TEXT.cancel}</button>
    {#if word !== ''}<StateSticker word={word} />{/if}
  </div>
  <p class="say" class:bad={failed} class:good={!failed && line !== ''} id="{uid}-line" role="status" hidden={line === ''}>{line}</p>
</section>
