<script lang="ts">
  import { onMount } from 'svelte';
  import Drawer from '../../components/Drawer.svelte';
  import { cancelArchive, getArchiveJob, startArchive } from '../archiveApi';
  import { folderText, jobRunning, needsTypedPassphrase, passphraseOk, type FileJob } from '../archiveModel';
  import { app } from '../appState.svelte';
  import { CaseApiError, JOB_MAX_UNREACHABLE, JOB_POLL_MS, believedState, isCaseChanged, isCaseGone, type CaseInfo } from '../cases';
  import { ARCHIVE_TEXT, archiveReasonText, jobFailureText, keyEndingText, runningText } from '../copy/archiveCopy';
  import { formatBytes } from '../copy/importCopy';

  // Archive a case: one folder, one button. It works on an open or a closed case. The file is written, read back and checked on the server;
  // this drawer only asks and shows how far it has got. A passphrase is asked for only when no master key is in use, and it is never kept.
  let { info, onClose, onDone, onChanged, onGone }: {
    info: Pick<CaseInfo, 'id' | 'title' | 'state' | 'closed' | 'bytes' | 'ownKey' | 'keyEnding'>;
    onClose: () => void;
    /** The archive finished: the page reads the case again. */
    onDone: () => void;
    /** Another tab changed the case (M2). */
    onChanged: () => void;
    /** The case was erased elsewhere. */
    onGone: () => void;
  } = $props();

  let folder = $state('');
  let passphrase = $state('');
  let job = $state.raw<FileJob | null>(null);
  let problem = $state('');
  let starting = $state(false);
  let stopping = $state(false);
  let unreachable = $state(0);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let alive = true;

  // A case with a key of its own is sealed with that key: nothing is asked, whatever the master key is doing.
  const ownKey = $derived(info.ownKey === true);
  const typed = $derived(!ownKey && needsTypedPassphrase(app.lockState));
  const sealedText = $derived(ownKey ? [ARCHIVE_TEXT.sealedCase, keyEndingText(info.keyEnding ?? '')].filter(Boolean).join(' · ') : typed ? ARCHIVE_TEXT.sealedTyped : ARCHIVE_TEXT.sealedMaster);
  const running = $derived(jobRunning(job));
  const failed = $derived(job !== null && (job.state === 'failed' || job.state === 'stopped'));
  const finished = $derived(job?.state === 'done');
  const formOk = $derived(folderText(folder) !== '' && (!typed || passphraseOk(passphrase)));
  const percent = $derived(job?.percent ?? 0);
  const failureText = $derived(job && failed ? jobFailureText('archive', job.reason) : '');

  function reasonOf(error: unknown): string {
    if (error instanceof CaseApiError) return archiveReasonText(error.reason, error.status);
    return archiveReasonText('unreachable', 0);
  }

  function failedCall(error: unknown) {
    if (error instanceof DOMException && error.name === 'AbortError') return;
    if (isCaseGone(error)) { onGone(); return; }
    if (isCaseChanged(error)) { onChanged(); }
    problem = reasonOf(error);
  }

  function schedule() {
    clearTimeout(timer);
    if (alive) timer = setTimeout(() => void poll(), JOB_POLL_MS);
  }

  async function poll() {
    if (!alive) return;
    try {
      const next = await getArchiveJob(info.id);
      if (!alive) return;
      unreachable = 0;
      job = next;
      if (next.state === 'done') { stopping = false; onDone(); return; }
      if (next.state === 'running') { schedule(); return; }
      stopping = false;
    } catch (error) {
      if (!alive) return;
      if (isCaseGone(error)) { onGone(); return; }
      unreachable += 1;
      if (unreachable >= JOB_MAX_UNREACHABLE) { problem = archiveReasonText('unreachable', 0); return; }
      schedule();
    }
  }

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (starting || running || !formOk) return;
    starting = true;
    problem = '';
    try {
      const next = await startArchive(info.id, believedState(info), folderText(folder), typed ? passphrase : undefined);
      passphrase = '';
      job = next;
      if (next.state === 'running') schedule();
      else if (next.state === 'done') onDone();
    } catch (error) { failedCall(error); } finally { starting = false; }
  }

  async function stop() {
    if (stopping || !running) return;
    stopping = true;
    problem = '';
    try { await cancelArchive(info.id); schedule(); } catch (error) { stopping = false; failedCall(error); }
  }

  // A job that is already running (the drawer was closed and opened again, or another tab started it) shows its progress at once.
  onMount(() => {
    void (async () => {
      try {
        const now = await getArchiveJob(info.id);
        if (!alive) return;
        if (now.state === 'running') { job = now; schedule(); }
      } catch { /* the form simply shows */ }
    })();
    return () => { alive = false; clearTimeout(timer); passphrase = ''; };
  });
