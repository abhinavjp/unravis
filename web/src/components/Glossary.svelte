<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api } from '../lib/api';
  import { findTerm } from '../lib/copy/lookup';
  import { searchTerms } from '../lib/copy/lookup';
  import { HELP_STEPS, HELP_TEXT, glossaryCount } from '../lib/copy/screenCopy';
  import { versionLine } from '../lib/versionLine';
  import { updateCheckNote, updateCheckState, updatePreviewNow, isOwnReleaseUrl, UPDATE_INTERVALS, intervalLabel, checkedNote } from '../lib/updateCheck';
  import { refreshUpdateStatus, setUpdateReply, updates } from '../lib/updateState.svelte';
  import type { UpdateCheckReply } from '../lib/types';
  import Drawer from './Drawer.svelte';
  import HelpGuide from './HelpGuide.svelte';
  import { HELP_GUIDE_TEXT } from '../lib/copy/helpCopy';

  // The glossary drawer: every term in the copy deck, alphabetical, with a plain text filter. It opens from the Help button in the header.
  // Esc closes it and the page gives focus back to the button. Tab stays inside the panel while it is open, like the Connections drawer.
  // A hint's "Learn more in the glossary" button opens it on one term: that entry scrolls into view and takes focus, and is marked.
  let { onClose, focusTerm = null }: { onClose: () => void; focusTerm?: string | null } = $props();
  let query = $state('');
  let drawer = $state<HTMLElement>();
  let search = $state<HTMLInputElement>();
  // The user guide: when set, it replaces the help parts below. Back returns to them and puts focus on the button that opened it.
  let guide = $state<{ slug: string; anchor: string } | null>(null);
  let guideOpen = $state<HTMLButtonElement>();
  async function closeGuide() {
    guide = null;
    await tick();
    guideOpen?.focus();
  }
  const results = $derived(searchTerms(query));
  const summary = $derived(glossaryCount(results.length, query));
  // Which build this is, quietly, at the foot of the panel. If the call fails the line is simply absent.
  let build = $state('');
  onMount(() => {
    void api.version().then((info) => { build = versionLine(info); }).catch(() => { build = ''; });
  });
  // Support logs: where the journey/error log lives, for sending to a developer. Absent when the call fails.
  let logs = $state<{ dir: string } | null>(null);
  let logPathCopied = $state(false);
  onMount(() => {
    void api.logsInfo().then((info) => { if (info && info.dir) logs = { dir: info.dir }; }).catch(() => { logs = null; });
  });
  async function copyLogFolderPath() {
    if (!logs) return;
    try { await navigator.clipboard.writeText(logs.dir); logPathCopied = true; } catch { logPathCopied = false; }
  }
  // The manual update check: only runs when the button is pressed, never by itself.
  let checking = $state(false);
  let update = $state<UpdateCheckReply | null>(null);
  let checkId = $state(0);
  const unreachableReply = (): UpdateCheckReply => ({ status: 'unreachable', current: '', latest: '', url: '' });
  // The automatic check and its settings. The program checks on its own; these only change how often, or switch it off.
  const settings = $derived(updates.reply);
  const auto = $derived(settings?.auto ?? true);
  const minutes = $derived(settings?.intervalMinutes ?? 30);
  const intervalChoices = $derived(UPDATE_INTERVALS.some(item => item.minutes === minutes) ? UPDATE_INTERVALS : [...UPDATE_INTERVALS, { minutes, label: intervalLabel(minutes) }]);
  const applyAuto = $derived(settings?.applyAuto ?? true);
  const canChange = $derived(!!settings && settings.status !== 'dev');
  let settingsNote = $state('');
  async function saveSettings(nextAuto: boolean, nextMinutes: number, undo: () => void, nextApplyAuto?: boolean) {
    settingsNote = '';
    try {
      const reply = await api.updateSettings(nextAuto, nextMinutes, nextApplyAuto);
      if (reply && typeof reply.status === 'string') setUpdateReply(reply);
    } catch { settingsNote = 'Could not save this choice. It was not changed.'; undo(); await refreshUpdateStatus(); }
  }
  onMount(() => { void refreshUpdateStatus(); });
  async function checkForUpdates() {
    if (checking) return;
    checking = true;
    update = null;
    try {
      const reply = await api.updateCheck(updatePreviewNow());
      update = reply && typeof reply.status === 'string' ? reply : unreachableReply();
      if (update.status !== 'unreachable' || update.current) setUpdateReply({ ...update, auto: update.auto ?? auto, intervalMinutes: update.intervalMinutes ?? minutes });
    } catch { update = unreachableReply(); }
    finally { checking = false; }
    checkId += 1;
    await tick();
    document.querySelector('[data-testid=update-check-result]')?.scrollIntoView({ block: 'nearest' });
  }
  onMount(() => { if (!focusTerm || !findTerm(focusTerm)) search?.focus(); });
  $effect(() => {
    const id = focusTerm;
    if (!id || !findTerm(id)) return;
    query = '';
    void tick().then(() => {
      const entry = drawer?.querySelector<HTMLElement>('#glossary-' + CSS.escape(id));
      if (!entry) return;
      entry.scrollIntoView({ block: 'start' });
      entry.focus({ preventScroll: true });
    });
  });
