<script lang="ts">
  import { tick } from 'svelte';
  import {
    NAME_MAX, blackboxApi, defaultSettings, nameProblem, packFileName, refusalText, saveBlob, settingsProblem,
    type BlackboxItem, type InstallAnswer, type Settings, type ThisPc
  } from '../lib/blackbox';
  import { BB_REFUSAL, BB_TEXT as T } from '../lib/copy/blackboxCopy';
  import SettingsFields from './BlackboxSettings.svelte';

  // The Place a Blackbox wizard: Name, Where, Settings, Review. Nothing is saved or changed until the last step.
  // On this PC the last step installs the service (Windows asks first). For another server it builds the pack zip.
  let { thisPc, canPack, onLeave, onPlaced }: {
    thisPc: ThisPc;
    canPack: boolean;
    onLeave: () => void;
    onPlaced: (entry: BlackboxItem | null) => void;
  } = $props();

  type Phase =
    | { kind: 'idle' }
    | { kind: 'working' }
    | { kind: 'installed'; answer: InstallAnswer }
    | { kind: 'pack'; blob: Blob; fileName: string }
    | { kind: 'failed'; text: string };

  const canInstallHere = $derived(thisPc.available && !(thisPc.installed));
  let part = $state(1);
  let name = $state('');
  let where = $state<'pc' | 'pack' | ''>('');
  let settings = $state<Settings>(defaultSettings());
  let logPathsText = $state(defaultSettings().logPaths.join('\n'));
  let problem = $state('');
  let phase = $state<Phase>({ kind: 'idle' });
  let heading = $state<HTMLElement>();

  const nameWords = $derived.by(() => {
    const p = nameProblem(name);
    return p ? T.nameProblems[p] : '';
  });
  const busy = $derived(phase.kind === 'working');
  const file = $derived(packFileName(name || 'name'));

  async function go(to: number) {
    problem = '';
    part = to;
    await tick();
    heading?.focus();
  }

  function toWhere() {
    if (nameProblem(name)) { problem = nameWords; return; }
    where = where || (canInstallHere ? 'pc' : canPack ? 'pack' : '');
    go(2);
  }
  function toSettings() {
    if (where === '') { problem = T.wherePcUnavailable; return; }
    go(3);
  }
  async function toReview() {
    settings.name = name;
    settings.logPaths = logPathsText.split('\n').map((s) => s.trim()).filter(Boolean);
    const first = settingsProblem(settings);
    if (first) { problem = T.settingProblems[first]; return; }
    try {
      await blackboxApi.validate($state.snapshot(settings) as Settings);
    } catch (error) {
      problem = refusalText(error, BB_REFUSAL);
      return;
    }
    go(4);
  }

  async function confirm() {
    if (busy) return;
    problem = '';
    const body = $state.snapshot(settings) as Settings;
    phase = { kind: 'working' };
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
      phase = { kind: 'failed', text: refusalText(error, BB_REFUSAL) };
    }
  }
  function again() {
    if (phase.kind === 'pack') saveBlob(phase.blob, phase.fileName);
  }
  function backToSettings() {
    phase = { kind: 'idle' };
    go(3);
  }
</script>

