<script lang="ts">
  import { onMount } from 'svelte';
  import BrandMark from '../src/brand/components/BrandMark.svelte';
  import Button from '../src/brand/components/Button.svelte';
  import StepRail from '../src/brand/components/StepRail.svelte';
  import CheckList from './CheckList.svelte';
  import StepList from './StepList.svelte';
  import { api, ApiError } from './api.ts';
  import {
    approvalReason, blocked, buildRequest, canFinish, CONFIRM_DELETE_WORD, doneHeading, failedHeading, formatBytes, mergeEvents,
    parseAddresses, progressHeading, progressPercent, railPosition, railSteps, screenAfterResult, settleSteps, uninstallReady,
    validateOptions, welcomeHeading,
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
    await check();
  }

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
  async function welcomeNext() {
    notice = '';
    if (op === 'install') {
      screen = 'options';
      return;
    }
    if (await check()) proceed();
  }

  async function optionsNext() {
    notice = '';
    problems = validateOptions(wanted());
    if (problems.length) return;
    if (await check()) proceed();
  }

  async function removeNext() {
    notice = '';
    if (!uninstallReady(uninst, blackboxName)) return;
    if (await check()) proceed();
  }

  function proceed() {
    if (blocked(checks)) return;
    if (changes?.needsAdmin) screen = 'approval';
    else void run();
  }

  async function run() {
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
    screen = 'progress';
    let since = 0;
    for (;;) {
      try {
        const step = await api.progress(since);
        rows = mergeEvents(rows, step.events ?? []);
        since = step.next;
        if (!step.running && step.result) {
          finish(step.result);
          return;
        }
      } catch (err) {
        if (err instanceof ApiError && (err.status === 0 || err.status === 401)) {
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
    screen = next;
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

  function backToOptions() {
    notice = '';
    screen = op === 'install' ? 'options' : op === 'uninstall' ? 'uninstall' : 'welcome';
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
          <div class="content">
            {#if notice}<p class="notice" role="alert">{notice}</p>{/if}

            {#if screen === 'welcome'}
              <h1>{welcomeHeading(ui)}</h1>
              <div class="nutshell"><b>In a nutshell</b>
                {#if op === 'install'}Unravis reads audit logs and server health. It never changes your servers.
                {:else}Your cases, saved connections and recordings stay as they are. Only the program files change.{/if}
              </div>
              {#if checking && !checks.length}<p class="quiet">Checking this PC…</p>{:else}<CheckList {checks} />{/if}
              {@render closeCheckbox()}

            {:else if screen === 'options'}
              <h1>Where and how</h1>
              <fieldset class="scope">
                <legend class="sr">Who it is for</legend>
                <label class="tick"><input type="radio" name="scope" checked={options.scope === 'user'} onchange={() => setScope('user')} /> Just for me <span class="quiet">(no admin needed)</span></label>
                <label class="tick"><input type="radio" name="scope" checked={options.scope === 'machine'} onchange={() => setScope('machine')} /> For everyone on this PC <span class="quiet">(admin needed)</span></label>
              </fieldset>
              <label class="field">
                <span>Folder</span>
                <input type="text" class="mono" bind:value={options.location} aria-invalid={problemFor('location') ? 'true' : undefined} spellcheck="false" />
              </label>
              {#if problemFor('location')}<p class="problem">{problemFor('location')}</p>{/if}
              <label class="tick"><input type="checkbox" bind:checked={options.startAtSignIn} /> Start Unravis when I sign in</label>
              <label class="tick"><input type="checkbox" bind:checked={bbOn} /> Also place a Blackbox recorder on this PC</label>
              {#if bbOn}
                <div class="indent">
                  <div class="grid">
                    <label class="field"><span>Name</span><input type="text" bind:value={bb.name} aria-invalid={problemFor('bb-name') ? 'true' : undefined} spellcheck="false" /></label>
                    <label class="field wide"><span>Folder</span><input type="text" class="mono" bind:value={bb.folder} aria-invalid={problemFor('bb-folder') ? 'true' : undefined} spellcheck="false" /></label>
                    <label class="field"><span>Keep (days)</span><input type="number" min="1" max="365" bind:value={bb.keepDays} aria-invalid={problemFor('bb-keep') ? 'true' : undefined} /></label>
                    <label class="field"><span>Max (GB)</span><input type="number" min="0.1" step="0.1" bind:value={bb.maxGB} aria-invalid={problemFor('bb-max') ? 'true' : undefined} /></label>
                    <label class="field"><span>Keep free (%)</span><input type="number" min="1" max="50" bind:value={bb.minFreePercent} aria-invalid={problemFor('bb-free') ? 'true' : undefined} /></label>
                    <label class="field"><span>Port</span><input type="number" min="1" max="65535" bind:value={bb.port} aria-invalid={problemFor('bb-port') ? 'true' : undefined} /></label>
                  </div>
                  {#each problems.filter((problem) => problem.field.startsWith('bb-')) as problem (problem.field)}<p class="problem">{problem.message}</p>{/each}
                  <label class="tick"><input type="checkbox" bind:checked={fwOn} /> Allow other PCs to read it (firewall rule)</label>
                  {#if fwOn}
                    <label class="field indent"><span>Addresses</span><input type="text" class="mono" bind:value={fwText} placeholder="10.0.0.5, 10.0.1.0/24" aria-invalid={problemFor('firewall') ? 'true' : undefined} spellcheck="false" /></label>
                    {#if problemFor('firewall')}<p class="problem">{problemFor('firewall')}</p>{/if}
                  {/if}
                </div>
              {/if}
              {#if failedChecks.length}<CheckList checks={failedChecks} label="Not ready yet" />{/if}

            {:else if screen === 'approval'}
              <h1>Windows will ask for admin approval</h1>
              <div class="nutshell"><b>In a nutshell</b>
                {approvalReason(op, wanted(), ui.installed?.scope)} needs administrator rights. Windows shows its own prompt next; choose Yes to go on. Here is exactly what changes.
              </div>
              <div class="lists">
                <section><h2>Will change</h2><ul>{#each changes?.willChange ?? [] as item (item)}<li>{item}</li>{/each}</ul></section>
                <section><h2>Will not change</h2><ul>{#each changes?.willNotChange ?? [] as item (item)}<li>{item}</li>{/each}</ul></section>
              </div>

            {:else if screen === 'progress'}
              <h1>{progressHeading(op)}</h1>
              <div class="bar" role="progressbar" aria-label="Progress" aria-valuemin="0" aria-valuemax="100" aria-valuenow={progressPercent(rows)}>
                <span style:width={progressPercent(rows) + '%'}></span>
              </div>
              {#if rows.length}<StepList {rows} />{:else}<p class="quiet">Starting…</p>{/if}

            {:else if screen === 'done' && result}
              <h1>{doneHeading(op)}</h1>
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
                    <p class="quiet">Unravis on another PC needs this key to read this Blackbox. It is not saved anywhere you can see it again.</p>
                    <div class="key-row"><code class="mono">{result.readKey}</code><Button onclick={copyKey}>Copy</Button></div>
                    {#if copyNote}<p class="quiet" role="status">{copyNote}</p>{/if}
                    <label class="tick"><input type="checkbox" bind:checked={keySaved} /> I have saved the read key</label>
                  </section>
                {/if}
              {/if}
              {#if result.outcome === 'done-with-leftovers' && result.left?.length}
                <section><h2>Left behind</h2><ul class="plain mono">{#each result.left as item (item)}<li>{item}</li>{/each}</ul></section>
              {/if}
              {@render logLine()}

            {:else if screen === 'failed' && result}
              <h1>{failedHeading(result)}</h1>
              <p>{result.message}</p>
              <StepList rows={rows} fix={result.fix ?? ''} onretry={op === 'install' && result.outcome !== 'rollback-incomplete' ? run : undefined} />
              {#if result.left?.length}
                <section><h2>Left behind</h2><ul class="plain mono">{#each result.left as item (item)}<li>{item}</li>{/each}</ul></section>
              {/if}
              {@render logLine()}

            {:else if screen === 'uninstall'}
              <h1>Remove Unravis</h1>
              <div class="nutshell"><b>In a nutshell</b>
                Always removed: the program, its shortcuts and the Installed apps entry{everyone ? ', the key keeper service' : ''}{blackboxName ? ', the Blackbox service and its firewall rule' : ''}. Your data stays unless you tick it below.
              </div>
              <label class="tick"><input type="checkbox" bind:checked={uninst.deleteCases} /> {everyone ? 'Also delete the cases and saved connections of everyone on this PC' : 'Also delete my cases and saved connections'}</label>
              <div class="indent">
                {#if casesPlace}<p class="quiet"><span class="mono">{casesPlace.folder}</span> ({formatBytes(casesPlace.bytes)})</p>{/if}
                {#if uninst.deleteCases}
                  <label class="field"><span>Type {CONFIRM_DELETE_WORD} to confirm</span><input type="text" class="mono" bind:value={uninst.confirmCases} autocomplete="off" spellcheck="false" /></label>
                {/if}
              </div>
              {#if blackboxName}
                <label class="tick"><input type="checkbox" bind:checked={uninst.deleteRecordings} /> Also delete the Blackbox recordings</label>
                <div class="indent">
                  {#if recordingsPlace}<p class="quiet"><span class="mono">{recordingsPlace.folder}</span> ({formatBytes(recordingsPlace.bytes)})</p>{/if}
                  {#if uninst.deleteRecordings}
                    <label class="field"><span>Type the name {blackboxName} to confirm</span><input type="text" class="mono" bind:value={uninst.confirmRecordings} autocomplete="off" spellcheck="false" /></label>
                  {/if}
                </div>
              {/if}
              {@render closeCheckbox()}
              {#if failedChecks.length}<CheckList checks={failedChecks} label="Not ready yet" />{/if}
            {/if}
          </div>

          <footer>
            {#if screen === 'welcome'}
              <Button kind="quiet" onclick={() => { notice = ''; void check(); }} disabled={checking}>Check again</Button>
              <span class="grow"></span>
              <Button onclick={closeSetup}>Cancel</Button>
              <Button kind="primary" onclick={welcomeNext} disabled={checking || blocked(checks) || !checks.length}>{op === 'upgrade' ? 'Upgrade' : op === 'repair' ? 'Repair' : 'Next'}</Button>
            {:else if screen === 'options'}
              <span class="grow"></span>
              <Button onclick={() => { problems = []; screen = 'welcome'; }}>Back</Button>
              <Button kind="primary" onclick={optionsNext} disabled={checking}>Next</Button>
            {:else if screen === 'approval'}
              <span class="grow"></span>
              <Button onclick={backToOptions}>Back</Button>
              <Button kind="primary" onclick={run}>Continue</Button>
            {:else if screen === 'progress'}
              <span class="grow"></span>
              <Button onclick={cancelRun} disabled={cancelling}>{cancelling ? 'Cancelling…' : 'Cancel'}</Button>
            {:else if screen === 'done'}
              <span class="grow"></span>
              {#if op === 'uninstall'}
                <Button kind="primary" onclick={closeSetup}>Close</Button>
              {:else}
                <Button onclick={closeSetup} disabled={!canFinish(result, keySaved)}>Close</Button>
                <Button kind="primary" onclick={openApp} disabled={!canFinish(result, keySaved)}>Open Unravis</Button>
              {/if}
            {:else if screen === 'failed'}
              <span class="grow"></span>
              <Button onclick={closeSetup}>Close</Button>
              {#if op === 'install'}
                <Button kind="primary" onclick={backToOptions}>Back to Options</Button>
              {:else}
                <Button kind="primary" onclick={run} disabled={result?.outcome === 'rollback-incomplete'}>Try again</Button>
              {/if}
            {:else if screen === 'uninstall'}
              <span class="grow"></span>
              <Button onclick={closeSetup}>Cancel</Button>
              <Button kind="primary" onclick={removeNext} disabled={checking || !uninstallReady(uninst, blackboxName)}>Remove</Button>
            {/if}
          </footer>
        </main>
      </div>
    {/if}
  </div>
</div>

<style>
  .page { min-height: 100vh; display: grid; place-items: center; padding: 16px; }
  .frame { width: min(880px, 100%); height: 600px; display: flex; flex-direction: column; background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); overflow: hidden; }
  header { flex: 0 0 56px; height: 56px; display: flex; align-items: center; justify-content: space-between; padding: 0 20px; border-bottom: 1px solid var(--line); }
  .version { font-size: 12px; color: var(--text-2); }
  .body { flex: 1; min-height: 0; display: grid; grid-template-columns: 200px 1fr; }
  aside { padding: 16px 10px; border-right: 1px solid var(--line); background: var(--bg); }
  main { min-height: 0; display: flex; flex-direction: column; }
  main.solo { padding: 24px; }
  .content { flex: 1; min-height: 0; overflow: auto; padding: 20px 24px 16px; display: flex; flex-direction: column; gap: 12px; }
  footer { flex: 0 0 auto; display: flex; align-items: center; gap: 8px; padding: 12px 24px; border-top: 1px solid var(--line); }
  .grow { flex: 1; }
  h1 { margin: 0; font-size: 20px; font-weight: 600; line-height: 1.3; }
  h2 { margin: 0 0 6px; font-size: 13px; font-weight: 600; color: var(--text-2); }
  p { margin: 0; }
  .quiet { color: var(--text-2); }
  .nutshell { padding: 10px 12px; background: var(--surface-2); border-radius: var(--radius-sm); font-size: 13px; line-height: 1.5; }
  .nutshell b { display: block; margin-bottom: 2px; font-size: 12px; color: var(--text-2); }
  .notice { padding: 8px 12px; border-radius: var(--radius-sm); background: color-mix(in srgb, var(--warn) 14%, var(--surface)); font-size: 13px; }
  .problem { color: var(--bad); font-size: 12px; }
  .tick { display: flex; align-items: center; gap: 8px; font-size: 13px; }
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
  .key { display: grid; gap: 6px; padding: 10px 12px; border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .key h2 { margin: 0; }
  .key-row { display: flex; align-items: center; gap: 10px; }
  .key-row code { flex: 1; padding: 6px 8px; background: var(--surface-2); border-radius: var(--radius-sm); font-size: 14px; user-select: all; overflow-wrap: anywhere; }
  .log { display: flex; align-items: center; gap: 10px; font-size: 12px; }
  .log-label { color: var(--text-2); }
  .log-path { flex: 1; min-width: 0; overflow-wrap: anywhere; color: var(--text-2); }
  @media (max-width: 700px) {
    .body { grid-template-columns: 1fr; }
    aside { display: none; }
    .frame { height: auto; min-height: 600px; }
    .grid { grid-template-columns: 1fr 1fr; }
    .grid .wide { grid-column: span 2; }
    .lists { grid-template-columns: 1fr; }
  }
</style>
