<script lang="ts">
  import { onMount } from 'svelte';
  import Drawer from '../../components/Drawer.svelte';
  import { cancelVerify, freeCase, getVerify, startVerify } from '../archiveApi';
  import { freeConfirmed, jobRunning, keyEndingOf, passphraseOk, type ArchiveFile, type FileJob } from '../archiveModel';
  import { CaseApiError, JOB_MAX_UNREACHABLE, JOB_POLL_MS, believedState, caseCode, isCaseChanged, isCaseGone, type CaseInfo } from '../cases';
  import { ARCHIVE_TEXT, archiveReasonText, jobFailureText, keyEndingText } from '../copy/archiveCopy';

  // Free the space in three stages: 1 the case file is checked by a real test restore, 2 the person types the case code, 3 the working copy is gone.
  // The server refuses Free until a check of this very file has passed, so this drawer cannot skip stage 1; it only shows it.
  let { info, file, onClose, onFreed, onChanged, onGone }: {
    info: Pick<CaseInfo, 'id' | 'title' | 'state' | 'closed'>;
    file: ArchiveFile | null;
    onClose: () => void;
    onFreed: () => void;
    onChanged: () => void;
    onGone: () => void;
  } = $props();

  let job = $state.raw<FileJob | null>(null);
  let passphrase = $state('');
  let typedCode = $state('');
  let problem = $state('');
  let starting = $state(false);
  let stopping = $state(false);
  let freeing = $state(false);
  let freed = $state(false);
  let unreachable = $state(0);
  let verified = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let alive = true;

  const needsPass = $derived(file?.keyKind === 'typed');
  const running = $derived(jobRunning(job));
  const failedText = $derived(job && (job.state === 'failed' || job.state === 'stopped') ? jobFailureText('verify', job.reason) : '');
  const stage = $derived(freed ? 3 : verified ? 2 : 1);
  const codeOk = $derived(freeConfirmed(typedCode, info.id));
  const ending = $derived(keyEndingOf(file));

  function failedCall(error: unknown) {
    if (error instanceof DOMException && error.name === 'AbortError') return;
    if (isCaseGone(error)) { onGone(); return; }
    if (isCaseChanged(error)) onChanged();
    problem = error instanceof CaseApiError ? archiveReasonText(error.reason, error.status) : archiveReasonText('unreachable', 0);
  }

  function schedule() {
    clearTimeout(timer);
    if (alive) timer = setTimeout(() => void poll(), JOB_POLL_MS);
  }

  function take(next: FileJob) {
    job = next;
    if (next.state === 'done' && next.verified) { verified = true; stopping = false; return false; }
    if (next.state === 'running') return true;
    stopping = false;
    return false;
  }

  async function poll() {
    if (!alive) return;
    try {
      const next = await getVerify(info.id);
      if (!alive) return;
      unreachable = 0;
      if (take(next)) schedule();
    } catch (error) {
      if (!alive) return;
      if (isCaseGone(error)) { onGone(); return; }
      unreachable += 1;
      if (unreachable >= JOB_MAX_UNREACHABLE) { problem = archiveReasonText('unreachable', 0); return; }
      schedule();
    }
  }

  async function check(event?: SubmitEvent) {
    event?.preventDefault();
    if (starting || running || (needsPass && !passphraseOk(passphrase))) return;
    starting = true;
    problem = '';
    try {
      const next = await startVerify(info.id, believedState(info), needsPass ? passphrase : undefined);
      passphrase = '';
      if (take(next)) schedule();
    } catch (error) { failedCall(error); } finally { starting = false; }
  }

  async function stop() {
    if (stopping || !running) return;
    stopping = true;
    try { await cancelVerify(info.id); schedule(); } catch (error) { stopping = false; failedCall(error); }
  }

  async function free(event: SubmitEvent) {
    event.preventDefault();
    if (freeing || !codeOk || freed) return;
    freeing = true;
    problem = '';
    try {
      await freeCase(info.id, believedState(info), typedCode);
      freed = true;
      onFreed();
    } catch (error) {
      failedCall(error);
      // A check that no longer counts (the file changed on disk) sends the person back to stage 1.
      if (error instanceof CaseApiError && error.reason === 'check-first') { verified = false; job = null; }
    } finally { freeing = false; }
  }

  // A check that is already running, or one that already passed for this file, is shown at once.
  onMount(() => {
    void (async () => {
      try {
        const now = await getVerify(info.id);
        if (!alive) return;
        if (now.state === 'running') { job = now; schedule(); }
        else if (now.state === 'done' && now.verified) { job = now; verified = true; }
      } catch { /* stage 1 simply shows */ }
    })();
    return () => { alive = false; clearTimeout(timer); passphrase = ''; };
  });
</script>

