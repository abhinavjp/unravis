<script lang="ts">
  import { tick } from 'svelte';
  import { UPD_TEXT as T } from '../lib/copy/updatesCopy';
  import { shortVersion, type ReleaseNotes } from '../lib/updateNotes';
  import { formatNoteDate } from '../lib/updatesPage';
  import { estimateRow, indexAt, offsets, windowRange } from '../lib/virtualList';

  // The history of updates, newest first. Only the rows near the visible part are in the page; the rest is empty space of the right
  // height, so a long history stays quick. A row's real height is measured once it is on screen. This list is the only part of the page that scrolls.
  type Pending = { version: string; kind: 'ready' | 'installing' | 'failed'; current: string; link: string };
  let { releases, installed, pending = null }: { releases: ReleaseNotes[]; installed: string; pending?: Pending | null } = $props();

  type Row = { key: string; pending?: Pending; release?: ReleaseNotes };
  const rows = $derived<Row[]>([...(pending ? [{ key: 'pending:' + pending.version, pending }] : []), ...releases.map((release) => ({ key: release.version, release }))]);
  const PENDING_HEIGHT = 120;

  let scroller = $state<HTMLElement>();
  let scrollTop = $state(0);
  let viewport = $state(0);
  // Measured heights by row key. A row nobody has seen yet uses an estimate.
  let measured = $state<Record<string, number>>({});
  let opened = $state<Record<string, boolean>>({});

  const heights = $derived(rows.map((row) => measured[row.key] ?? (row.release ? estimateRow(row.release) : PENDING_HEIGHT)));
  const offs = $derived(offsets(heights));
  const range = $derived(windowRange(offs, scrollTop, viewport));
  const visible = $derived(rows.slice(range.start, range.end).map((row, k) => ({ row, index: range.start + k })));

  function onScroll() { if (scroller) scrollTop = scroller.scrollTop; }

  // Measuring rows: when a row changes height, the row at the top of the view stays where it is (the scroll position is corrected).
  let observer: ResizeObserver | undefined;
  function onResize(entries: ResizeObserverEntry[]) {
    if (!scroller) return;
    const keys = rows.map((row) => row.key);
    const anchor = indexAt(offs, scroller.scrollTop);
    const anchorKey = keys[anchor];
    const delta = scroller.scrollTop - offs[anchor];
    let changed = false;
    for (const entry of entries) {
      const key = (entry.target as HTMLElement).dataset.key ?? '';
      const height = Math.round((entry.target as HTMLElement).offsetHeight);
      const index = keys.indexOf(key);
      if (!height || index < 0 || Math.abs(heights[index] - height) <= 1) continue;
      measured[key] = height;
      changed = true;
    }
    if (!changed) return;
    void tick().then(() => {
      if (!scroller) return;
      const index = rows.findIndex((row) => row.key === anchorKey);
      if (index < 0) return;
      const want = offs[index] + delta;
      if (Math.abs(want - scroller.scrollTop) > 0.5) { scroller.scrollTop = want; scrollTop = scroller.scrollTop; }
    });
  }
  function watch(node: HTMLElement) {
    observer ??= new ResizeObserver(onResize);
    observer.observe(node);
    return { destroy() { observer?.unobserve(node); } };
  }
  $effect(() => {
    if (!scroller) return;
    const box = new ResizeObserver(() => { viewport = scroller?.clientHeight ?? 0; });
    box.observe(scroller);
    viewport = scroller.clientHeight;
    return () => { box.disconnect(); observer?.disconnect(); observer = undefined; };
  });

  function toggle(version: string, event: Event) { opened[version] = (event.currentTarget as HTMLDetailsElement).open; }
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div class="hist" bind:this={scroller} onscroll={onScroll} role="region" aria-label={T.historyLabel} tabindex="0" data-testid="update-history" data-drawn={visible.length} data-total={rows.length}>
  <div class="inner" style:padding-top="{offs[range.start] ?? 0}px" style:padding-bottom="{(offs[rows.length] ?? 0) - (offs[range.end] ?? 0)}px">
    {#each visible as { row, index } (row.key)}
      <div class="row" data-key={row.key} data-index={index} use:watch>
        {#if row.pending}
          {@const p = row.pending}
          <article class="release" class:first={index === 0}>
            <div class="head">
              <span class="v">{shortVersion(p.version)}</span>
              {#if p.kind === 'failed'}<span class="tag bad">{T.tagFailed}</span>{:else if p.kind === 'installing'}<span class="tag now">{T.tagInstalling}</span>{:else}<span class="tag now">{T.tagReady}</span>{/if}
            </div>
            <p class="pending-note">{p.kind === 'failed' ? T.pendingFailed(shortVersion(p.current), shortVersion(p.version)) : T.pendingNotes(shortVersion(p.version))}{#if p.link}<a href={p.link} target="_blank" rel="noopener noreferrer">{T.releasesPage}<span class="sr-only"> (opens in a new tab)</span></a>{/if}</p>
          </article>
        {:else if row.release}
          {@const r = row.release}
          <article class="release" class:first={index === 0}>
            <div class="head">
              <span class="v">{shortVersion(r.version)}</span>
              {#if r.version === installed}<span class="tag now">{T.tagInstalled}</span>{/if}
              {#if r.date}<span class="date">{formatNoteDate(r.date)}</span>{/if}
            </div>
            {#if r.headline}<h3>{r.headline}</h3>{/if}
            {#if r.highlights.length}
              <ul class="items">
                {#each r.highlights as item, n (n)}
                  <li><span class="txt">{#if item.label}<b>{item.label}</b> {/if}{item.text}</span>{#if item.image}<img class="thumb" src={item.image} alt={item.alt ?? ''} loading="lazy" decoding="async" width="132" height="82" />{/if}</li>
                {/each}
              </ul>
            {:else}
              <p class="none">{T.noNotesYet}</p>
            {/if}
            {#if r.changes.length}
              <details ontoggle={(event) => toggle(r.version, event)} open={!!opened[r.version]}>
                <summary>{T.allChanges(r.changes.length)}</summary>
                {#if opened[r.version]}
                  <p class="all-note">{T.allChangesHelp}</p>
                  <ul class="all">{#each r.changes as line, n (n)}<li>{line}</li>{/each}</ul>
                {/if}
              </details>
            {/if}
          </article>
        {/if}
      </div>
    {/each}
  </div>
</div>

<style>
  .hist { flex: 1 1 0; min-height: 0; overflow-y: auto; overscroll-behavior: contain; margin: 0 -26px; padding: 0 26px; }
  .hist:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
  @media (max-width: 720px) { .hist { margin: 0 -15px; padding: 0 15px; } }
  .inner { display: block; }
  .release { padding: var(--space-4) 0; border-top: 1px solid var(--hairline); }
  .release.first { border-top: 0; }
  .head { display: flex; flex-wrap: wrap; align-items: center; gap: 4px var(--space-2); }
  .v { font-size: var(--fs-5, 16px); font-weight: 650; letter-spacing: -.01em; font-variant-numeric: tabular-nums; }
  .date { margin-left: auto; color: var(--text-2); font-size: 13px; }
  .tag { padding: 1px 9px; font-size: 12px; font-weight: 600; color: var(--text); border: 1px solid var(--control-line); border-radius: 999px; }
  .tag.now { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 10%, transparent); }
  .tag.bad { border-color: var(--bad); background: color-mix(in srgb, var(--bad) 10%, transparent); }
  h3 { margin: var(--space-1) 0 0; font-size: 14px; font-weight: 600; line-height: 1.4; overflow-wrap: anywhere; }
  .items { margin: var(--space-2) 0 0; padding: 0; list-style: none; display: grid; gap: var(--space-2); }
  .items li { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--space-3); }
  .txt { min-width: 0; font-size: 13.5px; line-height: 1.5; color: var(--text); overflow-wrap: anywhere; }
  .thumb { flex: 0 0 auto; width: 132px; height: 82px; object-fit: cover; object-position: top left; border: 1px solid var(--line); border-radius: var(--radius-sm); background: var(--surface-2); }
  .none, .pending-note { margin: var(--space-2) 0 0; color: var(--text-2); font-size: 13px; line-height: 1.5; }
  .pending-note a { color: var(--accent); }
  details { margin-top: var(--space-2); }
  summary { width: fit-content; font-size: 13px; font-weight: 500; color: var(--accent); }
  .all-note { margin: var(--space-2) 0 0; color: var(--text-2); font-size: 12px; }
  .all { margin: var(--space-2) 0 0; padding-left: 18px; color: var(--text-2); font-size: 12.5px; line-height: 1.5; }
  .all li { margin: 2px 0; overflow-wrap: anywhere; }
  .sr-only { position: absolute; width: 1px; height: 1px; margin: -1px; padding: 0; border: 0; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
</style>