</script>

<Drawer class="glossary-drawer" title={HELP_TEXT.title} titleId="glossary-title" closeLabel="Close help and glossary" focusable={'button:not(:disabled), input:not(:disabled), [href], [tabindex]:not([tabindex="-1"])'} bind:element={drawer} {onClose}>
  {#if guide}
    <div class="guide-top"><button type="button" class="update-button" data-testid="guide-back" onclick={closeGuide}>{HELP_GUIDE_TEXT.guideBack}</button></div>
    <HelpGuide slug={guide.slug} anchor={guide.anchor} onNavigate={(slug, anchor) => { guide = { slug, anchor }; }} onBack={closeGuide} />
  {:else}
    <section class="help-start" aria-labelledby="help-start-title">
      <h3 id="help-start-title">{HELP_TEXT.startHeading}</h3>
      <p>{HELP_TEXT.startIntro}</p>
      <ol>
        {#each HELP_STEPS as step (step)}<li>{step}</li>{/each}
      </ol>
      <button type="button" class="update-button guide-open" data-testid="guide-open" bind:this={guideOpen} onclick={() => { guide = { slug: 'index', anchor: '' }; }}>{HELP_GUIDE_TEXT.guideButton}</button>
    </section>
    <h3 class="glossary-heading">{HELP_TEXT.termsHeading}</h3>
    <div class="glossary-search">
      <label for="glossary-filter">Search terms</label>
      <input id="glossary-filter" type="search" bind:this={search} bind:value={query} autocomplete="off" spellcheck="false" aria-describedby="glossary-count" />
      <span id="glossary-count" class="glossary-count" role="status" aria-live="polite">{summary}</span>
    </div>
    <dl class="glossary-list">
      {#each results as term (term.id)}
        <div class="glossary-entry" class:glossary-focused={term.id === focusTerm} id={'glossary-' + term.id} tabindex="-1">
          <dt>{term.term}</dt>
          <dd class="glossary-hint">{term.hint}</dd>
          <dd>{term.definition}</dd>
          <dd class="glossary-not"><span>What it does not mean</span>{term.notMeaning}</dd>
          {#if term.aliases?.length}<dd class="glossary-alias"><span>Also called</span>{term.aliases.join(', ')}</dd>{/if}
        </div>
      {/each}
    </dl>
    {#if build}<p class="build-line" data-testid="build-line">{build}</p>{/if}
    <section class="support-logs" aria-labelledby="support-logs-title">
      <h3 id="support-logs-title">Support logs</h3>
      <p>Unravis keeps a log of what it did and what went wrong, so a problem can be traced afterwards.</p>
      {#if logs}<p class="log-path" data-testid="log-folder-path">{logs.dir}</p>
      <button type="button" class="update-button" data-testid="copy-log-folder" onclick={copyLogFolderPath}>Copy folder path</button>{#if logPathCopied}<span class="log-copied" role="status">Copied</span>{/if}{/if}
      <p>Send the newest unravis.log to your contact; it contains no passwords or case data.</p>
    </section>
    <section class="updates" aria-labelledby="updates-title">
      <h3 id="updates-title">Updates</h3>
      <p>Unravis looks for a new version on its own and tells you here. With the second box ticked it also installs it by itself, but only when you are not using it, and then starts again. Otherwise you press Apply.</p>
      {#if canChange}
        <label class="updates-auto"><input type="checkbox" data-testid="update-auto" checked={auto} onchange={(event) => { const box = event.currentTarget; void saveSettings(box.checked, minutes, () => { box.checked = auto; }); }} /> Check automatically</label>
        <label class="updates-auto"><input type="checkbox" data-testid="update-apply-auto" checked={applyAuto} disabled={!auto} onchange={(event) => { const box = event.currentTarget; void saveSettings(auto, minutes, () => { box.checked = applyAuto; }, box.checked); }} /> Install by itself when I am not using it</label>
        <label class="updates-every" for="update-every">How often</label>
        <select id="update-every" data-testid="update-every" value={minutes} disabled={!auto} onchange={(event) => { const pick = event.currentTarget; void saveSettings(auto, Number(pick.value), () => { pick.value = String(minutes); }); }}>
          {#each intervalChoices as item (item.minutes)}<option value={item.minutes}>{item.label}</option>{/each}
        </select>
        {#if settingsNote}<p class="update-note" role="alert" data-testid="update-settings-note">{settingsNote}</p>{/if}
        {#if settings && settings.checkedAt}<p class="updates-checked" data-testid="update-checked">{checkedNote(settings.checkedAt)}</p>{/if}
      {/if}
      <div class="update-check"><button type="button" class="update-button" data-testid="update-check-button" aria-disabled={checking} onclick={checkForUpdates}>{checking ? 'Checking...' : 'Check for updates'}</button>
        <div role="status" aria-live="polite" aria-busy={checking}>{#if update}{#key checkId}<p class="update-note" data-testid="update-check-result" data-state={updateCheckState(update)}>{updateCheckNote(update)}{#if update.status === 'available' && update.url && isOwnReleaseUrl(update.url)}{' '}<a data-testid="update-check-link" href={update.url} target="_blank" rel="noopener noreferrer">Open the release page</a>{/if}</p>{/key}{/if}</div></div>
    </section>
  {/if}
</Drawer>

<style>
  :global(.connections-drawer.glossary-drawer) { overflow-x: hidden; }
  .glossary-search { display: grid; gap: 4px; margin: 13px 0 6px; }
  .glossary-search label { color: var(--text-2); font-size: 12px; font-weight: 500; }
  .glossary-search input { width: 100%; min-width: 0; min-height: 34px; padding: 6px 10px; color: var(--text); background: var(--bg); border: 1px solid var(--control-line); border-radius: var(--radius-sm); font-size: 13px; }
  .glossary-count { color: var(--text-2); font-size: 12px; }
  .glossary-list { margin: 0; }
  .glossary-entry { padding: 12px 0; border-bottom: 1px solid var(--line-soft); }
  .glossary-entry:focus { outline: none; }
  .glossary-focused { margin: 0 -10px; padding: 12px 10px; background: var(--surface-2); border-radius: 0; box-shadow: inset 0 0 0 1px var(--accent); }
  .help-start { margin: 13px 0 0; padding-bottom: 12px; border-bottom: 1px solid var(--line-soft); font-size: 13px; }
  .help-start h3, .glossary-heading { margin: 0 0 6px; font-size: 15px; font-weight: 600; letter-spacing: -.01em; }
  .glossary-heading { margin-top: 14px; }
  .help-start p { margin: 0 0 6px; color: var(--text-2); }
  .help-start ol { margin: 0; padding-left: 20px; display: grid; gap: 4px; }
  .glossary-entry dt { font-size: 15px; font-weight: 600; letter-spacing: -.01em; }
  .glossary-entry dd { margin: 5px 0 0; font-size: 13px; overflow-wrap: anywhere; }
  .build-line { margin: 14px 0 0; color: var(--text-2); font: 12px var(--mono); }
  .support-logs { margin-top: 14px; font-size: 13px; }
  .support-logs h3 { margin: 0 0 6px; font-size: 15px; font-weight: 600; letter-spacing: -.01em; }
  .support-logs p { margin: 0 0 6px; color: var(--text-2); }
  .support-logs .log-path { color: var(--text); font: 12px var(--mono); overflow-wrap: anywhere; }
  .support-logs .log-copied { margin-left: 8px; color: var(--text-2); font-size: 12px; }
  .updates { margin-top: 18px; display: grid; gap: 6px; }
  .updates h3 { margin: 0 0 2px; font-size: 15px; font-weight: 600; letter-spacing: -.01em; }
  .updates p { margin: 0; color: var(--text-2); }
  .updates-auto { display: inline-flex; align-items: center; gap: 8px; margin-top: 4px; color: var(--text); font-size: 13px; cursor: pointer; }
  .updates-auto input { margin: 0; accent-color: var(--accent); }
  .updates-every { margin-top: 4px; color: var(--text-2); font-size: 12px; font-weight: 500; }
  .updates select { width: 100%; max-width: 240px; min-height: 34px; padding: 6px 10px; color: var(--text); background: var(--bg); border: 1px solid var(--control-line); border-radius: var(--radius-sm); font: inherit; font-size: 13px; }
  .updates select:disabled { opacity: .55; }
  .updates select:focus-visible, .updates-auto:has(input:focus-visible) { outline: 2px solid var(--focus); outline-offset: 2px; }
  .updates-checked { font-size: 12px; }
  .update-check { margin-top: 10px; display: grid; gap: 8px; justify-items: start; }
  .update-button { border: 1px solid var(--control-line); background: transparent; color: var(--text); padding: 6px 12px; border-radius: var(--radius-sm); font: inherit; cursor: pointer; }
  .guide-open { margin-top: 10px; }
  .guide-top { margin: 13px 0 12px; }
  .update-button:hover { background: var(--surface-2); }
  .update-button:focus-visible { outline: 2px solid var(--focus); }
  .update-button[aria-disabled='true'] { opacity: .6; cursor: default; }
  .update-note { margin: 0; padding: 6px 10px; border-radius: var(--radius-sm); background: var(--surface-2); color: var(--text-2); font-size: 13px; }
  .glossary-hint { font-weight: 600; }
  .glossary-not, .glossary-alias { color: var(--text-2); }
  .glossary-not span, .glossary-alias span { display: block; color: var(--text); font-size: 12px; font-weight: 600; }
</style>
