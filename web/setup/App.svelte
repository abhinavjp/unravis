<script lang="ts">
  import { onMount, tick } from 'svelte';
  import BrandMark from '../src/brand/components/BrandMark.svelte';
  import Button from '../src/brand/components/Button.svelte';
  import StepRail from '../src/brand/components/StepRail.svelte';
  import CheckList from './CheckList.svelte';
  import StepList from './StepList.svelte';
  import { api, ApiError } from './api.ts';
  import {
    approvalReason, blockedLine, blocked, buildRequest, canFinish, checksFailedFirst, CONFIRM_DELETE_WORD, doneHeading, failedHeading, formatBytes, mergeEvents,
    parseAddresses, problemNotice, progressHeading, progressPercent, railPosition, railSteps, retryHelps, screenAfterResult, settleSteps, stepAnnouncement, uninstallReady,
    showsCloseBox, showsFolderActions, uninstallWhy, validateOptions, welcomeHeading, welcomeNutshell,
    type BlackboxOptions, type Check, type Op, type Options, type Problem, type Result, type Scope, type Screen, type StepEvent,
    type UIChanges, type UIState, type UninstallOptions
  } from './logic.ts';

  let ui = $state<UIState | null>(null);
  let fatal = $state('');
  let closed = $state(false);
  let screen = $state<Screen>('welcome');
  let op = $state<Op>('install');
  let checks = $state<Check[]>([]);
  let checking = $state(false);
  let changes = $state<UIChanges | null>(null);
  let notice = $state('');
  let problems = $state<Problem[]>([]);

  let options = $state<Options>({ scope: 'user', location: '', blackbox: null, firewall: null, startAtSignIn: true, openWhenDone: false, closeRunningApp: true });
  let bbOn = $state(false);
  let bb = $state<BlackboxOptions>({ name: '', folder: '', keepDays: 7, maxGB: 2, minFreePercent: 10, port: 7788 });
  let fwOn = $state(false);
  let fwText = $state('');
  let uninst = $state<UninstallOptions>({ deleteCases: false, deleteRecordings: false, confirmCases: '', confirmRecordings: '', closeRunningApp: true });

  let rows = $state<StepEvent[]>([]);
  let result = $state<Result | null>(null);
  let cancelling = $state(false);
  let keySaved = $state(false);
  let copyNote = $state('');
  // A press is taken once: while one is being worked on (busy), and for a moment after the window changes screen (settling), so a
  // double click cannot also press the same place on the next screen. Settling uses aria-disabled so the buttons do not flash grey.
  let busy = $state(false);
  let settling = $state(false);
  let watching = false;
  let settleTimer: ReturnType<typeof setTimeout> | undefined;
  const SETTLE_MS = 500;
  // Said wherever data is about to be deleted. It is permanent: there is no Recycle Bin and no undo.
  const FOREVER = 'This cannot be undone. The files are deleted for good, not moved to the Recycle Bin.';

  const wanted = (): Options => ({
    ...options,
    blackbox: bbOn ? { ...bb } : null,
    firewall: bbOn && fwOn ? { remoteAddresses: parseAddresses(fwText) } : null
  });
  const request = () => buildRequest(op, wanted(), uninst);
  const message = (err: unknown) => (err instanceof Error ? err.message : String(err));
  const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
  const problemFor = (field: string) => problems.find((problem) => problem.field === field)?.message ?? '';

  const rail = $derived(railSteps(op));
  const position = $derived(railPosition(op, screen, changes ? changes.needsAdmin : null));
  const appRunning = $derived(checks.some((check) => check.id === 'app-running' && check.status !== 'ready'));
  const failedChecks = $derived(checks.filter((check) => check.status === 'failed'));
  const casesPlace = $derived(ui?.data?.find((place) => place.kind === 'cases'));
  const recordingsPlace = $derived(ui?.data?.find((place) => place.kind === 'recordings'));
  const blackboxName = $derived(ui?.installed?.blackboxName ?? '');
  const everyone = $derived((ui?.installed?.scope ?? options.scope) === 'machine');
  const blackboxDone = $derived(rows.find((row) => row.id === 'blackbox' && row.state === 'done')?.detail ?? '');
  const placeLabel = $derived(ui?.installed?.location ?? options.location);
  const hold = $derived(settling ? true : undefined);
  const orderedChecks = $derived(checksFailedFirst(checks));
  const blockedNote = $derived(blockedLine(checks, op === 'install'));
  const stepNumber = $derived(Math.max(0, rail.findIndex((step) => step.id === position.current)) + 1);
  const stepLabel = $derived(rail.find((step) => step.id === position.current)?.label ?? '');
  const deleting = $derived(op === 'uninstall' && (uninst.deleteCases || uninst.deleteRecordings));
  // Said beside a footer button that is off, so a greyed button always has its reason on the screen.
  const removeWhy = $derived(uninstallWhy(uninst, blackboxName));
  const finishWhy = $derived(op !== 'uninstall' && result && !canFinish(result, keySaved) ? 'Tick \u201cI have saved the read key\u201d to go on.' : '');
  // Said to a screen reader, not shown: a screen change names the step; the two end screens say how the run ended (D-C-26).
  const announcement = $derived(fatal || !ui ? '' : closed ? 'Setup is closed.' : stepAnnouncement(op, screen, stepNumber, rail.length, stepLabel, result));

  // After each screen change the new heading takes the focus, so a keyboard user starts at the top of what changed.
  $effect(() => {
    void screen; void ui; void closed; void fatal;
    void tick().then(() => {
      const heading = document.querySelector<HTMLElement>('main h1');
      if (!heading) return;
      heading.tabIndex = -1;
      heading.focus({ preventScroll: true });
    });
  });

  onMount(() => {
    void start();
    // The page tells setup it is still open; setup ends itself a few minutes after the page goes quiet.
    const timer = setInterval(() => { if (ui && !closed) api.state().catch(() => undefined); }, 30_000);
    return () => clearInterval(timer);
  });

  async function start() {
    if (!api.hasKey()) {
      fatal = 'This setup window has no key. Close it and start setup again.';
      return;
    }
    try {
      ui = await api.state();
    } catch (err) {
      fatal = message(err);
      return;
    }
    op = ui.mode;
    const scope: Scope = ui.installed?.scope ?? 'user';
    options.scope = scope;
    options.location = ui.installed?.location ?? (scope === 'machine' ? ui.defaults.machineLocation : ui.defaults.userLocation);
    bb = { ...ui.defaults.blackbox };
    screen = op === 'uninstall' ? 'uninstall' : 'welcome';
    // A refresh in the middle of a run: show the run that is still working instead of starting over.
    try {
      const live = await api.progress(0);
      if (live.running) {
        rows = mergeEvents([], live.events ?? []);
        screen = 'progress';
        void watch(live.next);
        return;
      }
    } catch { /* no run to join: carry on from the start */ }
    await check();
  }

  function go(next: Screen) {
    screen = next;
    settling = true;
    clearTimeout(settleTimer);
    settleTimer = setTimeout(() => { settling = false; }, SETTLE_MS);
  }

  // Runs one press at a time. A press that comes while another is being worked on, or right after a screen change, is ignored.
  async function once(work: () => Promise<void>) {
    if (busy || settling) return;
    busy = true;
    try { await work(); } finally { busy = false; }
  }
  const press = (work: () => void | Promise<void>) => () => { if (!settling) void work(); };

  async function check(): Promise<boolean> {
    checking = true;
    try {
      const answer = await api.preflight(request());
      checks = answer.checks ?? [];
      changes = answer.changes;
      return true;
    } catch (err) {
      notice = message(err);
      return false;
    } finally {
      checking = false;
    }
  }

  function setScope(scope: Scope) {
    if (!ui) return;
    const before = options.scope === 'machine' ? ui.defaults.machineLocation : ui.defaults.userLocation;
    if (options.location.trim() === '' || options.location === before) options.location = scope === 'machine' ? ui.defaults.machineLocation : ui.defaults.userLocation;
    options.scope = scope;
  }

  // Next on Welcome: an install goes to Options; an upgrade or repair keeps the choices it was installed with.
  const welcomeNext = () => once(async () => {
    notice = '';
    if (op === 'install') {
      go('options');
      return;
    }
    if (await check()) await proceed();
  });

  const optionsNext = () => once(async () => {
    notice = '';
    problems = validateOptions(wanted());
    if (problems.length) {
      notice = problemNotice(problems);
      await tick();
      document.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus();
      return;
    }
    if (await check()) {
      // Still blocked: say so here, where the person is, and the list of what is not ready sits under the form.
      if (blocked(checks)) notice = 'Setup cannot go on yet. Fix what is listed under \u201cNot ready yet\u201d, then press Next again.';
      else await proceed();
    }
  });

  const removeNext = () => once(async () => {
    notice = '';
    if (!uninstallReady(uninst, blackboxName)) return;
    if (await check()) await proceed();
  });

  // Enter in a text box of the Options screen does what Next does (it did nothing before). A button or a tick box keeps its own Enter.
  function enterInField(event: KeyboardEvent) {
    if (event.key !== 'Enter' || screen !== 'options' || event.defaultPrevented) return;
    const target = event.target as HTMLElement;
    if (!(target instanceof HTMLInputElement) || (target.type !== 'text' && target.type !== 'number')) return;
    event.preventDefault();
    if (!settling) void optionsNext();
  }

  async function proceed() {
    if (blocked(checks)) return;
    if (changes?.needsAdmin) go('approval');
    else await startRun();
  }

  const run = () => once(startRun);

  async function startRun() {
    if (watching) return;
    notice = '';
    result = null;
    rows = [];
    keySaved = false;
    copyNote = '';
    cancelling = false;
    const back = screen;
    try {
      await api.run(request());
    } catch (err) {
      notice = message(err);
      screen = back;
      return;
    }
    go('progress');
    void watch(0);
  }

  // Follows a run until it ends. Only one follower runs at a time.
  async function watch(from: number) {
    if (watching) return;
    watching = true;
    let since = from;
    for (;;) {
      try {
        const step = await api.progress(since);
        rows = mergeEvents(rows, step.events ?? []);
        since = step.next;
        if (!step.running && step.result) {
          watching = false;
          finish(step.result);
          return;
        }
      } catch (err) {
        if (err instanceof ApiError && (err.status === 0 || err.status === 401)) {
          watching = false;
          fatal = message(err);
          return;
        }
      }
      await sleep(250);
    }
  }

  function finish(res: Result) {
    result = res;
    if (res.outcome !== 'done' && res.outcome !== 'done-with-leftovers') rows = settleSteps(rows);
    if (res.checks?.length) checks = res.checks;
    const next = screenAfterResult(res);
    if (res.outcome === 'cancelled-at-uac') notice = 'You chose No at the Windows prompt, so nothing was changed. Press Continue to be asked again.';
    else if (next !== 'done' && next !== 'failed') notice = res.message;
    go(next);
  }

  async function cancelRun() {
    cancelling = true;
    try { await api.cancel(); } catch (err) { notice = message(err); cancelling = false; }
  }

  async function closeSetup() {
    try {
      await api.close();
    } catch (err) {
      notice = message(err);
      return;
    }
    closed = true;
    try { window.close(); } catch { /* a browser tab may refuse; the page says it can be closed */ }
  }

  async function openApp() {
    try {
      await api.openApp();
    } catch (err) {
      notice = message(err);
      return;
    }
    await closeSetup();
  }

  async function openLogFolder() {
    try { await api.openLogFolder(); } catch (err) { notice = message(err); }
  }

  async function copyKey() {
    if (!result?.readKey) return;
    try {
      await navigator.clipboard.writeText(result.readKey);
      copyNote = 'Copied.';
    } catch {
      copyNote = 'Select the key and copy it with Ctrl+C.';
    }
  }

  // Under a failed Program folder check: Next is off, so the folder box on Options is reached from here, and the person can look inside.
  function chooseAnotherFolder() {
    notice = '';
    go('options');
    setTimeout(() => document.querySelector<HTMLInputElement>('input[data-folder]')?.focus(), 60);
  }

  async function openProgramFolder() {
    try { await api.openFolder(options.location); } catch (err) { notice = message(err); }
  }

  function backToOptions() {
    notice = '';
    go(op === 'install' ? 'options' : op === 'uninstall' ? 'uninstall' : 'welcome');
  }
