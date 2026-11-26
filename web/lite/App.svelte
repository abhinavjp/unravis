<script lang="ts">
  // Unravis Lite: one Blackbox, read-only. A Status page and the charts, for a server that runs only the Blackbox service.
  // Nothing here changes anything: the Lite server answers only the list and the chart series (internal/lite).
  import { onMount } from 'svelte';
  import BrandMark from '../src/brand/components/BrandMark.svelte';
  import BlackboxCharts from '../src/components/BlackboxCharts.svelte';
  import { blackboxApi, shownTime, type BlackboxItem } from '../src/lib/blackbox';
  import { BB_TEXT as T, ageWords } from '../src/lib/copy/blackboxCopy';
  import { formatTime } from '../src/lib/time';
  import Sticker from '../src/components/BlackboxSticker.svelte';
  import { hasSessionToken, NO_SESSION_MESSAGE } from '../src/lib/api';
  import { theme } from '../src/lib/theme.svelte';
  import { timeZone } from '../src/lib/timeZone.svelte';

  type Page = 'status' | 'charts';
  let item = $state<BlackboxItem | null>(null);
  let problem = $state('');
  let loading = $state(true);
  let page = $state<Page>(location.hash === '#charts' ? 'charts' : 'status');
  // When this page last read the Blackbox, so a tab that was left open does not look current (BLACKBOX-22).
  let readAt = $state('');
  let refreshing = $state(false);
  let liteVersion = $state('');

  const go = (next: Page) => { page = next; try { history.replaceState(null, '', next === 'charts' ? '#charts' : '#status'); } catch { /* a convenience only */ } };

  async function load() {
    // The first read shows "Reading". A refresh keeps the page and the button where they are, so focus stays on Refresh.
    // A failed refresh keeps the page too and shows the problem above the page (S2).
    if (item) { if (refreshing) return; refreshing = true; } else loading = true;
    problem = '';
    try {
      if (!hasSessionToken()) {
        problem = NO_SESSION_MESSAGE.replace('Unravis', 'Unravis Lite');
        return;
      }
      try {
        const list = await blackboxApi.list();
        item = list.items[0] ?? null;
        readAt = formatTime(Date.now(), { zone: timeZone.zone, precision: 'second', date: false });
        if (!item) problem = 'No Blackbox is set for this copy. Start Unravis Lite with --folder <recordings folder>.';
      } catch (e) {
        const text = e instanceof Error ? e.message : 'Could not read the Blackbox.';
        // The full app's wording points at screens Lite does not have.
        problem = text.replace(/Open Unravis from the link it printed when it started, or restart it\./, 'Open Unravis Lite from the link it printed, or start it again.');
      }
    } finally {
      loading = false;
      refreshing = false;
    }
  }

  // Unravis Lite's own version comes from a small file the program serves beside the page (cmd/unravis-lite/info.go).
  async function loadVersion() {
    try {
      const r = await fetch('lite-info.json', { cache: 'no-store' });
      if (r.ok) liteVersion = String((await r.json()).version ?? '');
    } catch { /* the version line is left out */ }
  }

  onMount(() => { theme.start(); void load(); void loadVersion(); });

  const facts = $derived(item?.facts);
  // The recorder cannot be read: most often the recordings folder is locked to administrators.
  const unreadable = $derived(!!item && (item.state.state === 'not-found' || item.state.state === 'unreachable' || item.state.state === 'incomplete'));
  const themeWord = $derived(theme.choice === 'auto' ? 'Theme: follow Windows' : theme.choice === 'light' ? 'Theme: light' : 'Theme: dark');
  const every = (s: number) => s >= 60 && s % 60 === 0 ? s / 60 + ' min' : s + ' s';
</script>

