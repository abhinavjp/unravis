<script lang="ts">
  // Unravis Lite: one Blackbox, read-only. It lists the Blackbox this copy was started on and opens its charts.
  // Nothing here changes anything: the Lite server answers only the list and the chart series (internal/lite).
  import { onMount } from 'svelte';
  import BrandMark from '../src/brand/components/BrandMark.svelte';
  import BlackboxCharts from '../src/components/BlackboxCharts.svelte';
  import { blackboxApi, shownTime, type BlackboxItem } from '../src/lib/blackbox';
  import { hasSessionToken, NO_SESSION_MESSAGE } from '../src/lib/api';
  import { theme } from '../src/lib/theme.svelte';

  let item = $state<BlackboxItem | null>(null);
  let problem = $state('');
  let loading = $state(true);
  let open = $state(false);

  async function load() {
    loading = true;
    problem = '';
    if (!hasSessionToken()) {
      problem = NO_SESSION_MESSAGE.replace('Unravis', 'Unravis Lite');
      loading = false;
      return;
    }
    try {
      const list = await blackboxApi.list();
      item = list.items[0] ?? null;
      if (!item) problem = 'No Blackbox is set for this copy. Start Unravis Lite with --folder <recordings folder>.';
    } catch (e) {
      const text = e instanceof Error ? e.message : 'Could not read the Blackbox.';
      // The full app's wording points at screens Lite does not have.
      problem = text.replace(/Open Unravis from the link it printed when it started, or restart it\./, 'Open Unravis Lite from the link it printed, or start it again.');
    }
    loading = false;
  }

  onMount(() => { theme.start(); void load(); });
</script>

<main class="lite">
  <header><BrandMark wordmark="Unravis Lite" /></header>
  {#if loading}
    <p role="status">Reading the Blackbox…</p>
  {:else if problem}
    <p class="problem" role="alert">{problem}</p>
    <button type="button" onclick={load}>Try again</button>
  {:else if item}
    <section aria-labelledby="bb-name">
      <h1 id="bb-name">{item.name}</h1>
      <p class="mono wrap">{item.location}</p>
      <p><span class="tone {item.state.tone}">{item.state.label}</span> {item.state.detail}</p>
      {#if item.facts}
        <dl>
          <div><dt>Host</dt><dd>{item.facts.host}</dd></div>
          <div><dt>First reading</dt><dd>{shownTime(item.facts.firstSample)}</dd></div>
          <div><dt>Last reading</dt><dd>{shownTime(item.facts.lastSample)}</dd></div>
          <div><dt>Readings</dt><dd>{item.facts.samples}</dd></div>
          <div><dt>Gaps</dt><dd>{item.facts.gaps}</dd></div>
        </dl>
      {/if}
      <div class="actions">
        <button type="button" class="primary" onclick={() => { open = true; }}>Open charts</button>
        <button type="button" onclick={load}>Refresh</button>
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
  .mono { font-family: var(--mono); color: var(--text-muted, inherit); }
  .wrap { overflow-wrap: anywhere; }
  .problem { color: var(--bad); }
  dl { display: grid; gap: 6px; margin: 4px 0; }
  dl div { display: flex; gap: 10px; }
  dt { width: 120px; color: var(--text-muted, inherit); }
  dd { margin: 0; }
  .actions { display: flex; gap: 8px; margin-top: 6px; }
  button { padding: 6px 12px; border: 1px solid var(--control-line); border-radius: var(--radius-sm); background: var(--surface); cursor: pointer; }
  button.primary { background: var(--accent); color: var(--on-accent, #fff); border-color: var(--accent); }
  .tone { font-weight: 600; }
  .tone.bad { color: var(--bad); }
  .note { color: var(--text-muted, inherit); font-size: 12px; }
</style>