<div class="bb-wizard">
  <header class="bb-wizard-head">
    <h3 tabindex="-1" bind:this={heading}>{T.wizardTitle}</h3>
    <p class="bb-note">{T.wizardNothingSaved}</p>
    <ol class="bb-parts" aria-label={T.partOf(part)}>
      {#each T.partNames as label, i (label)}
        <li class:current={part === i + 1} class:past={part > i + 1} aria-current={part === i + 1 ? 'step' : undefined}><span class="bb-part-n">{i + 1}</span> {label}</li>
      {/each}
    </ol>
  </header>

  {#if phase.kind === 'installed'}
    <section class="bb-block" aria-label={T.installedTitle}>
      <h4>{T.installedTitle}</h4>
      <p role="status">{T.installedText(phase.answer.name)}</p>
      <p class="bb-note">{T.installedKeyNote}</p>
    </section>
    <div class="bb-actions"><button type="button" class="primary-button" onclick={() => onPlaced(phase.kind === 'installed' ? phase.answer.entry : null)}>{T.done}</button></div>
  {:else if phase.kind === 'pack'}
    <section class="bb-block" aria-label={T.packReadyTitle(phase.fileName)}>
      <h4>{T.packReadyTitle(phase.fileName)}</h4>
      <p role="status">{T.packInside}</p>
      <ol class="bb-steps">{#each T.packSteps as step, i (i)}<li>{step}</li>{/each}</ol>
      <p class="bb-note">{T.packKeyNote}</p>
      <p class="bb-note">{T.packBindNote}</p>
      <p class="bb-note">{T.packUninstall}</p>
    </section>
    <div class="bb-actions">
      <button type="button" class="primary-button" onclick={() => onPlaced(null)}>{T.packDone}</button>
      <button type="button" class="secondary-button" onclick={again}>{T.packAgain}</button>
    </div>
  {:else if part === 1}
    <section class="bb-block" aria-label={T.nameHeading}>
      <h4>{T.nameHeading}</h4>
      <label class="bb-field"><span>{T.nameLabel}</span>
        <input type="text" bind:value={name} maxlength={NAME_MAX + 8} autocomplete="off" spellcheck="false" aria-describedby="bb-name-help" aria-invalid={problem && nameWords ? 'true' : undefined} onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); toWhere(); } }} />
      </label>
      <p class="bb-note" id="bb-name-help">{T.nameHelp}</p>
    </section>
    {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
    <div class="bb-actions">
      <button type="button" class="primary-button" onclick={toWhere}>{T.nextWhere}</button>
      <button type="button" class="secondary-button" onclick={onLeave}>{T.wizardLeave}</button>
    </div>
  {:else if part === 2}
    <fieldset class="bb-block bb-choice">
      <legend><h4>{T.whereHeading}</h4></legend>
      <label class="bb-radio" class:off={!canInstallHere}>
        <input type="radio" name="bb-where" value="pc" bind:group={where} disabled={!canInstallHere} />
        <span><strong>{T.wherePc}</strong><span class="bb-note">{canInstallHere ? T.wherePcText : thisPc.installed ? T.whereAlready : T.wherePcUnavailable}</span></span>
      </label>
      <label class="bb-radio" class:off={!canPack}>
        <input type="radio" name="bb-where" value="pack" bind:group={where} disabled={!canPack} />
        <span><strong>{T.wherePack}</strong><span class="bb-note">{canPack ? T.wherePackText : T.wherePackUnavailable}</span></span>
      </label>
      {#if where === 'pack'}<p class="bb-note">{T.wherePackNote}</p>{/if}
    </fieldset>
    {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
    <div class="bb-actions">
      <button type="button" class="primary-button" disabled={where === ''} onclick={toSettings}>{T.nextSettings}</button>
      <button type="button" class="secondary-button" onclick={() => go(1)}>{T.backStep}</button>
    </div>
  {:else if part === 3}
    <SettingsFields bind:settings bind:logPathsText />
    {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
    <div class="bb-actions">
      <button type="button" class="primary-button" onclick={toReview}>{T.nextReview}</button>
      <button type="button" class="secondary-button" onclick={() => go(2)}>{T.backStep}</button>
    </div>
  {:else}
    <section class="bb-block" aria-label={T.reviewHeading}>
      <h4>{T.reviewHeading}</h4>
      <dl class="bb-facts">
        <div><dt>{T.reviewName}</dt><dd>{name}</dd></div>
        <div><dt>{T.reviewRuns}</dt><dd>{where === 'pc' ? T.reviewRunsPc : T.reviewRunsPack}</dd></div>
        <div><dt>{T.reviewSamples}</dt><dd>{settings.intervalSeconds} {T.seconds}</dd></div>
        <div><dt>{T.reviewKeeps}</dt><dd>{T.reviewKeepsText(settings.keepDays, settings.maxGB, settings.minFreePercent)}</dd></div>
        <div><dt>{T.reviewFolder}</dt><dd>{settings.folder}</dd></div>
        <div><dt>{T.reviewPort}</dt><dd>{settings.port}</dd></div>
        <div><dt>{T.reviewBind}</dt><dd>{settings.bind}</dd></div>
        <div><dt>{T.reviewProbe}</dt><dd>{T.reviewProbeOff}</dd></div>
      </dl>
      <p class="bb-note">{T.readOnly}</p>
      {#if where === 'pc'}<p class="bb-note">{T.windowsAsks}</p>{:else}<p class="bb-note">{T.packWill(file, name)}</p>{/if}
    </section>
    <div aria-live="polite">
      {#if busy}<p class="bb-note" role="status">{where === 'pc' ? T.installing : T.building}</p>{/if}
      {#if phase.kind === 'failed'}
        <div class="inline-error" role="alert"><strong>{T.installFailedTitle}</strong> {phase.text} {where === 'pc' ? T.installUndone : ''}</div>
      {/if}
      {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
    </div>
    <div class="bb-actions">
      <button type="button" class="primary-button" disabled={busy} onclick={confirm}>{where === 'pc' ? T.installButton : T.packButton}</button>
      {#if phase.kind === 'failed'}
        <button type="button" class="secondary-button" onclick={backToSettings}>{T.changeSettings}</button>
      {:else}
        <button type="button" class="secondary-button" disabled={busy} onclick={() => go(3)}>{T.backStep}</button>
      {/if}
    </div>
  {/if}

  {#if phase.kind !== 'installed' && phase.kind !== 'pack' && part !== 1}
    <div class="bb-leave"><button type="button" class="quiet-button" disabled={busy} onclick={onLeave}>{T.wizardLeave}</button></div>
  {/if}
</div>

<style>
  .bb-wizard { display: grid; gap: var(--space-4); min-width: 0; }
  .bb-wizard-head h3 { margin: 0; font-size: var(--fs-5); font-weight: 600; }
  .bb-wizard-head h3:focus-visible { outline-offset: 3px; }
  .bb-parts { display: flex; flex-wrap: wrap; gap: var(--space-2) var(--space-4); margin: var(--space-3) 0 0; padding: 0; list-style: none; font-size: 13px; color: var(--text-2); }
  .bb-parts li.current { color: var(--text); font-weight: 600; }
  .bb-part-n { display: inline-grid; place-items: center; width: 20px; height: 20px; border: 1px solid var(--line); border-radius: 50%; font-size: 12px; font-variant-numeric: tabular-nums; }
  .bb-parts li.current .bb-part-n { color: var(--accent-ink); background: var(--accent); border-color: var(--accent); }
  .bb-block { display: grid; gap: var(--space-2); min-width: 0; margin: 0; padding: 0; border: 0; }
  .bb-block h4 { margin: 0; font-size: 14px; font-weight: 600; }
  .bb-note { margin: 0; color: var(--text-2); font-size: 12px; line-height: 1.5; overflow-wrap: anywhere; }
  .bb-field { display: grid; gap: 4px; font-size: 13px; font-weight: 500; }
  .bb-field input[type='text'] { width: 100%; max-width: 420px; padding: 6px 8px; font: inherit; font-weight: 400; color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); box-sizing: border-box; }
  .bb-choice legend { padding: 0; }
  .bb-radio { display: flex; align-items: flex-start; gap: 10px; padding: 10px 0; border-top: 1px solid var(--line); font-size: 14px; }
  .bb-radio.off { opacity: .6; }
  .bb-radio input { flex: 0 0 auto; margin: 3px 0 0; }
  .bb-radio > span { display: grid; gap: 2px; min-width: 0; }
  .bb-facts { display: grid; gap: 4px; margin: 0; font-size: 13px; }
  .bb-facts div { display: flex; gap: 10px; min-width: 0; }
  .bb-facts dt { flex: 0 0 90px; color: var(--text-2); }
  .bb-facts dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
  .bb-steps { margin: 0; padding-left: 20px; display: grid; gap: 4px; font-size: 13px; line-height: 1.5; }
  .bb-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .bb-leave { margin-top: calc(var(--space-2) * -1); }
</style>