<div class="lite">
  <header class="top">
    <BrandMark wordmark="Unravis Lite" />
    {#if liteVersion}<span class="ver mono" title="The version of this viewer">{liteVersion}</span>{/if}
    {#if item}
      <nav aria-label="Pages">
        <button type="button" class="tab" class:on={page === 'status'} aria-current={page === 'status' ? 'page' : undefined} onclick={() => go('status')}>Status</button>
        <button type="button" class="tab" class:on={page === 'charts'} aria-current={page === 'charts' ? 'page' : undefined} onclick={() => go('charts')}>Charts</button>
      </nav>
    {/if}
    <span class="grow"></span>
    {#if item}<span class="host mono" title="The server this Blackbox records">{facts?.host ?? item.name}</span><Sticker info={item.state} />{/if}
    <button type="button" class="quiet" title="Cycles between following Windows, light and dark" onclick={() => theme.cycle()}>{themeWord}</button>
  </header>

  <main>
    {#if loading}
      <p role="status">Reading the Blackbox…</p>
    {:else if problem && !item}
      <p class="problem" role="alert">{problem}</p>
      <div><button type="button" onclick={load}>Try again</button></div>
    {:else if item}
      {#if problem}<p class="problem" role="alert">{problem}</p>{/if}
      {#if page === 'status'}
        <section class="status" aria-labelledby="bb-name">
          <h1 id="bb-name">{item.name}</h1>
          <p class="where">Blackbox on <span class="mono wrap">{item.location}</span></p>
          <p class="state"><span>{item.state.detail}</span></p>
          {#if unreadable}
            <p class="hint" role="note">If the recordings folder is locked, start Unravis Lite as an administrator, then open the new link it prints.</p>
          {/if}
          <div class="cols">
            <section aria-labelledby="h-this">
              <h2 id="h-this">This server</h2>
              {#if facts}
                <dl>
                  <div><dt>Host</dt><dd>{facts.host}</dd></div>
                  <div><dt>Recorder</dt><dd>{facts.version ?? 'Not reported'}</dd></div>
                  <div><dt>Samples every</dt><dd>{every(facts.effectiveIntervalSeconds ?? facts.intervalSeconds)}{#if facts.effectiveIntervalSeconds && facts.effectiveIntervalSeconds !== facts.intervalSeconds}{' '}(set to {every(facts.intervalSeconds)}){/if}</dd></div>
                  {#if facts.freePercent !== undefined}<div><dt>Drive free</dt><dd>{Math.round(facts.freePercent)}%</dd></div>{/if}
                </dl>
              {:else}<p class="note">The recorder could not be read, so there is nothing to show here yet.</p>{/if}
            </section>
            <section aria-labelledby="h-rec">
              <h2 id="h-rec">What is recorded</h2>
              {#if facts}
                <dl>
                  <div><dt>First reading</dt><dd>{shownTime(facts.firstSample, timeZone.zone)}</dd></div>
                  <div><dt>Last reading</dt><dd>{shownTime(facts.lastSample, timeZone.zone)}{#if ageWords(facts.lastSample)}{' '}({ageWords(facts.lastSample)}){/if}</dd></div>
                  <div><dt>Readings</dt><dd>{facts.samples}</dd></div>
                  <div><dt>Files</dt><dd>{facts.files}</dd></div>
                  <div><dt>Gaps</dt><dd>{T.gapsText(facts.gaps, facts.unread ?? 0, facts.longestGapSeconds ?? 0)}</dd></div>
                </dl>
              {/if}
            </section>
            <section aria-labelledby="h-upd">
              <h2 id="h-upd">Version and updates</h2>
              <dl>
                <div><dt>Unravis Lite</dt><dd>{liteVersion || 'Not reported'}</dd></div>
                {#if facts?.update}<div><dt>Recorder</dt><dd>{T.updateLine(facts.update)}</dd></div>{/if}
              </dl>
              <p class="note">Updates come with the Blackbox service. Unravis Lite does not install anything.</p>
            </section>
          </div>
          <div class="actions">
            <button type="button" class="primary" onclick={() => go('charts')}>Open the charts</button>
            <button type="button" onclick={load} aria-busy={refreshing}>Refresh</button>
            <span class="read-at" role="status">{refreshing ? 'Reading the Blackbox.' : readAt ? 'Read at ' + readAt + '.' : ''}</span>
          </div>
          <p class="note">Unravis Lite only reads. It changes nothing and saves nothing.</p>
        </section>
      {:else}
        <BlackboxCharts {item} inline />
      {/if}
    {/if}
  </main>
</div>

<style>
  .lite { min-height: 100vh; display: flex; flex-direction: column; background: var(--bg, var(--surface)); color: var(--text); }
  .top { display: flex; flex-wrap: wrap; align-items: center; gap: 10px 16px; padding: 10px 20px; border-bottom: 1px solid var(--line); background: var(--surface); }
  .grow { flex: 1; }
  .ver { color: var(--text-2); font-size: 12px; }
  .host { color: var(--text-2); }
  nav { display: flex; gap: 4px; margin-left: 8px; }
  .tab { padding: 6px 14px; border: 1px solid transparent; border-radius: var(--radius-sm); background: transparent; color: var(--text-2); cursor: pointer; }
  .tab.on { color: var(--text); border-color: var(--line); background: var(--surface-2, var(--surface)); }
  main { flex: 1; width: 100%; max-width: 1840px; margin: 0 auto; padding: 20px; display: grid; gap: 14px; align-content: start; }
  h1 { font-size: 22px; margin: 0; }
  h2 { font-size: 14px; margin: 0 0 8px; color: var(--text-2); font-weight: 600; }
  p { margin: 0; }
  .status { display: grid; gap: 14px; max-width: 1100px; }
  .cols { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 24px; margin-top: 6px; }
  .mono { font-family: var(--mono); color: var(--text-2); }
  .wrap { overflow-wrap: anywhere; }
  .problem { color: var(--bad); }
  .hint { color: var(--text-2); }
  dl { display: grid; gap: 6px; margin: 0; }
  dl div { display: flex; gap: 10px; }
  dt { width: 120px; flex: 0 0 auto; color: var(--text-2); }
  dd { margin: 0; overflow-wrap: anywhere; }
  .actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; margin-top: 6px; }
  .read-at { color: var(--text-2); font-size: 13px; }
  .where { color: var(--text-2); }
  button { padding: 6px 12px; border: 1px solid var(--control-line); border-radius: var(--radius-sm); background: var(--surface); color: var(--text); cursor: pointer; }
  button.primary { background: var(--accent); color: var(--accent-ink); border-color: var(--accent); }
  button.quiet { border-color: transparent; background: transparent; color: var(--text-2); font-size: 12px; }
  .note { color: var(--text-2); font-size: 12px; }
</style>