</script>

<Drawer class="archive-drawer" title={ARCHIVE_TEXT.archiveTitle} titleId="archive-title" intro={ARCHIVE_TEXT.archiveIntro} focusOnOpen returnFocus closeLabel={ARCHIVE_TEXT.archiveClose} {onClose}>
  <div class="archive-body">
    {#if running}
      <section class="running" aria-label={ARCHIVE_TEXT.progressLabel}>
        <p class="progress-line" role="status">{runningText(job?.phase ?? '', percent)}</p>
        <progress max="100" value={percent} aria-label={ARCHIVE_TEXT.progressLabel}></progress>
        <div class="actions">
          <button type="button" class="secondary-button" disabled={stopping} onclick={() => void stop()}>{stopping ? ARCHIVE_TEXT.archiveStopping : ARCHIVE_TEXT.archiveStop}</button>
        </div>
      </section>
    {:else if finished}
      <p class="done" role="status">{ARCHIVE_TEXT.archiveDone}</p>
      <div class="actions"><button type="button" class="primary-button" onclick={onClose}>{ARCHIVE_TEXT.archiveClose}</button></div>
    {:else}
      <form class="form" onsubmit={submit}>
        <dl class="facts">
          <div><dt>{ARCHIVE_TEXT.caseSize}</dt><dd>{formatBytes(info.bytes)}</dd></div>
          <div><dt>{ARCHIVE_TEXT.sealedWith}</dt><dd>{sealedText}</dd></div>
        </dl>
        <label for="archive-folder">{ARCHIVE_TEXT.folderLabel}</label>
        <input id="archive-folder" type="text" bind:value={folder} oninput={() => { problem = ''; }} placeholder={ARCHIVE_TEXT.folderPlaceholder} autocomplete="off" spellcheck="false" disabled={starting} />
        <p class="hint">{ARCHIVE_TEXT.folderHint}</p>
        {#if ownKey}
          <p class="hint">{ARCHIVE_TEXT.sealedCaseHint}</p>
        {:else if typed}
          <label for="archive-pass">{ARCHIVE_TEXT.passphraseLabel}</label>
          <input id="archive-pass" type="password" bind:value={passphrase} oninput={() => { problem = ''; }} autocomplete="new-password" spellcheck="false" disabled={starting} />
          <p class="hint">{ARCHIVE_TEXT.passphraseHint}</p>
        {/if}
        {#if failed}<p class="inline-error" role="alert">{failureText}</p>{/if}
        <div class="actions">
          <button type="submit" class="primary-button" disabled={starting || !formOk}>{starting ? ARCHIVE_TEXT.archiveRunning : ARCHIVE_TEXT.archiveSubmit}</button>
          <button type="button" class="secondary-button" onclick={onClose}>{ARCHIVE_TEXT.archiveCancel}</button>
        </div>
      </form>
    {/if}
    <div aria-live="polite">{#if problem}<p class="inline-error" role="alert">{problem}</p>{/if}</div>
  </div>
</Drawer>

<style>
  .archive-body { display: grid; gap: var(--space-3); padding-top: var(--space-3); }
  .form { display: grid; gap: var(--space-2); }
  .form label { color: var(--text-2); font-size: var(--fs-2); font-weight: 500; }
  .form input { width: 100%; font-family: var(--mono); }
  .facts { display: flex; flex-wrap: wrap; gap: var(--space-2) var(--space-5); margin: 0 0 var(--space-2); }
  .facts div { display: grid; gap: 2px; }
  .facts dt { color: var(--text-2); font-size: var(--fs-2); }
  .facts dd { margin: 0; font-size: var(--fs-3); }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-2); }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); margin-top: var(--space-2); }
  .running { display: grid; gap: var(--space-2); }
  .progress-line { margin: 0; font-size: var(--fs-3); font-variant-numeric: tabular-nums; }
  .running progress { width: 100%; height: 8px; accent-color: var(--accent, currentColor); }
  .done { margin: 0; font-size: var(--fs-3); }
</style>
