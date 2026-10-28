<script lang="ts">
  import Drawer from '../../components/Drawer.svelte';
  import { restoreAsNew, restoreInPlace } from '../archiveApi';
  import { keyEndingOf, passphraseOk, type ArchiveFile } from '../archiveModel';
  import { CaseApiError, believedState, caseCode, isCaseChanged, isCaseGone, type CaseInfo } from '../cases';
  import { ARCHIVE_TEXT, archiveReasonText, keyEndingText, restoredNewNotice } from '../copy/archiveCopy';
  import { formatBytes } from '../copy/importCopy';

  // The two ways back from the archive. In place moves the case record back, closed, and reads nothing from the case file (only while the working copy
  // is kept). As new reads the case file and makes another case; the archived case and the file stay as they are. A typed passphrase is asked for only
  // when the file was sealed with one, is sent once and is never kept.
  let { mode, info, file, onClose, onRestored, onOpenNew, onChanged, onGone }: {
    mode: 'in-place' | 'as-new';
    info: Pick<CaseInfo, 'id' | 'title' | 'state' | 'closed'>;
    file: ArchiveFile | null;
    onClose: () => void;
    /** The case came back in place, or a new case was made: the page reads again. */
    onRestored: (made: CaseInfo | null) => void;
    onOpenNew: (id: string) => void;
    onChanged: () => void;
    onGone: () => void;
  } = $props();

  let passphrase = $state('');
  let busy = $state(false);
  let problem = $state('');
  let made = $state.raw<CaseInfo | null>(null);
  let placed = $state(false);

  const needsKey = $derived(mode === 'as-new' && file?.keyKind === 'typed');
  const ending = $derived(keyEndingOf(file));
  const canSubmit = $derived(!busy && !made && !placed && (!needsKey || passphraseOk(passphrase)));
  const title = $derived(mode === 'in-place' ? ARCHIVE_TEXT.restoreInPlaceTitle : ARCHIVE_TEXT.restoreNewTitle);
  const intro = $derived(mode === 'in-place' ? ARCHIVE_TEXT.restoreInPlaceIntro : ARCHIVE_TEXT.restoreNewIntro);

  function failedCall(error: unknown) {
    if (error instanceof DOMException && error.name === 'AbortError') return;
    if (isCaseGone(error)) { onGone(); return; }
    if (isCaseChanged(error)) onChanged();
    problem = error instanceof CaseApiError ? archiveReasonText(error.reason, error.status) : archiveReasonText('unreachable', 0);
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (!canSubmit) return;
    busy = true;
    problem = '';
    try {
      if (mode === 'in-place') {
        await restoreInPlace(info.id, believedState(info));
        placed = true;
        onRestored(null);
      } else {
        const next = await restoreAsNew(info.id, believedState(info), needsKey ? passphrase : undefined);
        passphrase = '';
        made = next;
        onRestored(next);
      }
    } catch (error) { failedCall(error); } finally { busy = false; }
  }
</script>

<Drawer class="restore-drawer" {title} titleId="restore-title" {intro} focusOnOpen returnFocus closeLabel={ARCHIVE_TEXT.restoreClose} onClose={() => { passphrase = ''; onClose(); }}>
  <div class="restore-body">
    {#if mode === 'as-new' && file}
      <dl class="facts">
        <div><dt>{ARCHIVE_TEXT.restoreFile}</dt><dd>{file.name}</dd></div>
        {#if file.bytes > 0}<div><dt>{ARCHIVE_TEXT.caseSize}</dt><dd>{formatBytes(file.bytes)}</dd></div>{/if}
        {#if ending}<div><dt>{ARCHIVE_TEXT.keyEnding}</dt><dd>{ending}</dd></div>{/if}
      </dl>
    {/if}
    {#if made}
      <p class="done" role="status">{restoredNewNotice(made.title || caseCode(made.id), caseCode(made.id))}</p>
      <div class="actions">
        <button type="button" class="primary-button" onclick={() => onOpenNew(made?.id ?? '')}>{ARCHIVE_TEXT.restoreOpenNew}</button>
        <button type="button" class="secondary-button" onclick={onClose}>{ARCHIVE_TEXT.restoreClose}</button>
      </div>
    {:else if placed}
      <p class="done" role="status">{ARCHIVE_TEXT.restoreInPlaceDone}</p>
      <div class="actions"><button type="button" class="primary-button" onclick={onClose}>{ARCHIVE_TEXT.restoreClose}</button></div>
    {:else}
      <form class="form" onsubmit={submit}>
        {#if needsKey}
          <label for="restore-pass">{ARCHIVE_TEXT.restoreKeyLabel}{ending ? ' (' + keyEndingText(ending) + ')' : ''}</label>
          <input id="restore-pass" type="password" bind:value={passphrase} autocomplete="off" spellcheck="false" disabled={busy} />
          <p class="hint">{ARCHIVE_TEXT.restoreKeyHint}</p>
        {/if}
        <div class="actions">
          <button type="submit" class="primary-button" disabled={!canSubmit}>{busy ? ARCHIVE_TEXT.restoreWorking : mode === 'in-place' ? ARCHIVE_TEXT.restoreInPlaceSubmit : ARCHIVE_TEXT.restoreNewSubmit}</button>
          <button type="button" class="secondary-button" disabled={busy} onclick={onClose}>{ARCHIVE_TEXT.restoreCancel}</button>
        </div>
      </form>
    {/if}
    <div aria-live="polite">{#if problem}<p class="inline-error" role="alert">{problem}</p>{/if}</div>
  </div>
</Drawer>

<style>
  .restore-body { display: grid; gap: var(--space-3); padding-top: var(--space-3); }
  .form { display: grid; gap: var(--space-2); }
  .form label { color: var(--text-2); font-size: var(--fs-2); font-weight: 500; }
  .form input { width: 100%; font-family: var(--mono); }
  .facts { display: flex; flex-wrap: wrap; gap: var(--space-2) var(--space-5); margin: 0; }
  .facts div { display: grid; gap: 2px; min-width: 0; }
  .facts dt { color: var(--text-2); font-size: var(--fs-2); }
  .facts dd { margin: 0; font-size: var(--fs-3); overflow-wrap: anywhere; }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); margin-top: var(--space-2); }
  .done { margin: 0; font-size: var(--fs-3); }
</style>
