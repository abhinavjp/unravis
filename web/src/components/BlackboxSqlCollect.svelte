<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { BlackboxError, blackboxApi, refusalText, type ThisPc } from '../lib/blackbox';
  import { BB_REFUSAL } from '../lib/copy/blackboxCopy';
  import { SQLC_TEXT as T } from '../lib/copy/blackboxSqlCopy';
  import { QUERY_TEXT_SIZES, SQL_PROFILES, keepsQueryText, loginChoices, profileOf, queryTextSizeOf, setupLine, sqlStatusLine, withProfile, type SqlProfile } from '../lib/blackboxSql';
  import type { SavedSqlConnection, SqlAccessReport } from '../lib/types';
  import SqlConnectionForm from './sources/SqlConnectionForm.svelte';
  import SqlAccessResult from './sources/SqlAccessResult.svelte';

  // "Collect SQL data" on a Blackbox that runs on this PC. The access check comes first and changes nothing; the turn-on is one
  // administrator approval that installs the SQL service if needed, saves the login sealed and starts it. The password is never
  // typed here and never read back: it stays with the key keeper.
  let { thisPc }: { thisPc: ThisPc } = $props();
  const uid = $props.id();

  let mode = $state<'login' | 'windows'>('login');
  let connections = $state<SavedSqlConnection[]>([]);
  let savedId = $state('');
  let profile = $state<SqlProfile>('standard');
  // The profile applied during this visit; the list refreshes later, so this keeps a second press from asking for a second approval.
  let applied = $state<{ profile: SqlProfile; text: boolean; size: number } | null>(null);
  let textOn = $state(false);
  let textSize = $state<number>(1000);
  let addOpen = $state(false);
  let busy = $state<'' | 'check' | 'on' | 'clear'>('');
  let report = $state<SqlAccessReport | null>(null);
  let grant = $state('');
  let line = $state('');
  let bad = $state(false);
  let copyNote = $state('');

  const choices = $derived(loginChoices(connections));
  const current = $derived(applied ?? { profile: profileOf(thisPc.settings), text: keepsQueryText(thisPc.settings), size: queryTextSizeOf(thisPc.settings) });
  const wantText = $derived(profile === 'deep' && textOn);
  // What would change at Apply: the profile, the text flag, or (only while text is on) its size.
  const changed = $derived(profile !== current.profile || wantText !== current.text || (wantText && textSize !== current.size));
  const status = $derived(sqlStatusLine(thisPc));

  async function load() {
    try { connections = await api.sqlConnections(); } catch { connections = []; }
    if (!choices.some((c) => c.id === savedId)) savedId = choices[0]?.id ?? '';
  }
  onMount(() => { profile = profileOf(thisPc.settings); textOn = keepsQueryText(thisPc.settings); textSize = queryTextSizeOf(thisPc.settings); void load(); });

  const reset = () => { report = null; grant = ''; line = ''; bad = false; };
  $effect(() => { void mode; void savedId; reset(); });

  async function check() {
    if (busy) return;
    if (!savedId) { bad = true; line = T.needConnection; return; }
    busy = 'check'; reset();
    try { report = await api.checkSqlAccess({ savedId }); }
    catch (e) { bad = true; line = e instanceof Error && e.message ? e.message : T.failed; }
    finally { busy = ''; }
  }

  // The profile is read by the SQL service when it starts, so a changed choice is applied to the recorder first.
  async function applyProfile(): Promise<boolean> {
    if (!changed) return true;
    const base = thisPc.settings;
    if (!base) { bad = true; line = T.failed; return false; }
    await blackboxApi.apply(withProfile(base, profile, { on: wantText, size: textSize }));
    applied = { profile, text: wantText, size: textSize };
    return true;
  }

  async function turnOn(enable: boolean) {
    if (busy) return;
    if (mode === 'login' && !savedId) { bad = true; line = T.needConnection; return; }
    busy = 'on'; reset();
    try {
      if (!(await applyProfile())) return;
      const answer = await blackboxApi.sqlSetup({ mode, savedId: mode === 'login' ? savedId : undefined, enable });
      if (answer.grantScript && !answer.started) grant = answer.grantScript;
      line = setupLine(answer);
    } catch (e) {
      bad = true;
      if (e instanceof BlackboxError && e.refusal.report) { report = e.refusal.report; line = T.notReady; }
      else line = refusalText(e, BB_REFUSAL) || T.failed;
    } finally { busy = ''; }
  }

  async function clear() {
    if (busy) return;
    busy = 'clear'; reset();
    try { await blackboxApi.sqlClearLogin(); line = T.cleared; }
    catch (e) { bad = true; line = refusalText(e, BB_REFUSAL) || T.failed; }
    finally { busy = ''; }
  }

  async function copyScript() {
    try { await navigator.clipboard.writeText(grant); copyNote = T.copied; } catch { copyNote = T.copyFailed; }
  }
</script>