<Drawer class="free-drawer" title={ARCHIVE_TEXT.freeTitle} titleId="free-title" intro={ARCHIVE_TEXT.freeIntro} closeLabel={ARCHIVE_TEXT.freeClose} {onClose}>
  <div class="free-body">
    <ol class="stages" aria-label={ARCHIVE_TEXT.freeTitle}>
      <li class:current={stage === 1} aria-current={stage === 1 ? 'step' : undefined}>{ARCHIVE_TEXT.freeStage1}</li>
      <li class:current={stage === 2} aria-current={stage === 2 ? 'step' : undefined}>{ARCHIVE_TEXT.freeStage2}</li>
    </ol>

    {#if stage === 1}
      {#if running}
        <section class="running">
          <p class="progress-line" role="status">{ARCHIVE_TEXT.freeCheckRunning} {job?.percent ?? 0}%</p>
          <progress max="100" value={job?.percent ?? 0} aria-label={ARCHIVE_TEXT.verifyRunning}></progress>
          <div class="actions"><button type="button" class="secondary-button" disabled={stopping} onclick={() => void stop()}>{stopping ? ARCHIVE_TEXT.verifyStopping : ARCHIVE_TEXT.verifyStop}</button></div>
        </section>
      {:else}
        <form class="form" onsubmit={check}>
          {#if needsPass}
            <label for="free-pass">{ARCHIVE_TEXT.verifyPassphraseLabel}{ending ? ' (' + keyEndingText(ending) + ')' : ''}</label>
            <input id="free-pass" type="password" bind:value={passphrase} autocomplete="off" spellcheck="false" disabled={starting} />
          {/if}
          {#if failedText}<p class="inline-error" role="alert">{failedText}</p>{/if}
          <div class="actions">
            <button type="submit" class="primary-button" disabled={starting || (needsPass && !passphraseOk(passphrase))}>{starting ? ARCHIVE_TEXT.verifyRunning : ARCHIVE_TEXT.verifyStart}</button>
            <button type="button" class="secondary-button" onclick={onClose}>{ARCHIVE_TEXT.freeKeep}</button>
          </div>
        </form>
      {/if}
    {:else if stage === 2}
      <p class="done" role="status">{ARCHIVE_TEXT.verifyPassed}</p>
      <form class="form" onsubmit={free}>
        <p class="warn">{ARCHIVE_TEXT.freeAfter} {file?.name ?? ''}{ending ? ' · ' + keyEndingText(ending) : ''}</p>
        <p class="warn">{ARCHIVE_TEXT.freeKeyWarning}</p>
        <label for="free-code">{ARCHIVE_TEXT.freeTypeCode}: {caseCode(info.id)}</label>
        <input id="free-code" type="text" bind:value={typedCode} autocomplete="off" spellcheck="false" disabled={freeing} />
        <div class="actions">
          <button type="submit" class="primary-button danger-fill" disabled={freeing || !codeOk}>{freeing ? ARCHIVE_TEXT.freeWorking : ARCHIVE_TEXT.freeSubmit}</button>
          <button type="button" class="secondary-button" disabled={freeing} onclick={onClose}>{ARCHIVE_TEXT.freeKeep}</button>
        </div>
      </form>
    {:else}
      <p class="done" role="status">{ARCHIVE_TEXT.freeDone}</p>
      <div class="actions"><button type="button" class="primary-button" onclick={onClose}>{ARCHIVE_TEXT.freeClose}</button></div>
    {/if}
    <div aria-live="polite">{#if problem}<p class="inline-error" role="alert">{problem}</p>{/if}</div>
  </div>
</Drawer>

<style>
  .free-body { display: grid; gap: var(--space-3); padding-top: var(--space-3); }
  .stages { display: flex; flex-wrap: wrap; gap: var(--space-2) var(--space-4); margin: 0; padding: 0; list-style: none; color: var(--text-2); font-size: var(--fs-2); }
  .stages li.current { color: var(--text); font-weight: 600; }
  .form { display: grid; gap: var(--space-2); }
  .form label { color: var(--text-2); font-size: var(--fs-2); font-weight: 500; }
  .form input { width: 100%; font-family: var(--mono); }
  .warn { margin: 0; font-size: var(--fs-3); overflow-wrap: anywhere; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); margin-top: var(--space-2); }
  .running { display: grid; gap: var(--space-2); }
  .progress-line { margin: 0; font-size: var(--fs-3); font-variant-numeric: tabular-nums; }
  .running progress { width: 100%; height: 8px; accent-color: var(--accent, currentColor); }
  .done { margin: 0; font-size: var(--fs-3); }
  .danger-fill { background: var(--bad); border-color: var(--bad); color: var(--bg); }
  .danger-fill:disabled { opacity: .5; }
</style>
