<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchOrphans } from '../archiveApi';
  import { orphansFor, type OrphanReport } from '../archiveModel';
  import { ARCHIVE_TEXT } from '../copy/archiveCopy';
  import { formatBytes } from '../copy/importCopy';
  import { formatTime } from '../time';

  // Case files an archive finished but did not commit (Cancel at the last moment, or a stop at the wrong second). Unravis lists them and never deletes
  // or adopts one: the person keeps, moves or deletes them. The list is empty and silent when there are none.
  let { caseId }: { caseId: string } = $props();

  let report = $state.raw<OrphanReport | null>(null);
  let failed = $state(false);

  const mine = $derived(report ? orphansFor(report, caseId) : []);
  const when = (value: string): string => formatTime(value, { year: true, precision: 'minute', fallback: '' });

  onMount(() => {
    const controller = new AbortController();
    void (async () => {
      try { report = await fetchOrphans(controller.signal); } catch (error) {
        if (!(error instanceof DOMException && error.name === 'AbortError')) failed = true;
      }
    })();
    return () => controller.abort();
  });
</script>

{#if failed}
  <p class="inline-error" role="alert">{ARCHIVE_TEXT.orphanLoadFailed}</p>
{:else if mine.length > 0 || report?.verifyCleanupPending}
  <section class="orphans" aria-labelledby="orphan-title">
    <h4 id="orphan-title">{ARCHIVE_TEXT.orphanHeading}</h4>
    {#if mine.length > 0}
      <p class="note">{ARCHIVE_TEXT.orphanIntro}</p>
      <ul>
        {#each mine as file (file.folder + '/' + file.name)}
          <li><strong>{file.name}</strong> <span>{file.folder}{file.at ? ' · ' + ARCHIVE_TEXT.orphanMade + ' ' + when(file.at) : ''}{file.bytes > 0 ? ' · ' + formatBytes(file.bytes) : ''}</span></li>
        {/each}
      </ul>
    {/if}
    {#if report?.verifyCleanupPending}<p class="note">{ARCHIVE_TEXT.orphanCleanup}</p>{/if}
  </section>
{/if}

<style>
  .orphans { display: grid; gap: 6px; padding-top: 10px; border-top: 1px solid var(--line-soft); }
  .orphans h4 { margin: 0; font-size: 14px; }
  .note { margin: 0; color: var(--text-2); font-size: 13px; }
  ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 6px; }
  li { font-size: 14px; overflow-wrap: anywhere; }
  li span { color: var(--text-2); font-size: 13px; }
</style>
