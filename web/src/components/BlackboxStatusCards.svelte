<script lang="ts">
  import { shownTime, type BlackboxItem, type ThisPc } from '../lib/blackbox';
  import { stripParts, techCards } from '../lib/blackboxStatus';
  import { BBS_TEXT as T } from '../lib/copy/blackboxStatusCopy';
  import { timeZone } from '../lib/timeZone.svelte';

  // The summary line and the cards of the Blackbox status screen: how much is recorded and, for each part it looks at, what Unravis could read.
  // Every figure comes from the recorder's own files and status page or from its settings. A part with nothing read has no card.
  let { item, thisPc }: { item: BlackboxItem; thisPc: ThisPc } = $props();

  const when = (iso: string) => shownTime(iso, timeZone.zone);
  const parts = $derived(stripParts(item, item.facts?.firstSample ? when(item.facts.firstSample) : ''));
  const cards = $derived(techCards(item, thisPc, when));
</script>

{#if parts.length}
  <p class="bbs-strip" role="group" aria-label={T.stripLabel}>{#each parts as p, i (i)}{#if i > 0}<span class="bbs-dot" aria-hidden="true">·</span>{/if}<span>{p}</span>{/each}</p>
{/if}
{#if cards.length}
  <section class="bbs-cards" aria-labelledby="bbs-cards-title">
    <h3 id="bbs-cards-title">{T.cardsTitle}</h3>
    <p class="bbs-intro">{T.cardsIntro}</p>
    <ul>
      {#each cards as c (c.id)}
        <li data-card={c.id}>
          <div class="bbs-head"><strong>{c.title}</strong><span class="bbs-word {c.tone}">{c.word}</span></div>
          {#if c.sub}<p class="bbs-sub">{c.sub}</p>{/if}
          <p>{c.note}</p>
        </li>
      {/each}
    </ul>
  </section>
{/if}

<style>
  .bbs-strip { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 8px; margin: 0; font-size: 13px; color: var(--text-2); }
  .bbs-dot { color: var(--text-2); }
  .bbs-cards { display: grid; gap: var(--space-2); }
  .bbs-cards h3 { margin: 0; font-size: var(--fs-5); font-weight: 600; }
  .bbs-intro { margin: 0; font-size: 12px; color: var(--text-2); }
  ul { display: grid; grid-template-columns: repeat(auto-fit, minmax(220px, 1fr)); gap: var(--space-3); margin: 0; padding: 0; list-style: none; }
  li { display: grid; align-content: start; gap: 4px; padding: var(--space-3); border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface); min-width: 0; }
  li p { margin: 0; font-size: 12px; line-height: 1.5; overflow-wrap: anywhere; }
  .bbs-head { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); font-size: 13px; }
  .bbs-sub { color: var(--text-2); }
  .bbs-word { padding: 2px 10px; border-radius: 999px; font-size: 12px; font-weight: 600; line-height: 1.5; white-space: nowrap; color: var(--text-2); background: var(--surface-2); }
  .bbs-word.good { color: var(--tone-ok); background: color-mix(in srgb, var(--tone-ok) 10%, var(--surface)); }
  .bbs-word.warn { color: var(--warn); background: color-mix(in srgb, var(--warn) 10%, var(--surface)); }
  .bbs-word.bad { color: var(--bad); background: color-mix(in srgb, var(--bad) 10%, var(--surface)); }
</style>
