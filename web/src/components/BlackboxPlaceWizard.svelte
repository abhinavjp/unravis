<script lang="ts">
  // The Place a Blackbox pop-up (TSK-17). This file joins the pieces and owns no rule of its own:
  //   - the one WizardState lives here and is handed to the step files (rules: blackboxWizard.ts);
  //   - the dialog shell is WizardDialog (focus, Esc, Enter, the leave question);
  //   - validate runs before Review for every way into Review, and the last button runs the real install or pack
  //     with the payload from buildSettings (probe off, no extra keys), with the same refusals as the old wizard.
  // Nothing is saved or changed until the last button.
  import { onMount, tick } from 'svelte';
  import {
    BlackboxError, blackboxApi, defaultSettings, refusalText, saveBlob,
    type BlackboxItem, type InstallAnswer, type Settings, type SettingsProblem, type ThisPc
  } from '../lib/blackbox';
  import { fetchDrives } from '../lib/blackboxDrives';
  import {
    applyDefaultFolder, back, buildSettings, goTo, initialState, next, setSqlSeen, stepProblem, steps, useDefaultsToReview, whereChoices,
    type StepId, type WizardState
  } from '../lib/blackboxWizard';
  import { BB_REFUSAL, BB_TEXT } from '../lib/copy/blackboxCopy';
  import { BBW_TEXT as W } from '../lib/copy/blackboxWizardCopy';
  import WizardDialog from './blackboxWizard/WizardDialog.svelte';
  import ProgressRail from './blackboxWizard/ProgressRail.svelte';
  import ResultView, { type ResultOutcome } from './blackboxWizard/ResultView.svelte';
  import StepName from './blackboxWizard/StepName.svelte';
  import StepWhere from './blackboxWizard/StepWhere.svelte';
  import StepRecord from './blackboxWizard/StepRecord.svelte';
  import StepSql from './blackboxWizard/StepSql.svelte';
  import StepWatch from './blackboxWizard/StepWatch.svelte';
  import StepKeep from './blackboxWizard/StepKeep.svelte';
  import StepFolder from './blackboxWizard/StepFolder.svelte';
  import StepReview from './blackboxWizard/StepReview.svelte';

  let { thisPc, canPack, onLeave, onPlaced, open = true }: {
    /** What the this-pc scan said. The Where step gates its cards on it and the SQL step follows its services. */
    thisPc: ThisPc;
    /** Whether this build can build a pack. */
    canPack: boolean;
    /** The owner left without placing anything. */
    onLeave: () => void;
    /** Something was really placed (an install, a pack, or an install whose read key was not saved). */
    onPlaced: (entry: BlackboxItem | null) => void;
    open?: boolean;
  } = $props();

  type Phase =
    | { kind: 'idle' }
    | { kind: 'working' }
    | { kind: 'installed'; answer: InstallAnswer }
    | { kind: 'pack'; blob: Blob; fileName: string }
    | { kind: 'failed'; where: 'pc' | 'pack'; error: unknown };

  const FALLBACK_FOLDER = defaultSettings().folder;
  // The default Windows service names of SQL Server: the default instance and every named instance (B-01).
  const SQL_SERVICE = /^(MSSQLSERVER|MSSQL\$.+)$/i;

  const uid = $props.id();
  const titleId = uid + '-title';

  let wizard = $state<WizardState>(initialState());
  let phase = $state<Phase>({ kind: 'idle' });
  // True once a Next (or a rail jump) was refused on the step being shown, so its problem shows before any typing.
  let attempted = $state(false);
  let checking = $state(false);
  // The server refused the answers at the check before Review. Shown on Review and holds the Place button.
  let reviewProblem = $state('');
  let dialog = $state<ReturnType<typeof WizardDialog> | null>(null);
  let bodyEl = $state<HTMLElement | null>(null);
  let reviewRun = 0;

  const list = $derived(steps(wizard));
  const index = $derived(list.indexOf(wizard.step));
  const busy = $derived(phase.kind === 'working');
  const partial = $derived(
    phase.kind === 'failed' && phase.error instanceof BlackboxError && phase.error.refusal.reason === 'key-not-saved'
  );
  // Contact was lost during a PC install: the outcome is unknown, so the list must be read again on leaving.
  const lostContact = $derived(
    phase.kind === 'failed' && phase.where === 'pc' && phase.error instanceof BlackboxError && phase.error.refusal.reason === 'offline'
  );
  // Placed for real: nothing is left to lose, and a partial install already put the service on this PC.
  const finished = $derived(phase.kind === 'installed' || phase.kind === 'pack' || partial);
  const showingResult = $derived(phase.kind === 'installed' || phase.kind === 'pack' || phase.kind === 'failed');
  const problem = $derived(stepProblem(wizard, wizard.step));
  const railSteps = $derived(list.map((id) => ({ id, label: W[('rail.' + id) as `rail.${StepId}`] })));

  const TITLE: Record<StepId, string> = {
    name: W['step.name.title'], where: W['step.where.title'], record: W['step.record.title'], sql: W['step.sql.title'],
    watch: W['step.watch.title'], keep: W['step.keep.title'], folder: W['step.folder.title'], review: W['step.review.title']
  };
  // The SQL and Folder leads say "this PC" or "the other server" by the answer on the Where step.
  const LEAD = $derived<Record<StepId, string>>({
    name: W['step.name.lead'], where: W['step.where.lead'], record: W['step.record.lead'],
    sql: wizard.where === 'pack' ? W['step.sql.leadPack'] : W['step.sql.lead'],
    watch: W['step.watch.lead'], keep: W['step.keep.lead'],
    folder: wizard.where === 'pack' ? W['step.folder.leadPack'] : W['step.folder.lead'],
    review: W['step.review.lead']
  });
  // Name, Where and Watch print their own help inside the step, so the header does not repeat it.
  const HELP: Partial<Record<StepId, string>> = {
    record: W['step.record.help'], sql: W['step.sql.help'], keep: W['step.keep.help'], folder: W['step.folder.help'], review: W['step.review.help']
  };

  const headTitle = $derived(
    phase.kind === 'installed' ? W['result.placedTitle'] : phase.kind === 'pack' ? W['result.packTitle'] : TITLE[wizard.step]
  );
  const headLead = $derived(showingResult ? '' : LEAD[wizard.step]);
  const headHelp = $derived(showingResult ? '' : (HELP[wizard.step] ?? ''));

  const outcome = $derived.by((): ResultOutcome | null => {
    if (phase.kind === 'installed') return { kind: 'installed', answer: phase.answer };
    if (phase.kind === 'pack') return { kind: 'pack', fileName: phase.fileName };
    if (phase.kind === 'failed') return { kind: 'failed', where: phase.where, error: phase.error };
    return null;
  });

  // Targets the picker may be told about. Only the targets rules are shown there, and only after a refused Next.
  const watchProblem = $derived<SettingsProblem | null>(attempted && problem !== null && problem.startsWith('targets-') ? 'targets' : null);

  // The step that owns a problem code from stepProblem, so a refusal on Review can send the owner to the right place.
  function ownerOf(code: string): StepId {
    if (code.startsWith('name-')) return 'name';
    if (code === 'where-missing') return 'where';
    if (code === 'interval' || code === 'logs') return 'record';
    if (code.startsWith('targets-')) return 'watch';
    if (code === 'keep' || code === 'size' || code === 'free') return 'keep';
    return 'folder';
  }

  async function afterStepChange(): Promise<void> {
    await tick();
    dialog?.focusStep();
  }

  // After a refused Next: put focus on the first thing the step marked as wrong. A field inside a closed
  // "Advanced" section is opened first, or Next would seem to do nothing (a bad port, a bad listen address).
  async function focusProblem(): Promise<void> {
    await tick();
    const field = bodyEl?.querySelector<HTMLElement>('[aria-invalid="true"]')
      ?? bodyEl?.querySelector<HTMLElement>('input[type="radio"]:not(:disabled)');
    if (!field) return;
    const closed = field.closest('details');
    if (closed && !closed.open) {
      closed.open = true;
      await tick();
    }
    field.focus();
  }

  // The one way the wizard state moves. A new step clears the "refused" marks and the Review check result.
  function apply(to: WizardState): void {
    const moved = to.step !== wizard.step;
    wizard = to;
    attempted = false;
    if (!moved) return;
    reviewRun += 1;
    reviewProblem = '';
    checking = false;
    void afterStepChange();
    if (to.step === 'review') void enterReview();
  }

  // Every way into Review (Next, the rail, Use defaults, a Change detour coming back) passes here: the local rules first,
  // then the server's own check, so a bad answer is found before the last button and not after it.
  async function enterReview(): Promise<void> {
    const code = stepProblem(wizard, 'review');
    if (code) {
      const owner = ownerOf(code);
      wizard = { ...wizard, step: owner, returnTo: 'review' };
      attempted = true;
      void afterStepChange().then(focusProblem);
      return;
    }
    const run = ++reviewRun;
    checking = true;
    try {
      await blackboxApi.validate($state.snapshot(buildSettings(wizard)) as Settings);
      if (run === reviewRun && wizard.step === 'review') reviewProblem = '';
    } catch (error) {
      if (run === reviewRun && wizard.step === 'review') reviewProblem = refusalText(error, BB_REFUSAL);
    } finally {
      if (run === reviewRun) checking = false;
    }
  }

  function goNext(): void {
    const moved = next(wizard);
    if (moved === wizard) {
      if (problem) { attempted = true; void focusProblem(); }
      return;
    }
    apply(moved);
  }

  function goBack(): void {
    if (busy || checking) return;
    apply(back(wizard));
  }

  function railGo(i: number): void {
    const id = list[i];
    if (id === undefined || busy) return;
    const moved = goTo(wizard, id);
    if (moved === wizard) {
      if (id !== wizard.step && i > index && problem) { attempted = true; void focusProblem(); }
      return;
    }
    apply(moved);
  }

  // The shortcut skips the Folder step, so the default folder is worked out here when that step was never reached.
  async function useDefaults(): Promise<void> {
    if (busy || checking) return;
    if (list.indexOf('folder') > wizard.maxSeen) {
      checking = true;
      try {
        const answer = wizard.where === 'pc' ? await fetchDrives() : null;
        wizard = applyDefaultFolder(wizard, answer ? answer.drives : null, FALLBACK_FOLDER);
      } catch {
        wizard = applyDefaultFolder(wizard, null, FALLBACK_FOLDER);
      } finally {
        checking = false;
      }
    }
    // From the Keep step the shortcut also puts back that step's own three limits.
    const moved = useDefaultsToReview(wizard, wizard.step === 'keep');
    if (moved !== wizard) apply(moved);
  }

  async function place(): Promise<void> {
    if (busy || checking || reviewProblem) return;
    const code = stepProblem(wizard, 'review');
    if (code) { void enterReview(); return; }
    const where = wizard.where === 'pack' ? 'pack' : 'pc';
    const body = $state.snapshot(buildSettings(wizard)) as Settings;
    phase = { kind: 'working' };
    void afterStepChange();
    try {
      if (where === 'pc') {
        const answer = await blackboxApi.install(body);
        phase = { kind: 'installed', answer };
      } else {
        const made = await blackboxApi.pack(body);
        saveBlob(made.blob, made.fileName);
        phase = { kind: 'pack', blob: made.blob, fileName: made.fileName };
      }
    } catch (error) {
      phase = { kind: 'failed', where, error };
    }
    void afterStepChange();
  }

  function downloadAgain(): void {
    if (phase.kind === 'pack') saveBlob(phase.blob, phase.fileName);
  }

  // Back to Review after a failure, with the answers kept as they were.
  function backToReview(): void {
    phase = { kind: 'idle' };
    reviewRun += 1;
    void enterReview();
    void afterStepChange();
  }

  // The result's "Change" next to the folder: back to the Folder step, and Next there returns to Review.
  function changeFolder(): void {
    phase = { kind: 'idle' };
    apply({ ...wizard, step: 'folder', returnTo: 'review' });
  }

  function reviewChanged(): void {
    attempted = false;
    reviewRun += 1;
    reviewProblem = '';
    checking = false;
    void afterStepChange();
  }

  function leave(): void {
    if (phase.kind === 'installed') onPlaced(phase.answer.entry);
    else if (phase.kind === 'pack' || partial || lostContact) onPlaced(null);
    else onLeave();
  }

  function primary(): void {
    if (busy || checking) return;
    if (finished) leave();
    else if (phase.kind === 'failed') backToReview();
    else if (wizard.step === 'review') void place();
    else goNext();
  }

  const primaryLabel = $derived.by((): string => {
    if (finished) return W['footer.done'];
    if (phase.kind === 'failed') return W['footer.backToReview'];
    if (wizard.step === 'review') {
      if (busy) return wizard.where === 'pc' ? W['footer.placing'] : W['footer.building'];
      return wizard.where === 'pc' ? W['footer.placePc'] : W['footer.buildPack'];
    }
    return W['footer.next'];
  });
  // Where: with neither card available there is nothing to choose, so Next stays off and says why.
  const whereBlocked = $derived(wizard.step === 'where' && !showingResult && whereChoices(thisPc, canPack).none !== null);
  const primaryDisabled = $derived(
    busy || checking || whereBlocked || (phase.kind === 'idle' && wizard.step === 'review' && reviewProblem !== '')
  );
  const canBack = $derived(!showingResult && index > 0);
  const canDefaults = $derived(
    !showingResult && (wizard.step === 'record' || wizard.step === 'sql' || wizard.step === 'watch' || wizard.step === 'keep')
  );
  // Enter must never place or build from the page: on Review only the Place or Build button itself acts (its own click).
  const enterNeedsButton = $derived(wizard.step === 'review' && phase.kind === 'idle');
  const nextReasonId = uid + '-next-reason';

  // A held-down Enter on the focused primary button would repeat its click. Only the first press acts.
  function primaryKey(e: KeyboardEvent): void {
    if (e.key === 'Enter' && e.repeat) e.preventDefault();
  }
  const labels = {
    close: W['leave.close'],
    leaveTitle: W['leave.title'],
    leaveBody: W['leave.body'],
    keepGoing: W['leave.keepGoing'],
    leave: W['leave.leave'],
    busyNote: W['leave.busy']
  };

  // SQL Server on this PC adds the SQL step (B-01). It is read once, from the same services list the Watch step shows.
  onMount(() => {
    if (!thisPc.available) return;
    const controller = new AbortController();
    blackboxApi.targets(controller.signal).then(
      (answer) => {
        if (answer.services.some((s) => SQL_SERVICE.test(s.name))) wizard = setSqlSeen(wizard, true);
      },
      () => { /* not read: the SQL step simply stays out of the pc path */ }
    );
    return () => controller.abort();
  });
