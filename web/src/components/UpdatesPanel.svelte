<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api } from '../lib/api';
  import { INSTALL_STEPS, UPD_TEXT as T } from '../lib/copy/updatesCopy';
  import { UPDATE_INTERVALS, intervalLabel, isOwnReleaseUrl, updatePreviewNow } from '../lib/updateCheck';
  import { loadNotes, shortVersion, versionsSince, type ReleaseNotes } from '../lib/updateNotes';
  import { checkedLabel, installPercent, installStep, nextCheckNote, statusWords, updatePhase } from '../lib/updatesPage';
  import { installing, openWhatsNew, refreshUpdateStatus, setUpdateReply, updateFlow, updateNow, updates, whatsNew } from '../lib/updateState.svelte';
  import Drawer from './Drawer.svelte';
  import UpdateHistoryList from './UpdateHistoryList.svelte';

  // The Updates page: the version, whether a newer one exists, the settings, and the history of what changed. Every state comes from the update
  // server's one answer (updates.reply); this page only draws it. "Update now" asks first, then shows the steps while the update installs.
  let { onClose }: { onClose: () => void } = $props();

  let element = $state<HTMLElement>();
  let tab = $state<'now' | 'history'>('now');
  let checking = $state(false);
  let notes = $state<ReleaseNotes[] | null>(null);
  let settingsError = $state('');
  let popup = $state(false);
  let dialog = $state<HTMLDialogElement>();

  const reply = $derived(updates.reply);
  const phase = $derived(updatePhase(reply, checking));
  const words = $derived(statusWords(phase, reply));
  const current = $derived(reply?.current ?? '');
  const target = $derived(reply?.install?.version || reply?.latest || '');
  const percent = $derived(installPercent(reply));
  const auto = $derived(reply?.auto ?? true);
  const minutes = $derived(reply?.intervalMinutes ?? 30);
  const applyAuto = $derived(reply?.applyAuto ?? true);
  const canChange = $derived(!!reply && reply.status !== 'dev');
  const choices = $derived(UPDATE_INTERVALS.some((item) => item.minutes === minutes) ? UPDATE_INTERVALS : [...UPDATE_INTERVALS, { minutes, label: intervalLabel(minutes) }]);
  const own = $derived(!!reply?.url && isOwnReleaseUrl(reply.url));
  const unread = $derived(notes ? versionsSince(notes, whatsNew.seen, current).length : 0);
  const hasCurrentNotes = $derived(!!notes?.some((release) => release.version === current.replace(/^v/i, '')));
  const pendingRow = $derived.by(() => {
    if (!reply || !target || (phase !== 'available' && phase !== 'ready' && phase !== 'autowait' && phase !== 'installing' && phase !== 'failed')) return null;
    const kind = phase === 'failed' ? 'failed' : phase === 'installing' ? 'installing' : 'ready';
    return { version: target, kind: kind as 'ready' | 'installing' | 'failed', current, link: own ? reply.url : '' };
  });

  // The window: asking first, then the steps, then (if it did not work) the reason.
  const view = $derived<'confirm' | 'installing' | 'failed'>(updateFlow.chaining || phase === 'installing' ? 'installing' : phase === 'failed' || (installing.error && !installing.busy) ? 'failed' : 'confirm');
  // A refusal the page itself met (the download or the install could not start) has its own words; the program's reason wins when it gave one.
  const failReason = $derived(words.reason || installing.error);
  const step = $derived(updateFlow.chaining && installStep(reply, true) === 0 ? 1 : installStep(reply, updateFlow.chaining) || 1);

  onMount(() => {
    void refreshUpdateStatus();
    void loadNotes().then((list) => { notes = list; });
  });

  async function check() {
    if (checking) return;
    checking = true;
    try {
      const answer = await api.updateCheck(updatePreviewNow());
      if (answer && typeof answer.status === 'string') setUpdateReply({ ...answer, auto: answer.auto ?? auto, intervalMinutes: answer.intervalMinutes ?? minutes });
      else setUpdateReply({ status: 'unreachable', current, latest: '', url: '' });
    } catch { setUpdateReply({ status: 'unreachable', current, latest: '', url: '' }); }
    finally { checking = false; }
  }

  async function save(nextAuto: boolean, nextMinutes: number, nextApply?: boolean) {
    settingsError = '';
    try {
      const answer = await api.updateSettings(nextAuto, nextMinutes, nextApply);
      if (answer && typeof answer.status === 'string') setUpdateReply(answer);
    } catch { settingsError = T.settingsError; await refreshUpdateStatus(); }
  }

  $effect(() => {
    if (!dialog) return;
    if (popup && !dialog.open) {
      dialog.showModal();
      void tick().then(() => dialog?.querySelector<HTMLElement>('[data-primary]')?.focus());
    } else if (!popup && dialog.open) dialog.close();
  });
  function onDialogKey(event: KeyboardEvent) {
    // The drawer behind this window also listens for Esc and Tab: keep both for the window alone.
    event.stopPropagation();
    if (event.key === 'Escape') { event.preventDefault(); popup = false; }
  }
  function startNow() { void updateNow(); }

  const tabs = ['now', 'history'] as const;
  function onTabKey(event: KeyboardEvent) {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
    event.preventDefault();
    tab = tab === 'now' ? 'history' : 'now';
    void tick().then(() => element?.querySelector<HTMLElement>('[role=tab][aria-selected=true]')?.focus());
  }