</script>

{#snippet logLine()}
  {#if ui?.logPath}
    <div class="log">
      <span class="log-label">Log</span>
      <span class="mono log-path">{ui.logPath}</span>
      <Button onclick={openLogFolder}>Open log folder</Button>
    </div>
  {/if}
{/snippet}

{#snippet noticeLine()}
  {#if notice}<p class="notice" role="alert">{notice}</p>{/if}
{/snippet}

{#snippet closeNote(check: Check)}
  {#if showsCloseBox(check)}
    <label class="tick under-row"><input type="checkbox" bind:checked={options.closeRunningApp} /> Close Unravis for me</label>
  {/if}
  {#if showsFolderActions(check)}
    <div class="under-row actions">
      {#if op === 'install'}<Button onclick={press(chooseAnotherFolder)} aria-disabled={hold}>Choose another folder</Button>{/if}
      <Button onclick={press(openProgramFolder)} aria-disabled={hold}>Open this folder</Button>
    </div>
  {/if}
{/snippet}

{#snippet closeCheckbox()}
  {#if appRunning}
    <label class="tick"><input type="checkbox" bind:checked={options.closeRunningApp} /> Close Unravis for me</label>
  {/if}
{/snippet}

<div class="page">
  <div class="frame">
    <header>
      <BrandMark wordmark="Unravis Setup" />
      {#if ui?.version}<span class="mono version">v{ui.version}</span>{/if}
    </header>
    <p class="sr" role="status" aria-live="polite" aria-atomic="true">{announcement}</p>

    {#if fatal}
      <main class="solo"><h1>Setup cannot go on</h1><p>{fatal}</p></main>
    {:else if closed}
      <main class="solo"><h1>Setup is closed</h1><p>You can close this window.</p></main>
    {:else if !ui}
      <main class="solo"><p class="quiet">Getting ready…</p></main>
    {:else}
      <div class="body">
        <aside><StepRail steps={rail} current={position.current} done={position.done} skipped={position.skipped} label="Setup steps" /></aside>
        <main>
          <!-- svelte-ignore a11y_no_static_element_interactions -->
          <div class="content" onkeydown={enterInField}>
            <p class="stepline" aria-hidden="true">Step {stepNumber} of {rail.length}: {stepLabel}</p>

            {#if screen === 'welcome'}
              <h1>{welcomeHeading(ui)}</h1>
              {@render noticeLine()}
              <div class="nutshell"><b>In a nutshell</b>
                {welcomeNutshell(op)}
              </div>
              {#if blockedNote}<p class="blocked" role="alert">{blockedNote}</p>{/if}
              {#if checking && !checks.length}<p class="quiet">Checking this PC…</p>{:else}<CheckList checks={orderedChecks} rowNote={closeNote} />{/if}

            {:else if screen === 'options'}
              <h1>Where and how</h1>
              {@render noticeLine()}
              <fieldset class="scope">
                <legend class="sr">Who it is for</legend>
                <label class="tick"><input type="radio" name="scope" checked={options.scope === 'user'} onchange={() => setScope('user')} /> Just for me <span class="quiet">(no admin needed)</span></label>
                <label class="tick"><input type="radio" name="scope" checked={options.scope === 'machine'} onchange={() => setScope('machine')} /> For everyone on this PC <span class="quiet">(admin needed)</span></label>
              </fieldset>
              <label class="field">
                <span>Folder</span>
                <input type="text" class="mono" data-folder bind:value={options.location} aria-invalid={problemFor('location') ? 'true' : undefined} aria-describedby={problemFor('location') ? 'err-location' : undefined} spellcheck="false" />
              </label>
              {#if problemFor('location')}<p class="problem" id="err-location">{problemFor('location')}</p>{/if}
              <label class="tick"><input type="checkbox" bind:checked={options.startAtSignIn} /> Start Unravis when I sign in</label>
              <label class="tick"><input type="checkbox" bind:checked={bbOn} /> Also place a Blackbox recorder on this PC</label>
              {#if bbOn}
                <div class="indent">
                  <div class="grid">
                    <label class="field"><span>Name</span><input type="text" bind:value={bb.name} aria-invalid={problemFor('bb-name') ? 'true' : undefined} aria-describedby={problemFor('bb-name') ? 'err-bb-name' : undefined} spellcheck="false" /></label>
                    <label class="field wide"><span>Folder</span><input type="text" class="mono" bind:value={bb.folder} aria-invalid={problemFor('bb-folder') ? 'true' : undefined} aria-describedby={problemFor('bb-folder') ? 'err-bb-folder' : undefined} spellcheck="false" /></label>
                    <label class="field"><span>Keep (days)</span><input type="number" min="1" max="365" bind:value={bb.keepDays} aria-invalid={problemFor('bb-keep') ? 'true' : undefined} aria-describedby={problemFor('bb-keep') ? 'err-bb-keep' : undefined} /></label>
                    <label class="field"><span>Max (GB)</span><input type="number" min="0.1" step="0.1" bind:value={bb.maxGB} aria-invalid={problemFor('bb-max') ? 'true' : undefined} aria-describedby={problemFor('bb-max') ? 'err-bb-max' : undefined} /></label>
                    <label class="field"><span>Keep free (%)</span><input type="number" min="1" max="50" bind:value={bb.minFreePercent} aria-invalid={problemFor('bb-free') ? 'true' : undefined} aria-describedby={problemFor('bb-free') ? 'err-bb-free' : undefined} /></label>
                    <label class="field"><span>Port</span><input type="number" min="1" max="65535" bind:value={bb.port} aria-invalid={problemFor('bb-port') ? 'true' : undefined} aria-describedby={problemFor('bb-port') ? 'err-bb-port' : undefined} /></label>
                  </div>
                  {#each problems.filter((problem) => problem.field.startsWith('bb-')) as problem (problem.field)}<p class="problem" id={'err-' + problem.field}>{problem.message}</p>{/each}
                  <label class="tick"><input type="checkbox" bind:checked={fwOn} /> Allow other PCs to read it (firewall rule)</label>
                  {#if fwOn}
                    <label class="field indent"><span>Addresses</span><input type="text" class="mono" bind:value={fwText} placeholder="10.0.0.5, 10.0.1.0/24" aria-invalid={problemFor('firewall') ? 'true' : undefined} aria-describedby={problemFor('firewall') ? 'err-firewall' : undefined} spellcheck="false" /></label>
                    {#if problemFor('firewall')}<p class="problem" id="err-firewall">{problemFor('firewall')}</p>{/if}
                  {/if}
                </div>
              {/if}
              {#if failedChecks.length}<CheckList checks={failedChecks} label="Not ready yet" />{/if}

            {:else if screen === 'approval'}
              <h1>Windows will ask for admin approval</h1>
              {@render noticeLine()}
              <div class="nutshell"><b>In a nutshell</b>
                {approvalReason(op, wanted(), ui.installed?.scope)} needs administrator rights. Windows shows its own prompt next; choose Yes to go on. Here is exactly what changes.
              </div>
              <div class="lists">
                <section><h2>Will change</h2><ul>{#each changes?.willChange ?? [] as item (item)}<li>{item}</li>{/each}</ul></section>
                <section><h2>Will not change</h2><ul>{#each changes?.willNotChange ?? [] as item (item)}<li>{item}</li>{/each}</ul></section>
              </div>
              {#if deleting}<p class="danger">{FOREVER}</p>{/if}

            {:else if screen === 'progress'}
              <h1>{progressHeading(op)}</h1>
              {@render noticeLine()}
              <div class="bar" role="progressbar" aria-label="Progress" aria-valuemin="0" aria-valuemax="100" aria-valuenow={progressPercent(rows)} aria-valuetext={progressPercent(rows) + ' percent'}>
                <span style:width={progressPercent(rows) + '%'}></span>
              </div>
              {#if rows.length}<StepList {rows} />{:else}<p class="quiet">Starting…</p>{/if}

            {:else if screen === 'done' && result}
              <h1>{doneHeading(op)}</h1>
              {@render noticeLine()}
              {#if op === 'uninstall'}
                {#if result.message && result.message.replace(/\.$/, '') !== doneHeading(op)}<p>{result.message}</p>{/if}
                {#if result.kept?.length}
                  <section><h2>Kept</h2><ul class="plain">{#each result.kept as item (item)}<li>{item}</li>{/each}</ul></section>
                {/if}
              {:else}
                <p>Version {result.version || ui.version} in <span class="mono">{placeLabel}</span></p>
                {#if blackboxDone}<p>Blackbox: {blackboxDone}</p>{/if}
                {#if result.restartApp}<p>Unravis was open and has been closed. Press Open Unravis to start the new version.</p>{/if}
                {#if result.readKey}
                  <section class="key">
                    <h2>Read key (shown once)</h2>
                    <p class="quiet" id="key-hint">Unravis on another PC needs this key to read this Blackbox. It is not saved anywhere you can see it again.</p>
                    <div class="key-row"><code class="mono">{result.readKey}</code><Button onclick={copyKey}>Copy</Button></div>
                    {#if copyNote}<p class="quiet" role="status">{copyNote}</p>{/if}
                    <label class="tick"><input type="checkbox" bind:checked={keySaved} aria-describedby="key-hint" /> I have saved the read key</label>
                  </section>
                {/if}
              {/if}
              {#if result.outcome === 'done-with-leftovers' && result.left?.length}
                <section><h2>Left behind</h2><ul class="plain mono">{#each result.left as item (item)}<li>{item}</li>{/each}</ul></section>
              {/if}
              {@render logLine()}

            {:else if screen === 'failed' && result}
              <h1>{failedHeading(result)}</h1>
              {@render noticeLine()}
              <p>{result.message}</p>
              <StepList rows={rows} fix={result.fix ?? ''} onretry={op === 'install' && retryHelps(result) ? run : undefined} />
              {#if result.left?.length}
                <section><h2>Left behind</h2><ul class="plain mono">{#each result.left as item (item)}<li>{item}</li>{/each}</ul></section>
              {/if}
              {@render logLine()}

            {:else if screen === 'uninstall'}
              <h1>Remove Unravis</h1>
              {@render noticeLine()}
              <div class="nutshell"><b>In a nutshell</b>
                Always removed: the program, its shortcuts and the Installed apps entry{everyone ? ', the key keeper service' : ''}{blackboxName ? ', the Blackbox service and its firewall rule' : ''}. Your data stays unless you tick it below.
              </div>
              <label class="tick"><input type="checkbox" bind:checked={uninst.deleteCases} aria-describedby={casesPlace ? 'cases-place' : undefined} /> {everyone ? 'Also delete the cases and saved connections of everyone on this PC' : 'Also delete my cases and saved connections'}</label>
              <div class="indent">
                {#if casesPlace}<p class="quiet" id="cases-place"><span class="mono">{casesPlace.folder}</span> ({formatBytes(casesPlace.bytes)})</p>{/if}
                {#if uninst.deleteCases}
                  <p class="danger" id="forever-cases">{FOREVER}</p>
                  <label class="field"><span>Type {CONFIRM_DELETE_WORD} to confirm</span><input type="text" class="mono" bind:value={uninst.confirmCases} aria-describedby="forever-cases" autocomplete="off" spellcheck="false" /></label>
                {/if}
              </div>
              {#if blackboxName}
                <label class="tick"><input type="checkbox" bind:checked={uninst.deleteRecordings} aria-describedby={recordingsPlace ? 'recordings-place' : undefined} /> Also delete the Blackbox recordings</label>
                <div class="indent">
                  {#if recordingsPlace}<p class="quiet" id="recordings-place"><span class="mono">{recordingsPlace.folder}</span> ({formatBytes(recordingsPlace.bytes)})</p>{/if}
                  {#if uninst.deleteRecordings}
                    <p class="danger" id="forever-recordings">{FOREVER}</p>
                    <label class="field"><span>Type the name {blackboxName} to confirm</span><input type="text" class="mono" bind:value={uninst.confirmRecordings} aria-describedby="forever-recordings" autocomplete="off" spellcheck="false" /></label>
                  {/if}
                </div>
              {/if}
              {@render closeCheckbox()}
              {#if failedChecks.length}<CheckList checks={failedChecks} label="Not ready yet" />{/if}
            {/if}
          </div>

          <footer>
            {#if screen === 'welcome'}
              <Button kind="quiet" onclick={press(() => { notice = ''; void check(); })} disabled={checking} aria-disabled={hold}>Check again</Button>
              <span class="grow"></span>
              <Button onclick={press(closeSetup)} aria-disabled={hold}>Cancel</Button>
              <Button kind="primary" onclick={welcomeNext} disabled={checking || busy || blocked(checks) || !checks.length} aria-disabled={hold}>{op === 'upgrade' ? 'Upgrade' : op === 'repair' ? 'Repair' : 'Next'}</Button>
            {:else if screen === 'options'}
              <span class="grow"></span>
              <Button onclick={press(() => { problems = []; notice = ''; go('welcome'); })} aria-disabled={hold}>Back</Button>
              <Button kind="primary" onclick={optionsNext} disabled={checking || busy} aria-disabled={hold}>Next</Button>
            {:else if screen === 'approval'}
              <span class="grow"></span>
              <Button onclick={press(backToOptions)} aria-disabled={hold}>Back</Button>
              <Button kind="primary" onclick={run} disabled={busy} aria-disabled={hold}>Continue</Button>
            {:else if screen === 'progress'}
              <span class="grow"></span>
              <Button onclick={press(cancelRun)} disabled={cancelling} aria-disabled={hold}>{cancelling ? 'Cancelling…' : 'Cancel'}</Button>
            {:else if screen === 'done'}
              <span class="grow why" id="finish-why">{finishWhy}</span>
              {#if op === 'uninstall'}
                <Button kind="primary" onclick={press(closeSetup)} aria-disabled={hold}>Close</Button>
              {:else}
                <Button onclick={press(closeSetup)} disabled={!canFinish(result, keySaved)} aria-disabled={hold} aria-describedby={finishWhy ? 'finish-why' : undefined}>Close</Button>
                <Button kind="primary" onclick={press(openApp)} disabled={!canFinish(result, keySaved)} aria-disabled={hold} aria-describedby={finishWhy ? 'finish-why' : undefined}>Open Unravis</Button>
              {/if}
            {:else if screen === 'failed'}
              <span class="grow"></span>
              <Button onclick={press(closeSetup)} aria-disabled={hold}>Close</Button>
              {#if op === 'install'}
                <Button kind="primary" onclick={press(backToOptions)} aria-disabled={hold}>Back to Options</Button>
              {:else}
                <Button kind="primary" onclick={run} disabled={busy || result?.outcome === 'rollback-incomplete'} aria-disabled={hold}>Try again</Button>
              {/if}
            {:else if screen === 'uninstall'}
              <span class="grow why" id="remove-why">{removeWhy}</span>
              <Button onclick={press(closeSetup)} aria-disabled={hold}>Cancel</Button>
              <Button kind={deleting ? 'danger' : 'primary'} onclick={removeNext} disabled={checking || busy || !uninstallReady(uninst, blackboxName)} aria-disabled={hold} aria-describedby={removeWhy ? 'remove-why' : undefined}>{deleting ? 'Remove and delete data' : 'Remove'}</Button>
            {/if}
          </footer>
        </main>
      </div>
    {/if}
  </div>
</div>

<style>
  /* The setup window is the card: it fills the window at any size, so there is no dead band around it. */
  .page { box-sizing: border-box; height: 100vh; height: 100dvh; }
  .frame { box-sizing: border-box; width: 100%; height: 100%; display: flex; flex-direction: column; background: var(--surface); overflow: hidden; }
  header { flex: 0 0 56px; height: 56px; display: flex; align-items: center; justify-content: space-between; padding: 0 20px; border-bottom: 1px solid var(--line); }
  .version { font-size: 12px; color: var(--text-2); }
  .body { flex: 1; min-height: 0; display: grid; grid-template-columns: 200px minmax(0, 1fr); }
  aside { padding: 16px 10px; border-right: 1px solid var(--line); background: var(--bg); }
  main { min-height: 0; min-width: 0; display: flex; flex-direction: column; }
  main.solo { padding: 24px; }
  .content { flex: 1; min-height: 0; min-width: 0; overflow: auto; overflow-wrap: anywhere; padding: 20px max(24px, calc((100% - 1040px) / 2)) 16px; display: flex; flex-direction: column; gap: 12px; }
  footer { flex: 0 0 auto; display: flex; align-items: center; gap: 8px; padding: 12px max(24px, calc((100% - 1040px) / 2)); border-top: 1px solid var(--line); }
  .grow { flex: 1; }
  h1 { margin: 0; font-size: 20px; font-weight: 600; line-height: 1.3; }
  h2 { margin: 0 0 6px; font-size: 13px; font-weight: 600; color: var(--text-2); }
  p { margin: 0; }
  .quiet { color: var(--text-2); }
  .nutshell { padding: 10px 12px; background: var(--surface-2); border-radius: var(--radius-sm); font-size: 13px; line-height: 1.5; }
  .nutshell b { display: block; margin-bottom: 2px; font-size: 12px; color: var(--text-2); }
  .why { min-width: 0; font-size: 12px; color: var(--text-2); }
  .under-row { margin-top: 6px; min-height: 24px; }
  .actions { display: flex; flex-wrap: wrap; gap: 8px; }
  .notice { padding: 8px 12px; border-radius: var(--radius-sm); background: color-mix(in srgb, var(--warn) 14%, var(--surface)); font-size: 13px; }
  .problem { color: var(--bad); font-size: 12px; }
  .blocked { padding: 8px 12px; border-radius: var(--radius-sm); border: 1px solid var(--bad); font-size: 13px; }
  .danger { color: var(--bad); font-size: 13px; font-weight: 600; }
  .stepline { display: none; font-size: 12px; color: var(--text-2); }
  h1:focus-visible { outline: none; }
  /* 24px tap target (BL-475): the row is 18.84px of text; padding and an equal negative margin add the hit area without moving anything. */
  .tick { display: flex; align-items: center; gap: 8px; font-size: 13px; box-sizing: border-box; min-height: 24px; padding-block: 2.58px; margin-block: -2.58px; }
  .scope { display: grid; gap: 6px; margin: 0; padding: 0; border: 0; }
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
  .field { display: grid; gap: 4px; font-size: 12px; color: var(--text-2); }
  .indent { padding-left: 23px; display: grid; gap: 8px; }
  .grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 8px; }
  .grid .wide { grid-column: span 3; }
  .lists { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
  .lists ul, .plain { margin: 0; padding-left: 18px; display: grid; gap: 4px; font-size: 13px; }
  .bar { height: 4px; background: var(--line); border-radius: 1px; overflow: hidden; }
  .bar span { display: block; height: 100%; background: var(--accent); transition: width var(--dur) var(--ease); }
  /* Forced colors (Windows high contrast) removes background colours: the track gets a border and the fill is the system highlight, so progress stays visible. */
  @media (forced-colors: active) {
    .bar { border: 1px solid CanvasText; }
    .bar span { background: Highlight; forced-color-adjust: none; }
  }
  .key { display: grid; gap: 6px; padding: 10px 12px; border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .key h2 { margin: 0; }
  .key-row { display: flex; align-items: center; gap: 10px; min-width: 0; }
  .key-row code { flex: 1; padding: 6px 8px; background: var(--surface-2); border-radius: var(--radius-sm); font-size: 14px; user-select: all; overflow-wrap: anywhere; }
  .log { display: flex; align-items: center; gap: 10px; min-width: 0; font-size: 12px; }
  .log-label { color: var(--text-2); }
  .log-path { flex: 1; min-width: 0; overflow-wrap: anywhere; color: var(--text-2); }
  @media (max-width: 700px) {
    .body { grid-template-columns: minmax(0, 1fr); }
    .stepline { display: block; }
    aside { display: none; }
    /* The frame keeps the height of the window on a narrow or zoomed screen: the content scrolls and the footer buttons stay in view (SETUP-A11Y-06). */
    .grid { grid-template-columns: 1fr 1fr; }
    .grid .wide { grid-column: span 2; }
    .lists { grid-template-columns: 1fr; }
  }
</style>