</script>

<WizardDialog
  bind:this={dialog}
  {open}
  {titleId}
  {busy}
  {finished}
  {enterNeedsButton}
  {labels}
  onLeave={leave}
  onPrimary={primary}
>
  {#snippet rail()}
    {#if !showingResult}
      <ProgressRail steps={railSteps} current={index} maxSeen={wizard.maxSeen} disabled={busy || checking} onGo={railGo} />
    {/if}
  {/snippet}

  {#snippet header()}
    <h2 id={titleId} class="head-title">{headTitle}</h2>
    {#if headLead}<p class="head-lead">{headLead}</p>{/if}
    {#if headHelp}<p class="head-help">{headHelp}</p>{/if}
  {/snippet}

  {#snippet body()}
    <div class="step-body" bind:this={bodyEl}>
      {#if outcome}
        <ResultView
          {outcome}
          settings={buildSettings(wizard)}
          onDownloadAgain={phase.kind === 'pack' ? downloadAgain : undefined}
          onChangeFolder={phase.kind === 'pack' || (phase.kind === 'failed' && !partial) ? changeFolder : undefined}
        />
      {:else if wizard.step === 'name'}
        <StepName {wizard} showProblem={attempted} onChange={(to) => { wizard = to; }} />
      {:else if wizard.step === 'where'}
        <StepWhere {wizard} {thisPc} {canPack} onChange={(to) => { wizard = to; attempted = false; }} />
        {#if attempted && problem === 'where-missing'}
          <p class="problem" role="alert">{W['where.missing']}</p>
        {/if}
      {:else if wizard.step === 'record'}
        <StepRecord bind:wizard />
      {:else if wizard.step === 'sql'}
        <StepSql />
      {:else if wizard.step === 'watch'}
        <StepWatch bind:wizard problem={watchProblem} />
      {:else if wizard.step === 'keep'}
        <StepKeep bind:wizard />
      {:else if wizard.step === 'folder'}
        <StepFolder bind:wizard {attempted} />
      {:else}
        <StepReview bind:wizard disabled={busy || checking} onChange={reviewChanged} />
        <div class="review-status" aria-live="polite">
          {#if checking}<p class="status">{W['review.checking']}</p>{/if}
          {#if busy}<p class="status" role="status">{wizard.where === 'pc' ? BB_TEXT.installing : BB_TEXT.building}</p>{/if}
          {#if reviewProblem}<p class="problem" role="alert">{reviewProblem}</p>{/if}
        </div>
      {/if}
    </div>
  {/snippet}

  {#snippet footer()}
    {#if canBack}
      <button type="button" class="secondary-button" disabled={busy || checking} onclick={goBack}>{W['footer.back']}</button>
    {/if}
    {#if whereBlocked}
      <p class="saved-note" id={nextReasonId}>{W['where.nextOff']}</p>
    {:else if !showingResult}
      <p class="saved-note calm">{W['footer.nothingSaved']}</p>
    {/if}
    <span class="spacer"></span>
    {#if canDefaults}
      <button type="button" class="quiet-button" disabled={busy || checking} onclick={useDefaults}>{W['footer.useDefaults']}</button>
    {/if}
    <button
      type="button"
      class="primary-button"
      disabled={primaryDisabled}
      aria-describedby={whereBlocked ? nextReasonId : undefined}
      onkeydown={primaryKey}
      onclick={primary}
    >{primaryLabel}</button>
  {/snippet}
</WizardDialog>

<style>
  .head-title { margin: 0; font-size: var(--fs-6); line-height: 1.25; }
  .head-title:focus-visible { outline: 2px solid var(--focus); outline-offset: 3px; }
  .head-lead { margin: 0; color: var(--text); font-size: var(--fs-4); }
  .head-help { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .step-body { display: grid; gap: var(--space-4); min-width: 0; }
  .review-status { display: grid; gap: var(--space-2); }
  .status { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .problem { margin: 0; color: var(--bad); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .saved-note { flex: 1 1 12rem; margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .spacer { flex: 1 1 0; }
  .primary-button, .secondary-button, .quiet-button { min-height: 44px; padding: 0 var(--space-5); }
  /* On a phone the footer has two rows: "Use defaults", then Back and the main button side by side. The reassurance line
     "Nothing is saved until the last step" is left out there to give the step more room; the Review step says it too. The
     line that says why Next is off stays. */
  @media (max-width: 640px) {
    .saved-note { order: 0; flex: 0 0 100%; }
    .saved-note.calm { display: none; }
    .quiet-button { order: 1; flex: 0 0 100%; }
    .secondary-button { order: 2; }
    .spacer { order: 3; }
    .primary-button { order: 4; }
  }
</style>