<section class="sqlc" aria-labelledby="{uid}-h">
  <h4 id="{uid}-h">{T.heading}</h4>
  <p class="note">{T.intro}</p>
  {#if status}<p class="note" role="status">{status}</p>{/if}

  <fieldset class="modes">
    <legend>{T.howHeading}</legend>
    <label class="opt"><input type="radio" name="{uid}-mode" value="login" bind:group={mode} /> <span><b>{T.modeLogin}</b><span class="note">{T.modeLoginHelp}</span></span></label>
    <label class="opt"><input type="radio" name="{uid}-mode" value="windows" bind:group={mode} /> <span><b>{T.modeWindows}</b><span class="note">{T.modeWindowsHelp}</span></span></label>
  </fieldset>

  {#if mode === 'login'}
    {#if choices.length}
      <label class="field"><span>{T.connection}</span>
        <select bind:value={savedId} id="{uid}-conn">{#each choices as c (c.id)}<option value={c.id}>{c.label || c.server}</option>{/each}</select>
      </label>
    {:else}
      <p class="note">{T.noLogin}</p>
    {/if}
    <div class="row"><button type="button" class="secondary-button" aria-expanded={addOpen} onclick={() => { addOpen = !addOpen; }}>{addOpen ? T.hideAdd : T.addConnection}</button></div>
    {#if addOpen}<SqlConnectionForm onSaved={async (c) => { addOpen = false; await load(); savedId = c.id; }} onCancel={() => { addOpen = false; }} />{/if}
  {/if}

  <label class="field"><span>{T.profileHeading}</span>
    <select bind:value={profile} id="{uid}-profile">{#each SQL_PROFILES as p (p)}<option value={p}>{T.profile[p]}</option>{/each}</select>
  </label>
  {#if profile === 'deep'}
    <p class="note">{T.deepNote}</p>
    <label class="opt"><input type="checkbox" bind:checked={textOn} aria-describedby="{uid}-text-warn" /> <span><b>{T.queryText}</b></span></label>
    <p class="note warn" id="{uid}-text-warn">{T.queryTextWarn}</p>
    {#if textOn}
      <label class="field"><span>{T.queryTextSize}</span>
        <select bind:value={textSize} id="{uid}-text-size">{#each QUERY_TEXT_SIZES as n (n)}<option value={n}>{T.queryTextChars(n)}</option>{/each}</select>
      </label>
    {/if}
  {:else}
    <p class="note">{T.queryTextDeepOnly}</p>
  {/if}
  {#if changed}<p class="note">{T.profileWillApply}</p>{/if}

  <div class="row">
    {#if mode === 'login'}
      <button type="button" class="secondary-button" disabled={busy !== ''} onclick={() => void check()}>{busy === 'check' ? T.checking : T.check}</button>
      <button type="button" class="primary-button" disabled={busy !== ''} onclick={() => void turnOn(true)}>{busy === 'on' ? T.turningOn : T.turnOn}</button>
    {:else}
      <button type="button" class="primary-button" disabled={busy !== ''} onclick={() => void turnOn(false)}>{busy === 'on' ? T.turningOn : T.turnOnWindows}</button>
    {/if}
    <button type="button" class="secondary-button" disabled={busy !== ''} onclick={() => void clear()}>{busy === 'clear' ? T.clearing : T.clear}</button>
  </div>
  <p class="note">{T.oneApproval} {T.clearHelp}</p>

  {#if line}<p class="say" class:bad role="status">{line}</p>{/if}
  {#if report}<SqlAccessResult {report} />{/if}
  {#if grant}
    <label class="script-label" for="{uid}-grant">{T.scriptLabel}</label>
    <textarea id="{uid}-grant" class="script" readonly rows={Math.min(8, grant.split('\n').length)}>{grant}</textarea>
    <div class="row">
      <button type="button" class="secondary-button" onclick={() => void copyScript()}>{T.copy}</button>
      <button type="button" class="primary-button" disabled={busy !== ''} onclick={() => void turnOn(true)}>{T.windowsRan}</button>
      <span class="note" role="status">{copyNote}</span>
    </div>
    <p class="note">{T.windowsScriptIntro}</p>
  {/if}
  <p class="note">{T.otherPcs}</p>
</section>

<style>
  .sqlc { display: grid; gap: var(--space-3); min-width: 0; margin-top: var(--space-3); padding-top: var(--space-3); border-top: 1px solid var(--line); }
  h4 { margin: 0; font-size: 14px; font-weight: 600; }
  .note { margin: 0; font-size: 13px; line-height: 1.5; color: var(--text-2); overflow-wrap: anywhere; }
  .modes { display: grid; gap: var(--space-2); margin: 0; padding: 0; border: 0; }
  .modes legend { padding: 0; font-size: 13px; font-weight: 500; }
  .opt { display: flex; align-items: flex-start; gap: 8px; font-size: 13px; }
  .opt input { flex: 0 0 auto; margin: 3px 0 0; }
  .opt > span { display: grid; gap: 2px; min-width: 0; }
  .field { display: grid; gap: 4px; font-size: 13px; font-weight: 500; }
  .field select { width: 100%; max-width: 420px; padding: 6px 8px; font: inherit; font-weight: 400; color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); box-sizing: border-box; }
  .row { display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; }
  .warn { color: var(--text); border-left: 3px solid var(--warn); padding-left: var(--space-2); }
  .say { margin: 0; font-size: 13px; font-weight: 600; }
  .say.bad { color: var(--bad); }
  .script { font-family: var(--mono, ui-monospace, monospace); font-size: 12px; width: 100%; resize: vertical; box-sizing: border-box; }
  .script-label { font-size: 13px; }
</style>
