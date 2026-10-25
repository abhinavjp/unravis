<script lang="ts">
  import type { Snippet } from 'svelte';

  // One group of the More filters panel: a titled section with an optional count of its active filters and an optional one-line lede.
  // The optional help snippet holds the group's HelpNote, one compact note under the title. Its rules (the top line between groups, the title and count type) live in app.css under "More filters panel".
  let { id, title, count = 0, lede, help, children }: { id: string; title: string; count?: number; lede?: Snippet; help?: Snippet; children: Snippet } = $props();
</script>

<section class="filter-group" data-group={id} aria-labelledby={'fg-' + id + '-title'}>
  <h2 class="fg-title" id={'fg-' + id + '-title'}>{title}{#if count}<span class="fg-count">{count}<span class="visually-hidden"> in use</span></span>{/if}</h2>
  {#if lede}<p class="fg-lede">{@render lede()}</p>{/if}
  {#if help}<div class="fg-help">{@render help()}</div>{/if}
  {@render children()}
</section>