</script>

<Drawer title={T.title} titleId="updates-title" intro={T.intro} closeLabel={T.closeLabel} class="updates-drawer" focusTitle bind:element {onClose}>
  {#snippet headerExtra()}
    <div class="tabs" role="tablist" aria-label={T.tabsLabel} tabindex="-1" onkeydown={onTabKey}>
      {#each tabs as id (id)}
        <button type="button" role="tab" id={'upd-tab-' + id} aria-selected={tab === id} aria-controls={'upd-pane-' + id} tabindex={tab === id ? 0 : -1} class:on={tab === id} data-testid={'upd-tab-' + id} onclick={() => { tab = id; }}>{id === 'now' ? T.tabNow : T.tabHistory}</button>
      {/each}
    </div>
  {/snippet}

  {#if tab === 'now'}
    <div class="upd-body" id="upd-pane-now" role="tabpanel" aria-labelledby="upd-tab-now" data-testid="updates-now">
      <section class="status" data-phase={phase} aria-live="polite">
        <span class="icon" aria-hidden="true">{#if phase === 'checking'}<span class="spin"></span>{:else if phase === 'uptodate'}✓{:else if phase === 'failed' || phase === 'error'}!{:else if phase === 'installing'}<span class="spin"></span>{:else if phase === 'available' || phase === 'ready' || phase === 'autowait'}↑{:else}•{/if}</span>
        <div class="said">
          <h3 data-testid="updates-title-line">{words.title}</h3>
          <p>{words.sub}</p>
          {#if words.reason}<p class="reason"><b>{T.reason}</b>{words.reason}</p>{/if}
          {#if phase === 'installing'}<div class="bar" role="progressbar" aria-label={T.installingTitle(shortVersion(target))} aria-valuemin="0" aria-valuemax="100" aria-valuenow={percent}><i style:width="{percent}%"></i></div>{/if}
        </div>
        <div class="acts">
          {#if phase === 'available' || phase === 'ready' || phase === 'autowait'}
            <button type="button" class="primary-button" data-testid="updates-update-now" onclick={() => { popup = true; }}>{phase === 'available' ? T.updateNow : T.installNow}</button>
          {:else if phase === 'failed'}
            <button type="button" class="primary-button" data-testid="updates-update-now" onclick={() => { popup = true; }}>{T.tryAgain}</button>
          {:else if phase === 'installing'}
            <button type="button" class="secondary-button" data-testid="updates-show" onclick={() => { popup = true; }}>{T.showDetails}</button>
          {:else if phase !== 'checking'}
            <button type="button" class="secondary-button" data-testid="updates-check" onclick={check}>{T.checkNow}</button>
          {/if}
          {#if phase === 'checking'}<span class="busy">{T.checking}</span>{/if}
        </div>
      </section>

      <section class="facts" aria-label={T.fieldVersion}>
        <dl>
          <dt>{T.fieldVersion}</dt><dd data-testid="updates-version">{reply?.current ? shortVersion(reply.current) : '—'}</dd>
          <dt>{T.fieldChannel}</dt><dd>{reply?.channel || '—'}{#if reply?.channel}<span class="hint"> · {T.channelHelp}</span>{/if}</dd>
          <dt>{T.fieldChecked}</dt><dd data-testid="updates-checked">{checkedLabel(reply?.checkedAt)}</dd>
        </dl>
        {#if phase === 'available' || phase === 'ready' || phase === 'autowait' || phase === 'failed' || phase === 'uptodate' || phase === 'error'}
          <button type="button" class="link" data-testid="updates-check-again" disabled={checking} onclick={check}>{checking ? T.checking : T.checkNow}</button>
        {/if}
      </section>

      <section class="notes" aria-labelledby="upd-notes-h">
        <h3 id="upd-notes-h">{T.notesTitle}</h3>
        {#if notes && !notes.length}
          <p class="sub">{T.notesMissing}</p>
        {:else if unread > 1}
          <p class="sub"><b>{T.notesUnread(unread)}</b> {T.notesUnreadHelp(shortVersion(whatsNew.seen))}</p>
        {:else}
          <p class="sub"><b>{T.notesCurrent(shortVersion(current))}</b> {T.notesCurrentHelp}</p>
        {/if}
        <button type="button" class="secondary-button" data-testid="updates-open-whatsnew" disabled={!hasCurrentNotes} onclick={() => openWhatsNew(false)}>{T.notesButton}</button>
      </section>

      <section class="settings" aria-labelledby="upd-set-h">
        <h3 id="upd-set-h">{T.settingsTitle}</h3>
        <div class="setting">
          <div class="label"><span id="upd-auto-l">{T.checkAuto}</span><span class="help">{T.checkAutoHelp}{!auto ? T.checkAutoOff : (phase === 'ready' || phase === 'autowait' ? T.checkAutoPaused : '')}</span></div>
          <button type="button" role="switch" class="switch" aria-checked={auto} aria-labelledby="upd-auto-l" disabled={!canChange} data-testid="updates-auto" onclick={() => save(!auto, minutes)}><i></i></button>
        </div>
        <div class="setting">
          <label class="label" for="upd-every"><span>{T.intervalLabel}</span><span class="help">{nextCheckNote(reply?.checkedAt, minutes)}</span></label>
          <select id="upd-every" disabled={!canChange || !auto} value={minutes} data-testid="updates-every" onchange={(event) => { const pick = event.currentTarget; void save(auto, Number(pick.value)).then(() => { pick.value = String(minutes); }); }}>
            {#each choices as item (item.minutes)}<option value={item.minutes}>{item.label}</option>{/each}
          </select>
        </div>
        <div class="setting">
          <div class="label"><span id="upd-apply-l">{T.installAuto}</span><span class="help">{applyAuto ? T.installAutoOn : T.installAutoOff}</span></div>
          <button type="button" role="switch" class="switch" aria-checked={applyAuto} aria-labelledby="upd-apply-l" disabled={!canChange || !auto} data-testid="updates-apply" onclick={() => save(auto, minutes, !applyAuto)}><i></i></button>
        </div>
        {#if settingsError}<p class="err" role="alert">{settingsError}</p>{/if}
      </section>
    </div>
  {:else}
    <div class="upd-body hist-body" id="upd-pane-history" role="tabpanel" aria-labelledby="upd-tab-history" data-testid="updates-history">
      <div class="hist-top"><h3>{T.historyTitle}</h3>{#if notes?.length}<p>{T.historySub(notes.length)}</p>{/if}</div>
      {#if notes === null}
        <p class="sub" role="status">{T.historyLoading}</p>
      {:else if !notes.length && !pendingRow}
        <p class="sub">{T.historyEmpty}</p>
      {:else}
        <UpdateHistoryList releases={notes} installed={current.replace(/^v/i, '')} pending={pendingRow} />
      {/if}
    </div>
  {/if}

  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <dialog bind:this={dialog} class="upd-dialog" aria-labelledby="upd-dlg-title" onkeydown={onDialogKey} oncancel={(event) => { event.preventDefault(); popup = false; }} onclose={() => { popup = false; }} data-testid="updates-dialog" data-view={view}>
    {#if view === 'confirm'}
      <h3 id="upd-dlg-title">{T.confirmTitle(shortVersion(target))}</h3>
      <p class="jump"><span>{shortVersion(current)}</span><span aria-hidden="true">→</span><b>{shortVersion(target)}</b></p>
      <ul>{#each T.confirmPoints(shortVersion(current), shortVersion(target)) as point (point)}<li>{point}</li>{/each}</ul>
      {#if own}<p class="more"><a href={reply?.url} target="_blank" rel="noopener noreferrer">{T.readNotes}<span class="sr-only"> (opens in a new tab)</span></a></p>{/if}
      <div class="dlg-acts"><button type="button" class="secondary-button" onclick={() => { popup = false; }}>{T.later}</button><button type="button" class="primary-button" data-primary data-testid="updates-confirm" onclick={startNow}>{T.updateNow}</button></div>
    {:else if view === 'installing'}
      <h3 id="upd-dlg-title">{T.installingTitle(shortVersion(target))}</h3>
      <p class="sub">{reply?.applyAuto && !reply?.inUse && !updateFlow.chaining ? T.installingAutoSub : T.installingSub}</p>
      <div class="bar" role="progressbar" aria-label={T.installingTitle(shortVersion(target))} aria-valuemin="0" aria-valuemax="100" aria-valuenow={percent}><i style:width="{percent}%"></i></div>
      <ol class="steps" aria-label={T.stepLine(step, INSTALL_STEPS[step - 1])}>
        {#each INSTALL_STEPS as name, n (name)}
          <li class:done={n + 1 < step} class:now={n + 1 === step}><span class="mark" aria-hidden="true">{#if n + 1 < step}✓{:else if n + 1 === step}<span class="spin"></span>{/if}</span>{name}</li>
        {/each}
      </ol>
      <p class="sr-only" role="status">{T.stepLine(step, INSTALL_STEPS[step - 1])}</p>
      <div class="dlg-acts"><button type="button" class="secondary-button" data-primary onclick={() => { popup = false; }}>{T.hide}</button></div>
    {:else}
      <h3 id="upd-dlg-title">{T.failedTitle(shortVersion(target))}</h3>
      <p class="sub">{T.failedSub(shortVersion(current))}</p>
      {#if failReason}<p class="reason"><b>{T.reason}</b>{failReason}</p>{/if}
      <p class="sub">{T.failedHelp}</p>
      <div class="dlg-acts"><button type="button" class="secondary-button" onclick={() => { popup = false; }}>{T.close}</button><button type="button" class="primary-button" data-primary data-testid="updates-retry" onclick={startNow}>{T.tryAgain}</button></div>
    {/if}
  </dialog>
</Drawer>

<style>
  :global(.connections-drawer.updates-drawer) { width: min(780px, calc(100vw - 24px)); overflow: hidden; }
  @media (max-width: 720px) { :global(.connections-drawer.updates-drawer) { width: min(780px, 100vw); } }
  :global(.connections-drawer.updates-drawer > .upd-body) { flex: 1 1 0; min-height: 0; overflow-y: auto; }
  :global(.connections-drawer.updates-drawer > .upd-body.hist-body) { overflow: hidden; display: flex; flex-direction: column; }
  .tabs { display: flex; gap: var(--space-1); margin-top: var(--space-3); border-bottom: 1px solid var(--line); }
  .tabs button { padding: 8px 14px; font-size: 13.5px; font-weight: 500; color: var(--text-2); border: 0; border-bottom: 2px solid transparent; background: transparent; margin-bottom: -1px; }
  .tabs button.on { color: var(--text); font-weight: 600; border-bottom-color: var(--accent); }
  .tabs button:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
  .upd-body { display: grid; gap: var(--space-5, 22px); align-content: start; min-width: 0; padding: var(--space-4) 0 var(--space-2); }
  h3 { margin: 0; font-size: 15px; font-weight: 600; }
  .sub { margin: var(--space-1) 0 var(--space-2); color: var(--text-2); font-size: 13px; line-height: 1.5; }
  .sub b { color: var(--text); font-weight: 600; }
  .status { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; gap: var(--space-3); align-items: start; padding: var(--space-4); background: var(--surface-2); border: 1px solid var(--line); border-radius: var(--radius-sm); }
  .status[data-phase='available'], .status[data-phase='ready'], .status[data-phase='autowait'], .status[data-phase='installing'] { border-color: var(--accent); }
  .status[data-phase='failed'], .status[data-phase='error'] { border-color: var(--bad); }
  .icon { display: grid; place-items: center; width: 34px; height: 34px; border-radius: 50%; border: 1px solid var(--control-line); font-weight: 700; color: var(--text); }
  .said p { margin: var(--space-1) 0 0; color: var(--text-2); font-size: 13px; line-height: 1.5; }
  .said .reason { color: var(--text); }
  .acts { display: flex; align-items: center; gap: var(--space-2); }
  .busy { color: var(--text-2); font-size: 13px; }
  @media (max-width: 720px) { .status { grid-template-columns: auto minmax(0, 1fr); } .acts { grid-column: 1 / -1; } }
  .bar { height: 8px; margin-top: var(--space-3); background: var(--surface); border: 1px solid var(--line); border-radius: 999px; overflow: hidden; }
  .bar i { display: block; height: 100%; background: var(--accent); transition: width var(--dur) var(--ease); }
  .spin { width: 14px; height: 14px; border: 2px solid var(--control-line); border-top-color: var(--accent); border-radius: 50%; animation: upd-spin .9s linear infinite; }
  @keyframes upd-spin { to { transform: rotate(360deg); } }
  @media (prefers-reduced-motion: reduce) { .spin { animation: none; border-top-color: var(--control-line); border-right-color: var(--accent); } }
  .facts { display: flex; flex-wrap: wrap; align-items: end; justify-content: space-between; gap: var(--space-3); }
  dl { display: grid; grid-template-columns: max-content minmax(0, 1fr); gap: 4px var(--space-4); margin: 0; font-size: 13px; }
  dt { color: var(--text-2); }
  dd { margin: 0; font-weight: 500; }
  .hint { color: var(--text-2); font-weight: 400; }
  .link { padding: 0; border: 0; background: none; color: var(--accent); font-size: 13px; font-weight: 500; text-decoration: underline; }
  .link:disabled { color: var(--text-2); text-decoration: none; }
  .notes { display: grid; gap: var(--space-1); justify-items: start; }
  .settings { display: grid; gap: var(--space-3); }
  .setting { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--space-4); padding-top: var(--space-3); border-top: 1px solid var(--hairline); }
  .label { display: grid; gap: 2px; min-width: 0; font-size: 13.5px; font-weight: 500; }
  .help { color: var(--text-2); font-size: 12.5px; font-weight: 400; line-height: 1.5; }
  .setting select { flex: 0 0 auto; min-width: 160px; min-height: 34px; padding: 6px 10px; color: var(--text); background: var(--bg); border: 1px solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: 13px; }
  .setting select:disabled { opacity: .55; }
  .switch { flex: 0 0 auto; position: relative; width: 40px; height: 24px; padding: 0; border: 1px solid var(--control-line); border-radius: 999px; background: var(--surface-2); }
  .switch i { position: absolute; top: 2px; left: 2px; width: 18px; height: 18px; border-radius: 50%; background: var(--text-2); transition: transform var(--dur) var(--ease), background var(--dur) var(--ease); }
  .switch[aria-checked='true'] { background: var(--accent); border-color: var(--accent); }
  .switch[aria-checked='true'] i { transform: translateX(16px); background: var(--accent-ink); }
  .switch:disabled { opacity: .5; }
  .switch:focus-visible, .setting select:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; }
  .err { margin: 0; color: var(--bad); font-size: 13px; }
  .hist-top { flex: 0 0 auto; }
  .hist-top p { margin: var(--space-1) 0 0; color: var(--text-2); font-size: 13px; line-height: 1.5; }
  .upd-dialog { width: min(460px, calc(100vw - 32px)); padding: var(--space-5, 22px); color: var(--text); background: var(--surface); border: 1px solid var(--control-line); border-radius: var(--radius-sm); }
  .upd-dialog::backdrop { background: color-mix(in srgb, var(--bg) 60%, transparent); }
  .upd-dialog h3 { font-size: 17px; }
  .jump { display: flex; align-items: center; gap: var(--space-2); margin: var(--space-3) 0 0; font-variant-numeric: tabular-nums; }
  .upd-dialog ul { margin: var(--space-3) 0 0; padding-left: 18px; color: var(--text-2); font-size: 13px; line-height: 1.6; }
  .more { margin: var(--space-3) 0 0; font-size: 13px; }
  .more a { color: var(--accent); }
  .dlg-acts { display: flex; justify-content: flex-end; gap: var(--space-2); margin-top: var(--space-4); }
  .steps { margin: var(--space-3) 0 0; padding: 0; list-style: none; display: grid; gap: var(--space-2); font-size: 13.5px; color: var(--text-2); }
  .steps li { display: flex; align-items: center; gap: var(--space-2); }
  .steps li.now { color: var(--text); font-weight: 600; }
  .steps li.done { color: var(--text); }
  .mark { display: grid; place-items: center; width: 18px; height: 18px; font-size: 12px; }
  .steps li:not(.done):not(.now) .mark::before { content: ''; width: 6px; height: 6px; border-radius: 50%; background: var(--control-line); }
  .sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; border: 0; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
</style>
