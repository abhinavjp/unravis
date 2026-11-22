<script lang="ts">
  // Unravis Lite: one Blackbox, read-only. It lists the Blackbox this copy was started on and opens its charts.
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

  let item = $state<BlackboxItem | null>(null);
  let problem = $state('');
  let loading = $state(true);
  let open = $state(false);
  // When this page last read the Blackbox, so a tab that was left open does not look current (BLACKBOX-22).
  let readAt = $state('');
  let refreshing = $state(false);

  async function load() {
    // The first read shows "Reading". A refresh keeps the page and the button where they are, so focus stays on Refresh.
    // A failed refresh keeps the page too and shows the problem above the buttons (S2).
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

  onMount(() => { theme.start(); void load(); });
</script>

<main class="lite">
  <header><BrandMark wordmark="Unravis Lite" /></header>
  {#if loading}
    <p role="status">Reading the Blackbox…</p>
  {:else if problem && !item}
    <p class="problem" role="alert">{problem}</p>
    <button type="button" onclick={load}>Try again</button>
  {:else if item}
    <section aria-labelledby="bb-name">
      <h1 id="bb-name">{item.name}</h1>
      {#if problem}<p class="problem" role="alert">{problem}</p>{/if}
      <p class="where">Blackbox on <span class="mono wrap">{item.location}</span></p>
      <p class="state"><Sticker info={item.state} /> <span>{item.state.detail}</span></p>
      {#if item.facts}
        <dl>
          <div><dt>Host</dt><dd>{item.facts.host}</dd></div>
          <div><dt>First reading</dt><dd>{shownTime(item.facts.firstSample, timeZone.zone)}</dd></div>
          <div><dt>Last reading</dt><dd>{shownTime(item.facts.lastSample, timeZone.zone)}{#if ageWords(item.facts.lastSample)} ({ageWords(item.facts.lastSample)}){/if}</dd></div>
          <div><dt>Readings</dt><dd>{item.facts.samples}</dd></div>
          <div><dt>Gaps</dt><dd>{T.gapsText(item.facts.gaps, item.facts.unread ?? 0, item.facts.longestGapSeconds ?? 0)}</dd></div>
        </dl>
      {/if}
      <div class="actions">
        <button type="button" class="primary" onclick={() => { open = true; }}>Open charts</button>
        <button type="button" onclick={load} aria-busy={refreshing}>Refresh</button>
        <span class="read-at" role="status">{refreshing ? 'Reading the Blackbox.' : readAt ? 'Read at ' + readAt + '.' : ''}</span>
      </div>
      <p class="note">Unravis Lite only reads. It changes nothing and saves nothing.</p>
    </section>
    {#if open}<BlackboxCharts {item} onClose={() => { open = false; }} />{/if}
  {/if}
</main>

<style>
  .lite { max-width: 760px; margin: 0 auto; padding: 24px 16px; display: grid; gap: 14px; }
  h1 { font-size: 20px; margin: 0; }
  p { margin: 0; }
  .mono { font-family: var(--mono); color: var(--text-2); }
  .wrap { overflow-wrap: anywhere; }
  .problem { color: var(--bad); }
  dl { display: grid; gap: 6px; margin: 4px 0; }
  dl div { display: flex; gap: 10px; }
  dt { width: 120px; color: var(--text-2); }
  dd { margin: 0; }
  .actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 12px; margin-top: 6px; }
  .read-at { color: var(--text-2); font-size: 13px; }
  .where { color: var(--text-2); }
  .state { display: flex; flex-wrap: wrap; align-items: baseline; gap: 8px; }
  button { padding: 6px 12px; border: 1px solid var(--control-line); border-radius: var(--radius-sm); background: var(--surface); cursor: pointer; }
  button.primary { background: var(--accent); color: var(--accent-ink); border-color: var(--accent); }
  .note { color: var(--text-2); font-size: 12px; margin-top: 16px; }
</style>
