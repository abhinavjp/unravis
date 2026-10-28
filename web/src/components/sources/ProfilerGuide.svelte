<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchProfilerGuide, type ProfilerGuide } from '../../lib/lanes';
  import { GUIDE_TEXT, GRANT_TEXT } from '../../lib/copy/sourcesCopy';
  import './sources.css';

  // "Switched on?" The MongoDB profiler is how slow operations are seen. Unravis cannot switch it on: this panel shows a guide and the scripts
  // for the owner to run. `guide` is used when a read already brought one; otherwise it is fetched from /api/grant-scripts.
  let { database = '', guide = null }: { database?: string; guide?: ProfilerGuide | null } = $props();

  let fetched = $state<ProfilerGuide | null>(null);
  let failed = $state(false);
  let copied = $state('');
  const shown = $derived(guide ?? fetched);

  onMount(() => {
    if (guide) return;
    const mine = new AbortController();
    fetchProfilerGuide(database.trim(), mine.signal)
      .then((g) => { if (!mine.signal.aborted) { fetched = g; failed = g === null; } })
      .catch(() => { if (!mine.signal.aborted) failed = true; });
    return () => mine.abort();
  });

  async function copy(id: string, text: string) {
    try { await navigator.clipboard.writeText(text); copied = id; } catch { copied = 'no'; }
  }
</script>

<section class="src panel" aria-labelledby="guide-head">
  <h4 id="guide-head">{GUIDE_TEXT.heading}</h4>
  <p class="lead">{GUIDE_TEXT.unravisCannot}</p>
  {#if failed}<p class="say bad" role="alert">{GUIDE_TEXT.loadFailed}</p>{/if}
  {#if shown}
    <div class="field"><h5>{GUIDE_TEXT.why}</h5><p>{shown.why}</p></div>
    <div class="field"><h5>{GUIDE_TEXT.cost}</h5><p>{shown.cost}</p></div>
    {#if shown.steps.length > 0}
      <div class="field">
        <h5>{GUIDE_TEXT.steps}</h5>
        <ol class="hint">{#each shown.steps as step, i (i)}<li>{step}</li>{/each}</ol>
      </div>
    {/if}
    <p class="say">{GUIDE_TEXT.changes}</p>
    {#each [{ id: 'check', label: GUIDE_TEXT.check, text: shown.check }, { id: 'on', label: GUIDE_TEXT.switchOn, text: shown.switchOn }, { id: 'off', label: GUIDE_TEXT.switchOff, text: shown.switchOff }] as item (item.id)}
      {#if item.text !== ''}
        <div class="field">
          <h5>{item.label}</h5>
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <pre class="script" tabindex="0" aria-label={item.label}>{item.text}</pre>
          <div class="actions">
            <button type="button" class="btn" onclick={() => void copy(item.id, item.text)}>{GRANT_TEXT.copy}</button>
            <span aria-live="polite" class="hint">{copied === item.id ? GRANT_TEXT.copied : copied === 'no' ? GRANT_TEXT.copyFailed : ''}</span>
          </div>
        </div>
      {/if}
    {/each}
  {/if}
</section>
