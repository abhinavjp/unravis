<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { clockTime, fmtClock, MINUTE_CHIPS, parseMinutes, STEP_RESULT } from '../lib/codeMode';
  import { codeModeFlow as flow } from '../lib/codeModeFlow.svelte';
  import { codeModeStore as store } from '../lib/codeModeStore.svelte';
  import { closeCodeMode } from '../lib/codeModeUi.svelte';
  import { openSidePanel } from '../lib/appState.svelte';
  import { CODE_TEXT as T, NO_LIMIT_PHRASE } from '../lib/copy/codeModeCopy';
  import CodeIcon from './CodeIcon.svelte';

  // The code-mode popup (prototype: web/prototype/code-mode-settings.html, approved 2026-11-26): seven steps, one question on each,
  // nothing saved until the Review step is confirmed. The logic is CodeModeFlow; this file is the markup. What the Result step shows
  // is the server's last answer from the store, never the draft.

  const FOCUSABLE = 'button:not(:disabled), input:not(:disabled), select:not(:disabled), [href], [tabindex]:not([tabindex="-1"])';

  let dlgEl = $state<HTMLElement>();
  let stageEl = $state<HTMLElement>();
  let typedEl = $state<HTMLInputElement>();
  let zeroNote = $state(false);
  let landed = store.status === 'ready';

  const ready = $derived(store.status === 'ready' && store.settings !== null);
  const settings = $derived(store.settings);
  const cli = $derived(store.cli);
  const step = $derived(flow.step);
  const draft = $derived(flow.draft);
  const showKeep = $derived((settings?.enabled === true && (cli.kind === 'open' || cli.kind === 'unlimited')) || draft.mode === 'keep');
  const keepState = $derived(cli.kind === 'unlimited' ? T.badgeNoLimit : cli.kind === 'open' ? T.badgeLeft(fmtClock(cli.leftMs)) : T.badgeShut);
  const leftMinutes = $derived(Math.max(1, Math.ceil(cli.leftMs / 60000)));
  const untilClock = $derived(settings && settings.cliOpenUntil > 0 ? clockTime(settings.cliOpenUntil - store.offsetMs) : '');
  const allowedCount = $derived(flow.allowedAssistants.length);

  // Put the keyboard on the new step's title after every step change (and when the answer first arrives).
  $effect(() => {
    void step; void store.status;
    void tick().then(() => {
      if (stageEl) stageEl.scrollTop = 0;
      dlgEl?.querySelector<HTMLElement>('#cm-title')?.focus({ preventScroll: true });
    });
  });

  // The popup can be opened before the first read has come back: land on the right step once it has.
  $effect(() => {
    if (!landed && store.status === 'ready') {
      landed = true;
      if (flow.step === 0 && flow.furthest === 0) flow.open();
    }
  });

  // A timed window that ends while the Result step is on screen says so.
  let lastKind = "";
  $effect(() => {
    const kind = store.cli.kind;
    if (lastKind === 'open' && kind === 'shut' && flow.step === STEP_RESULT) flow.notice = T.windowEnded;
    lastKind = kind;
  });

  onMount(() => { void store.refresh(); });

  function onKey(event: KeyboardEvent) {
    if (event.key === 'Escape') { event.preventDefault(); void closeCodeMode(); return; }
    if (event.key !== 'Tab' || !dlgEl) return;
    const items = [...dlgEl.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((element) => element.offsetParent !== null);
    if (!items.length) return;
    const first = items[0], last = items[items.length - 1];
    const active = document.activeElement;
    const outside = !(active instanceof Node) || active === dlgEl || !dlgEl.contains(active);
    if (event.shiftKey) {
      if (outside || active === first) { event.preventDefault(); last.focus(); }
    } else if (outside || active === last) {
      event.preventDefault(); first.focus();
    }
  }

  async function openAiAccess() {
    await closeCodeMode();
    openSidePanel('mcp');
  }

  async function pickMode(mode: 'keep' | 'shut' | 'timed' | 'unlimited') {
    zeroNote = false;
    flow.setMode(mode);
    if (mode === 'unlimited') { await tick(); typedEl?.scrollIntoView({ block: 'nearest' }); }
  }

  async function typeMinutes(raw: string) {
    zeroNote = flow.setMinutes(raw);
    if (zeroNote) {
      await tick();
      dlgEl?.querySelector<HTMLInputElement>('input[name="cli"][value="shut"]')?.focus();
    }
  }

  const stepNo = $derived(step === STEP_RESULT ? T.resultNo : T.stepNo(step + 1, T.steps[step] ?? ''));

  /** The rows of the Review step: label, value, sub line, the step a Change goes to, and whether the value is a danger. */
  const reviewRows = $derived.by(() => {
    const rows: { label: string; value: string; sub: string; go: number; bad: boolean }[] = [
      { label: T.rowCode, value: draft.enabled ? T.on : T.off, sub: draft.enabled ? '' : T.offSub, go: 1, bad: false }
    ];
    if (draft.enabled) {
      const names = flow.allowedAssistants.map((a) => a.name).join(', ');
      const value = flow.assistantsStatus === 'loading' || flow.assistantsStatus === 'idle' ? T.s2Loading : flow.assistantsStatus === 'error' ? T.s2Unreadable : names || T.stateOnly;
      const summary = flow.cliSummary;
      rows.push(
        { label: T.rowAssistants, value, sub: T.assistantsSub, go: 2, bad: false },
        { label: T.rowKeep, value: draft.keep ? T.kept : T.notKept, sub: '', go: 3, bad: false },
        { label: T.rowDeg, value: draft.deg ? T.allowed : T.notAllowed, sub: '', go: 3, bad: false },
        { label: T.rowCli, value: summary.value, sub: summary.sub, go: 4, bad: summary.danger }
      );
    }
    return rows;
  });

  const TAG_OF = { allowed: 'on', notAllowed: '', waiting: 'warn', gone: 'hatch' } as const;
  const TAG_WORDS = { allowed: T.s2Allowed, notAllowed: T.s2NotAllowed, waiting: T.s2Waiting, gone: T.s2Gone } as const;
</script>

<svelte:window onkeydowncapture={onKey} />

{#snippet radio(name: string, value: string, title: string, desc: string, checked: boolean, pick: () => void, cls: string, icon: boolean, disabled: boolean)}
  <label class="cm-opt {cls}" class:cm-dim={disabled}>
    <input type="radio" {name} {value} {checked} {disabled} onchange={pick} />
    <div>
      <div class="cm-opt-title">{#if icon}<CodeIcon name="alert" />{/if}{title}</div>
      <p class="cm-opt-desc">{desc}</p>
    </div>
  </label>
{/snippet}

{#snippet dangerCallout(title: string, body: string, points: readonly string[])}
  <div class="cm-callout bad" role="group" aria-label={title}>
    <div class="cm-callout-row">
      <CodeIcon name="alert" />
      <div>
        <p><strong>{title}</strong></p>
        <p class="cm-gap">{body}</p>
        {#if points.length}<ul>{#each points as point}<li>{point}</li>{/each}</ul>{/if}
      </div>
    </div>
  </div>
{/snippet}

<button type="button" class="cm-scrim" tabindex="-1" aria-label={T.closeLabel} onclick={() => void closeCodeMode()}></button>
<div class="cm-dlg" role="dialog" aria-modal="true" aria-labelledby="cm-title" data-testid="code-popup" bind:this={dlgEl}>
  <nav class="cm-rail" aria-label={T.stepsLabel}>
    <h1>{T.title}</h1>
    <ol>
      {#each T.steps as name, i}
        {@const skipped = ready && flow.skipped(i)}
        {@const done = ready && i < step && !skipped}
        <li class:cur={i === step} class:done class:skip={skipped}>
          <button type="button" disabled={!ready || !flow.reachable(i)} aria-current={i === step ? 'step' : undefined} onclick={() => flow.go(i)}>
            <span class="cm-num">{#if done}<CodeIcon name="check" size={13} />{:else}{i + 1}{/if}</span>
            <span class="cm-lbl">{name}{#if skipped}<small>{T.skipped}</small>{/if}</span>
          </button>
        </li>
      {/each}
    </ol>
  </nav>

  <div class="cm-main">
    <div class="cm-top">
      <span class="cm-stepno">{ready ? stepNo : T.title}</span>
      <button type="button" class="cm-x" aria-label={T.closeLabel} onclick={() => void closeCodeMode()}><CodeIcon name="x" /></button>
    </div>

    <div class="cm-stage" bind:this={stageEl}>
      <div class="cm-live" role="status">{#if flow.notice}<p class="cm-notice"><CodeIcon name="check" />{flow.notice}</p>{/if}</div>
      {#if flow.saveError}
        <div class="cm-callout bad" role="alert"><div class="cm-callout-row"><CodeIcon name="alert" /><p>{flow.saveError}</p></div></div>
      {/if}
      {#if ready && store.error}
        <div class="cm-callout warn"><div class="cm-callout-row"><CodeIcon name="alert" /><p>{store.error}</p></div></div>
      {/if}

      {#if store.status === 'unavailable'}
        <h2 tabindex="-1" id="cm-title">{T.unavailableTitle}</h2>
        <p class="cm-lede">{T.unavailableBody}</p>
      {:else if store.status === 'error' && !settings}
        <h2 tabindex="-1" id="cm-title">{T.refreshFailed}</h2>
        {#if store.error}<p class="cm-lede">{store.error}</p>{/if}
        <div class="cm-actions"><button type="button" class="secondary-button cm-btn" onclick={() => void store.refresh()}>{T.retry}</button></div>
      {:else if !ready || !settings}
        <h2 tabindex="-1" id="cm-title">{T.loadingTitle}</h2>
      {:else}
        {#key step}
          <div class="cm-enter">
            {#if step === 0}
              <h2 tabindex="-1" id="cm-title">{T.s0Title}</h2>
              <p class="cm-lede">{T.s0Lede}</p>
              {#if settings.damaged}
                <div class="cm-callout bad" role="alert"><div class="cm-callout-row"><CodeIcon name="alert" /><p><strong>{T.damagedTitle}</strong> {T.damagedBody}</p></div></div>
              {/if}
              <ul class="cm-facts">
                {#each T.facts(settings.allowIdentifiers, draft.deg) as fact}
                  <li><CodeIcon name={fact.icon as 'box' | 'eye' | 'lock'} /><div><strong>{fact.title}</strong><p>{fact.body}</p></div></li>
                {/each}
              </ul>
              <div class="cm-stat"><CodeIcon name="term" /><div><p><strong>{T.runnerTitle}</strong></p><p class="cm-sub">{T.runnerBody}</p></div></div>
              <p class="cm-fine">{T.fineOff}</p>

            {:else if step === 1}
              <h2 tabindex="-1" id="cm-title">{T.s1Title}</h2>
              <p class="cm-lede">{T.s1Lede}</p>
              <div class="cm-opts" role="radiogroup" aria-label={T.radioLabel}>
                {@render radio('on', 'off', T.offTitle, T.offBody, !draft.enabled, () => flow.setEnabled(false), '', false, false)}
                {@render radio('on', 'on', T.onTitle, T.onBody, draft.enabled, () => flow.setEnabled(true), '', false, false)}
              </div>
              {#if !draft.enabled}<p class="cm-fine">{T.skipNote}</p>{/if}

            {:else if step === 2}
              <h2 tabindex="-1" id="cm-title">{T.s2Title}</h2>
              <p class="cm-lede">{T.s2Lede}</p>
              {#if flow.assistantsStatus === 'loading' || flow.assistantsStatus === 'idle'}
                <p class="cm-fine" role="status">{T.s2Loading}</p>
              {:else if flow.assistantsStatus === 'error'}
                <p class="cm-fine" role="status">{T.s2Unreadable}</p>
                <div class="cm-actions"><button type="button" class="secondary-button cm-btn" onclick={() => void flow.loadAssistants()}>{T.retry}</button></div>
              {:else if flow.assistants.length === 0}
                <p class="cm-fine">{T.s2None}</p>
              {:else}
                <ul class="cm-who">
                  {#each flow.assistants as who (who.id)}
                    <li class:gone={who.tone === 'gone'}>
                      <div>
                        <div class="cm-who-name">{who.name}<span class="cm-tag {TAG_OF[who.tone]}" class:hatch={who.tone === 'gone'}><span class="cm-dot"></span>{TAG_WORDS[who.tone]}</span></div>
                        {#if who.meta}<p class="cm-who-meta">{who.meta}</p>{/if}
                      </div>
                    </li>
                  {/each}
                </ul>
              {/if}
              <p class="cm-fine cm-above">{T.s2Fine}</p>
              <div class="cm-actions"><button type="button" class="secondary-button cm-btn" onclick={() => void openAiAccess()}>{T.s2Open}</button></div>

            {:else if step === 3}
              <h2 tabindex="-1" id="cm-title">{T.s3Title}</h2>
              <p class="cm-lede">{T.s3Lede}</p>
              <div class="cm-rows">
                <div class="cm-row">
                  <div><div class="cm-row-title" id="cm-keep">{T.keepTitle}</div><p class="cm-row-desc">{T.keepBody}</p></div>
                  <button type="button" class="cm-switch" role="switch" aria-checked={draft.keep} aria-labelledby="cm-keep" onclick={() => flow.setKeep(!draft.keep)}><span class="visually-hidden">{draft.keep ? T.switchOn : T.switchOff}</span></button>
                </div>
                <div class="cm-row">
                  <div><div class="cm-row-title" id="cm-deg">{T.degTitle}</div><p class="cm-row-desc"><span class="cm-careful">{T.degCareful}</span>{T.degBody}</p></div>
                  <button type="button" class="cm-switch" role="switch" aria-checked={draft.deg} aria-labelledby="cm-deg" onclick={() => flow.setDeg(!draft.deg)}><span class="visually-hidden">{draft.deg ? T.switchOn : T.switchOff}</span></button>
                </div>
                <div class="cm-row">
                  <div><div class="cm-row-title">{T.idsTitle}</div><p class="cm-row-desc">{settings.allowIdentifiers ? T.idsBodyAllowed : T.idsBodyMasked}</p></div>
                  <span class="cm-fact"><CodeIcon name="lock" />{settings.allowIdentifiers ? T.idsAllowed : T.idsMasked}</span>
                </div>
              </div>

            {:else if step === 4}
              <h2 tabindex="-1" id="cm-title">{T.s4Title}</h2>
              <p class="cm-lede">{T.s4Lede}</p>
              <div class="cm-opts" role="radiogroup" aria-label={T.cliGroup}>
                {#if showKeep}{@render radio('cli', 'keep', T.keepAsIsTitle(keepState), T.keepAsIs, draft.mode === 'keep', () => void pickMode('keep'), '', false, false)}{/if}
                {@render radio('cli', 'shut', T.shutTitle(!showKeep), T.shutBody, draft.mode === 'shut', () => void pickMode('shut'), '', false, false)}
                {@render radio('cli', 'timed', T.timedTitle, T.timedBody, draft.mode === 'timed', () => void pickMode('timed'), '', false, false)}
              </div>
              {#if draft.mode === 'timed'}
                <div class="cm-time">
                  <div class="cm-pick" role="group" aria-label={T.minutesGroup}>
                    {#each MINUTE_CHIPS as m}
                      <button type="button" class="cm-chip" aria-pressed={parseMinutes(draft.minsRaw) === m} onclick={() => flow.pickMinutes(m)}>{T.chip(m)}</button>
                    {/each}
                    <label class="cm-numin">{T.minutesOr}
                      <input inputmode="numeric" autocomplete="off" value={draft.minsRaw} aria-invalid={flow.minsError ? 'true' : 'false'} aria-describedby="cm-mins-err cm-mins-hint" oninput={(event) => void typeMinutes(event.currentTarget.value)} />
                      {T.minutesUnit}
                    </label>
                  </div>
                  <p class="cm-err" id="cm-mins-err" role="alert">{flow.minsError}</p>
                  <p class="cm-fine" id="cm-mins-hint">{T.minutesHint}</p>
                </div>
              {/if}
              {#if zeroNote}<p class="cm-fine cm-above" role="status">{T.zeroNote}</p>{/if}
              <div class="cm-opts cm-above">
                {@render radio('cli', 'unlimited', T.noLimitTitle, flow.hasUnlimited ? T.noLimitBody : T.noLimitBody + ' ' + T.noLimitSoon, draft.mode === 'unlimited', () => void pickMode('unlimited'), 'bad', true, !flow.hasUnlimited)}
              </div>
              {#if draft.mode === 'unlimited'}
                {@render dangerCallout(T.dangerTitle, T.dangerBody, T.dangerPoints)}
                <div class="cm-typed">
                  <label for="cm-typed">{T.typedLabel} <code>{NO_LIMIT_PHRASE}</code></label>
                  <input id="cm-typed" autocomplete="off" spellcheck="false" class:ok={flow.typedOk} value={draft.typed} aria-describedby="cm-typed-hint" bind:this={typedEl} oninput={(event) => flow.setTyped(event.currentTarget.value)} />
                  <p class="cm-fine" id="cm-typed-hint">{flow.typedOk ? T.typedHintYes : T.typedHintNo}</p>
                </div>
              {/if}

            {:else if step === 5}
              <h2 tabindex="-1" id="cm-title">{T.s5Title}</h2>
              <p class="cm-lede">{T.s5Lede}</p>
              <dl class="cm-sum">
                {#each reviewRows as row (row.label)}
                  <div>
                    <dt>{row.label}</dt>
                    <dd class:bad={row.bad}>{#if row.bad}<CodeIcon name="alert" />{/if}{row.value}{#if row.sub}<span class="cm-sub-line">{row.sub}</span>{/if}</dd>
                    <div><button type="button" class="quiet-button cm-btn" aria-label={T.changeLabel(row.label)} onclick={() => flow.go(row.go)}>{T.change}</button></div>
                  </div>
                {/each}
              </dl>
              {#if draft.enabled && draft.mode === 'unlimited'}
                <div class="cm-callout bad"><div class="cm-callout-row"><CodeIcon name="alert" /><p><strong>{T.reviewNoLimitTitle}</strong> {T.reviewNoLimitBody}</p></div></div>
              {/if}

            {:else}
              {#if !settings.enabled}
                <h2 tabindex="-1" id="cm-title">{T.offResultTitle}</h2>
                <p class="cm-lede">{T.offResultLede}</p>
                {#if settings.damaged}
                  <div class="cm-callout bad" role="alert"><div class="cm-callout-row"><CodeIcon name="alert" /><p><strong>{T.offDamagedTitle}</strong> {T.offDamagedBody}</p></div></div>
                {/if}
                <div class="cm-actions"><button type="button" class="primary-button cm-btn" onclick={() => flow.go(1)}>{T.setUp}</button></div>
              {:else}
                <h2 tabindex="-1" id="cm-title">{T.onResultTitle}</h2>
                <div class="cm-state">
                  <span class="cm-tag on"><span class="cm-dot"></span>{T.on}</span>
                  {#if flow.assistantsStatus === 'ready'}<span class="cm-state-big">{allowedCount ? T.stateAssistants(allowedCount) : T.stateOnly}</span>{/if}
                </div>
                <div class="cm-blk">
                  <h3>{T.cliHeading}</h3>
                  {#if cli.kind === 'unlimited'}
                    {@render dangerCallout(T.noLimitNowTitle, T.noLimitNowBody, [])}
                    <div class="cm-actions">
                      <button type="button" class="primary-button cm-btn" disabled={flow.saving} onclick={() => void flow.quick('tofifteen')}>{T.fifteen}</button>
                      <button type="button" class="secondary-button cm-btn" disabled={flow.saving} onclick={() => void flow.quick('closecli')}>{T.closeNow}</button>
                    </div>
                  {:else if cli.kind === 'open'}
                    <div class="cm-timer">
                      <span class="cm-big" class:warn={cli.leftMs < 60000} aria-hidden="true">{fmtClock(cli.leftMs)}</span>
                      {#if untilClock}<span class="cm-until">{T.openUntil(untilClock)}</span>{/if}
                    </div>
                    <span class="visually-hidden" role="timer">{T.minutesLeft(leftMinutes)}</span>
                    <div class="cm-actions">
                      <button type="button" class="secondary-button cm-btn" disabled={flow.saving} onclick={() => void flow.quick('closecli')}>{T.closeNow}</button>
                      <button type="button" class="quiet-button cm-btn" onclick={() => flow.go(4)}>{T.changeWindow}</button>
                    </div>
                  {:else}
                    <p class="cm-lede cm-tight">{T.cliShutLede}</p>
                    <div class="cm-actions"><button type="button" class="secondary-button cm-btn" onclick={() => flow.go(4)}>{T.openIt}</button></div>
                  {/if}
                </div>
                <div class="cm-blk">
                  <h3>{T.offHeading}</h3>
                  <p class="cm-fine">{T.offHow}</p>
                </div>
              {/if}
            {/if}
          </div>
        {/key}
      {/if}
    </div>

    {#if ready}
      <footer class="cm-foot">
        {#if step === STEP_RESULT}
          {#if settings?.enabled}
            <button type="button" class="secondary-button cm-btn cm-danger" disabled={flow.saving} onclick={() => void flow.quick('off')}>{flow.saving ? T.saving : T.turnOff}</button>
            <button type="button" class="quiet-button cm-btn" onclick={() => flow.go(1)}>{T.changeSettings}</button>
          {/if}
          <span class="cm-grow"></span>
          <button type="button" class="primary-button cm-btn" onclick={() => void closeCodeMode()}>{T.done}</button>
        {:else}
          {#if step > 0}<button type="button" class="secondary-button cm-btn" disabled={flow.saving} onclick={() => flow.back()}>{T.back}</button>{/if}
          <span class="cm-grow"></span>
          {#if step === 5}
            <button type="button" class="primary-button cm-btn" disabled={flow.saving} onclick={() => void flow.commit()}>{flow.saving ? T.saving : flow.confirmLabel}</button>
          {:else}
            <button type="button" class="primary-button cm-btn" disabled={!flow.nextOn} onclick={() => flow.next()}>{step === 1 && !draft.enabled ? T.nextReview : T.next}</button>
          {/if}
        {/if}
      </footer>
    {/if}
  </div>
</div>

<style>
  .cm-scrim { position: fixed; inset: 0; z-index: 40; width: 100%; height: 100%; padding: 0; border: 0; background: var(--scrim); cursor: default; animation: cm-fade 200ms var(--ease) both; }
  .cm-dlg {
    position: fixed; z-index: 45; left: 50%; top: 50%; transform: translate(-50%, -50%);
    width: min(840px, calc(100vw - 32px)); height: min(660px, calc(100dvh - 32px));
    display: grid; grid-template-columns: 216px minmax(0, 1fr); overflow: hidden;
    color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius);
    box-shadow: 0 24px 60px -16px color-mix(in srgb, var(--bg) 85%, transparent); animation: cm-rise 280ms var(--ease) both;
  }
  @keyframes cm-rise { from { opacity: 0; transform: translate(-50%, calc(-50% + 12px)); } to { opacity: 1; transform: translate(-50%, -50%); } }
  @keyframes cm-fade { from { opacity: 0; } to { opacity: 1; } }

  /* The list of steps */
  .cm-rail { display: flex; flex-direction: column; gap: var(--space-5); padding: var(--space-6) var(--space-4); overflow: auto; background: var(--bg); border-right: 1px solid var(--line); }
  .cm-rail h1 { margin: 0 0 0 var(--space-2); font-size: 18px; font-weight: 600; letter-spacing: -.01em; }
  .cm-rail ol { display: grid; gap: 2px; margin: 0; padding: 0; list-style: none; }
  .cm-rail li button { display: grid; grid-template-columns: 22px 1fr; gap: var(--space-2); align-items: center; width: 100%; min-height: 36px; padding: var(--space-2); text-align: left; color: var(--text-2); background: transparent; border: 0; border-radius: var(--radius-sm); font-size: 14px; }
  .cm-rail li button:disabled { cursor: default; opacity: .7; }
  .cm-rail li button:hover:not(:disabled) { color: var(--text); background: var(--surface-2); }
  .cm-rail li.cur button { color: var(--text); background: var(--surface-2); font-weight: 600; opacity: 1; }
  .cm-num { display: grid; place-items: center; width: 22px; height: 22px; color: var(--text-2); border: 1px solid var(--control-line); border-radius: 50%; font-family: var(--mono); font-size: 12px; }
  li.cur .cm-num { color: var(--accent); border-color: var(--accent); }
  li.done .cm-num { color: var(--accent-ink); background: var(--accent); border-color: var(--accent); }
  li.skip .cm-num { border-style: dashed; }
  .cm-lbl small { display: block; color: var(--text-2); font-size: 12px; font-weight: 400; }

  /* The page: a top line, the step, and the buttons */
  .cm-main { display: flex; flex-direction: column; min-width: 0; min-height: 0; }
  .cm-top { display: flex; align-items: center; justify-content: space-between; min-height: 48px; padding: var(--space-3) var(--space-4) 0 var(--space-8); }
  .cm-stepno { color: var(--text-2); font-size: 13px; }
  .cm-x { display: grid; place-items: center; width: 36px; height: 36px; padding: 0; color: var(--text-2); background: transparent; border: 1px solid transparent; border-radius: var(--radius-sm); }
  .cm-x:hover { color: var(--text); background: var(--surface-2); }
  .cm-stage { flex: 1 1 auto; min-height: 0; overflow: auto; padding: var(--space-2) var(--space-8) var(--space-6); scrollbar-width: thin; scrollbar-color: var(--control-line) transparent; }
  .cm-enter { animation: cm-fade 220ms var(--ease) both; }
  .cm-stage h2 { margin: 0; font-size: 22px; line-height: 1.25; font-weight: 600; letter-spacing: -.015em; }
  .cm-stage h2:focus { outline: none; }
  .cm-lede { max-width: 62ch; margin: var(--space-3) 0 var(--space-5); color: var(--text-2); text-wrap: pretty; }
  .cm-lede.cm-tight { margin-bottom: var(--space-3); }
  .cm-foot { display: flex; align-items: center; gap: var(--space-3); padding: var(--space-4) var(--space-8); border-top: 1px solid var(--line); }
  .cm-grow { flex: 1; }
  .cm-stage code, .cm-typed code { padding: 1px 6px; background: var(--surface-2); border-radius: var(--radius-sm); font-family: var(--mono); font-size: 12.5px; }

  /* Buttons: the app's own button classes, at the size of the prototype */
  .cm-btn { min-height: 40px; padding: 0 var(--space-5); font-size: 14px; font-weight: 500; }
  .cm-btn.quiet-button { padding: 0 var(--space-3); }
  .cm-danger { color: var(--bad); border-color: var(--bad); }
  .cm-danger:hover:not(:disabled) { color: var(--bad); border-color: var(--bad); }

  /* Messages */
  .cm-live { min-height: 0; }
  .cm-notice { display: flex; align-items: center; gap: var(--space-2); margin: var(--space-2) 0 var(--space-3); color: var(--accent); font-size: 13px; font-weight: 500; }
  .cm-callout { margin: var(--space-2) 0 var(--space-4); padding: var(--space-3) var(--space-4); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .cm-callout p { margin: 0; }
  .cm-callout.bad { border-color: var(--bad); background: color-mix(in srgb, var(--bad) 10%, var(--surface)); }
  .cm-callout.warn { border-color: var(--warn); background: color-mix(in srgb, var(--warn) 10%, var(--surface)); }
  .cm-callout-row { display: flex; align-items: flex-start; gap: var(--space-3); }
  .cm-callout-row :global(.code-icon) { margin-top: 2px; }
  .cm-callout.bad :global(.code-icon) { color: var(--bad); }
  .cm-callout.warn :global(.code-icon) { color: var(--warn); }
  .cm-callout ul { margin: var(--space-2) 0 0; padding-left: var(--space-5); }
  .cm-callout li + li { margin-top: var(--space-1); }
  .cm-gap { margin-top: var(--space-2) !important; }
  .cm-fine { max-width: 60ch; margin: 0; color: var(--text-2); font-size: 13px; }
  .cm-above { margin-top: var(--space-4); }
  .cm-err { margin: 0; color: var(--bad); font-size: 13px; }
  .cm-err:empty { display: none; }
  .cm-sub { max-width: 56ch; margin: 2px 0 0; color: var(--text-2); font-size: 13px; }
  .cm-stat { display: grid; grid-template-columns: auto 1fr; gap: var(--space-3); align-items: start; margin: 0 0 var(--space-4); }
  .cm-stat :global(.code-icon) { margin-top: 2px; color: var(--text-2); }
  .cm-stat p { margin: 0; }

  .cm-facts { display: grid; gap: var(--space-4); margin: 0 0 var(--space-5); padding: 0; list-style: none; }
  .cm-facts li { display: grid; grid-template-columns: 20px 1fr; gap: var(--space-3); }
  .cm-facts :global(.code-icon) { margin-top: 2px; color: var(--text-2); }
  .cm-facts p { max-width: 58ch; margin: 0; color: var(--text-2); }

  /* Choices */
  .cm-opts { display: grid; gap: var(--space-2); margin: 0 0 var(--space-4); }
  .cm-opt { display: grid; grid-template-columns: 20px 1fr; gap: var(--space-3); padding: var(--space-4); border: 1px solid var(--line); border-radius: var(--radius-sm); cursor: pointer; transition: border-color var(--dur) var(--ease), background var(--dur) var(--ease); }
  .cm-opt:hover { background: var(--surface-2); }
  .cm-opt input { width: 18px; height: 18px; margin: 2px 0 0; accent-color: var(--accent); }
  .cm-opt-title { display: flex; align-items: center; flex-wrap: wrap; gap: var(--space-2); font-weight: 500; }
  .cm-opt-desc { max-width: 56ch; margin: 2px 0 0; color: var(--text-2); font-size: 13px; }
  .cm-opt:has(input:checked) { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 10%, var(--surface)); }
  .cm-opt.bad:has(input:checked) { border-color: var(--bad); background: color-mix(in srgb, var(--bad) 10%, var(--surface)); }
  .cm-opt.bad input { accent-color: var(--bad); }
  .cm-opt.bad :global(.code-icon) { color: var(--bad); }
  .cm-opt:has(input:focus-visible) { outline: 2px solid var(--focus); outline-offset: 2px; }
  .cm-opt.cm-dim { cursor: not-allowed; opacity: .7; }
  .cm-opt.cm-dim:hover { background: transparent; }

  .cm-tag { display: inline-flex; align-items: center; gap: 6px; padding: 2px 10px; color: var(--text-2); border: 1px solid var(--control-line); border-radius: 999px; font-size: 12px; font-weight: 500; white-space: nowrap; }
  .cm-tag.on { color: var(--accent); border-color: var(--accent); background: color-mix(in srgb, var(--accent) 10%, var(--surface)); }
  .cm-tag.warn { color: var(--warn); border-color: var(--warn); }
  .cm-dot { width: 7px; height: 7px; border-radius: 50%; background: currentColor; }
  .cm-tag.hatch .cm-dot { background: repeating-linear-gradient(45deg, currentColor 0 2px, transparent 2px 4px); border: 1px solid currentColor; }

  /* The time box and the typed phrase */
  .cm-time { display: grid; gap: var(--space-2); margin: var(--space-3) 0 0 32px; }
  .cm-pick { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .cm-chip { min-width: 64px; min-height: 40px; padding: 0 var(--space-4); color: var(--text); background: transparent; border: 1px solid var(--control-line); border-radius: var(--radius-sm); font-size: 14px; transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease); }
  .cm-chip:hover { background: var(--surface-2); }
  .cm-chip[aria-pressed='true'] { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 10%, var(--surface)); font-weight: 600; }
  .cm-numin { display: inline-flex; align-items: center; gap: var(--space-2); margin-left: var(--space-2); color: var(--text-2); }
  .cm-numin input, .cm-typed input { min-height: 40px; color: var(--text); background: var(--surface); border: 1px solid var(--control-line); border-radius: var(--radius-sm); font-family: var(--mono); font-size: 14px; }
  .cm-numin input { width: 76px; text-align: center; }
  .cm-numin input:focus, .cm-typed input:focus { outline: none; border-color: var(--accent); box-shadow: 0 0 0 3px color-mix(in srgb, var(--accent) 10%, var(--surface)); }
  .cm-numin input[aria-invalid='true'] { border-color: var(--bad); }
  .cm-typed { display: grid; gap: var(--space-2); margin: var(--space-4) 0 0; }
  .cm-typed label { font-weight: 500; }
  .cm-typed input { width: min(320px, 100%); padding: 0 var(--space-3); }
  .cm-typed input.ok { border-color: var(--bad); }

  /* Switch rows, assistants */
  .cm-rows { display: grid; }
  .cm-row { display: grid; grid-template-columns: 1fr auto; gap: var(--space-2) var(--space-6); align-items: center; padding: var(--space-4) 0; border-top: 1px solid var(--line-soft); }
  .cm-row:first-child { padding-top: 0; border-top: 0; }
  .cm-row-title { font-weight: 500; }
  .cm-row-desc { max-width: 54ch; margin: 2px 0 0; color: var(--text-2); font-size: 13px; }
  .cm-careful { color: var(--warn); font-weight: 600; }
  .cm-switch { position: relative; width: 44px; height: 26px; padding: 0; background: var(--surface-2); border: 1px solid var(--control-line); border-radius: 13px; transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease); }
  .cm-switch::after { content: ''; position: absolute; top: 3px; left: 3px; width: 18px; height: 18px; background: var(--text-2); border-radius: 50%; transition: transform var(--dur) var(--ease), background var(--dur) var(--ease); }
  .cm-switch[aria-checked='true'] { background: var(--accent); border-color: var(--accent); }
  .cm-switch[aria-checked='true']::after { background: var(--accent-ink); transform: translateX(18px); }
  .cm-fact { display: flex; align-items: center; gap: var(--space-2); color: var(--text-2); font-size: 13px; }
  .cm-who { margin: 0; padding: 0; list-style: none; }
  .cm-who li { display: grid; grid-template-columns: 1fr; padding: var(--space-4) 0; border-top: 1px solid var(--line-soft); }
  .cm-who li:first-child { padding-top: 0; border-top: 0; }
  .cm-who-name { display: flex; align-items: center; flex-wrap: wrap; gap: var(--space-3); font-weight: 500; }
  .cm-who li.gone .cm-who-name { color: var(--text-2); }
  .cm-who-meta { margin: 2px 0 0; color: var(--text-2); font-size: 13px; overflow-wrap: anywhere; }

  /* Review and result */
  .cm-sum { display: grid; margin: 0 0 var(--space-4); }
  .cm-sum > div { display: grid; grid-template-columns: 150px 1fr auto; gap: var(--space-4); align-items: baseline; padding: var(--space-3) 0; border-top: 1px solid var(--line-soft); }
  .cm-sum > div:first-child { border-top: 0; }
  .cm-sum dt { color: var(--text-2); }
  .cm-sum dd { margin: 0; font-weight: 500; }
  .cm-sum dd.bad { color: var(--bad); }
  .cm-sum dd :global(.code-icon) { margin-right: 6px; vertical-align: -3px; }
  .cm-sub-line { display: block; color: var(--text-2); font-size: 13px; font-weight: 400; }
  .cm-state { display: flex; align-items: center; gap: var(--space-3); margin: var(--space-4) 0 var(--space-2); }
  .cm-state-big { font-size: 16px; font-weight: 600; }
  .cm-timer { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--space-4); }
  .cm-big { font-family: var(--mono); font-size: 32px; font-weight: 500; line-height: 1; letter-spacing: -.02em; }
  .cm-big.warn { color: var(--warn); }
  .cm-until { color: var(--text-2); font-size: 13px; }
  .cm-actions { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-3); margin: var(--space-4) 0; }
  .cm-blk { padding: var(--space-5) 0; border-top: 1px solid var(--line); }
  .cm-blk h3 { margin: 0 0 var(--space-2); font-size: 15px; font-weight: 600; }

  /* Phone: the whole screen, no list of steps */
  @media (max-width: 720px) {
    .cm-scrim { display: none; }
    .cm-dlg { inset: 0; width: auto; height: auto; transform: none; grid-template-columns: minmax(0, 1fr); border: 0; border-radius: 0; animation: cm-fade 200ms var(--ease) both; }
    .cm-rail { display: none; }
    .cm-top { padding-left: var(--space-4); }
    .cm-stage { padding: var(--space-2) var(--space-4) var(--space-5); }
    .cm-foot { padding: var(--space-3) var(--space-4); }
    .cm-sum > div { grid-template-columns: 1fr auto; }
    .cm-sum dt { grid-column: 1 / -1; }
    .cm-row { grid-template-columns: 1fr; }
    .cm-time { margin-left: 0; }
    .cm-btn, .cm-chip { min-height: 44px; }
    .cm-foot .cm-btn { padding: 0 var(--space-3); }
    .cm-foot { gap: var(--space-2); }
  }
  @media (prefers-reduced-motion: reduce) {
    .cm-dlg, .cm-scrim, .cm-enter { animation: none; }
    .cm-switch, .cm-switch::after, .cm-opt, .cm-chip { transition: none; }
  }
</style>
