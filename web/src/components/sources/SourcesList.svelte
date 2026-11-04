<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { listImports } from '../../lib/cases';
  import { listSourceTags, type SourceTag } from '../../lib/caseTagsApi';
  import { api } from '../../lib/api';
  import { auditSourcesOfDatabase, mergeStats, sizeLine } from '../../lib/sourceSize';
  import SourceEdit from './SourceEdit.svelte';
  import { hostsView, loadHosts } from '../../lib/wizardHosts.svelte';
  import { buildSourceList, envNames, filterByEnv, tzCounts, type ListedSource, type ListImport, type ListTag } from '../../lib/sourcesList';
  import { SOURCES_LIST_TEXT as T, TZ_STATE_LABEL, countsText, tzWhyText } from '../../lib/copy/sourcesListCopy';
  import './sources.css';

  // The Sources list of the open case (packet P11, wireframe srcItemHTML): every source with its Environment, Portal and API, a sticker for
  // the lane it feeds, and the state of its time zone (known, assumed UTC, needs a check). The labels can be edited in place, and so can the
  // time zone of a file's computer. A file's time-zone rule is fixed when the file is read, so a file with no rule or no computer is fixed by
  // adding it again (onAddFiles). An audit database also shows its size: how many documents, and how old the newest is.
  // The environment buttons are a filter. Pressing the pressed one, or "Show all", clears it.
  let { caseId, readOnly = false, onAddFiles }: { caseId: string; readOnly?: boolean; onAddFiles?: () => void } = $props();

  type Phase = 'loading' | 'ready' | 'failed';
  let phase = $state<Phase>('loading');
  let tags = $state.raw<ListTag[]>([]);
  let imports = $state.raw<ListImport[]>([]);
  let envFilter = $state('');
  let open = $state<Record<string, boolean>>({});
  /** database name -> its size line ('' while unknown). */
  let sizes = $state.raw<ReadonlyMap<string, string>>(new Map());
  let controller: AbortController | null = null;
  let token = 0;

  const list = $derived<ListedSource[]>(buildSourceList(tags, imports, hostsView.entries));
  const envs = $derived(envNames(list));
  const shown = $derived(filterByEnv(list, envs.includes(envFilter) ? envFilter : ''));
  // A filter whose environment is gone is let go, so it cannot come back by itself when the environment returns after a refresh.
  // Only once the list has been read: while it loads, the environments are not known yet.
  $effect(() => { if (phase === 'ready' && envFilter !== '' && !envs.includes(envFilter)) envFilter = ''; });
  const counts = $derived(tzCounts(list));
  const summary = $derived(countsText(counts));

  async function read() {
    controller?.abort();
    const mine = ++token;
    const own = new AbortController();
    controller = own;
    phase = 'loading';
    try {
      // Forced: a computer or zone saved on another screen since the first read must show up here.
      const [foundTags, foundImports] = await Promise.all([listSourceTags(caseId, own.signal), listImports(caseId, own.signal), loadHosts(true)]);
      if (mine !== token) return;
      tags = foundTags;
      imports = foundImports;
      phase = 'ready';
      void readSizes(foundTags, mine, own.signal);
    } catch {
      if (mine !== token) return;
      phase = 'failed';
    }
  }

  // The size line is a nicety: any failure leaves it out and the rest of the list stands. A collection that could not be read counts
  // as an unknown part, so one good read is never shown as the whole database.
  async function readSizes(found: readonly ListTag[], mine: number, signal: AbortSignal) {
    const databases = [...new Set(found.filter((tag) => tag.kind === 'mongo-database' && tag.classification === 'audit').map((tag) => tag.label))];
    if (databases.length === 0) return;
    try {
      const all = await api.sources();
      for (const database of databases) {
        const ids = auditSourcesOfDatabase(all, database);
        const parts = await Promise.all(ids.map((id) => api.sourceStats(id, signal).catch(() => ({ docs: -1, newestAt: '' }))));
        const line = sizeLine(mergeStats(parts), Date.now());
        if (mine !== token) return;
        if (line !== '') sizes = new Map(sizes).set(database, line);
      }
    } catch {
      // left out
    }
  }

  // A saved tag replaces the one with its id, or joins the list when the file had none: the list rebuilds from it.
  const onTag = (saved: SourceTag) => { tags = tags.some((tag) => tag.id === saved.id) ? tags.map((tag) => (tag.id === saved.id ? saved : tag)) : [...tags, saved]; };

  onMount(() => { void read(); });
  onDestroy(() => { token++; controller?.abort(); });

  const toggle = (key: string) => { open = { ...open, [key]: !open[key] }; };
  const pickEnv = (name: string) => { envFilter = envFilter === name ? '' : name; };
  const stateClass = (row: ListedSource): string => (row.tz === null ? '' : row.tz.state === 'known' ? 'ready' : row.tz.state === 'assumed-utc' ? 'partly' : 'failed');
</script>

<div class="src" data-sources-list>
  <div class="head">
    <h3>{T.heading}</h3>
    <p class="lead">{T.intro}</p>
  </div>

  {#if phase === 'loading'}
    <p class="hint" role="status">{T.loading}</p>
  {:else if phase === 'failed'}
    <div class="say bad" role="alert">{T.loadFailed}</div>
    <div class="actions"><button type="button" class="btn" onclick={() => void read()}>{T.retry}</button></div>
  {:else if list.length === 0}
    <p class="hint" data-testid="srclist-empty">{T.empty}</p>
  {:else}
    {#if summary !== ''}<p class="hint" data-testid="srclist-counts"><b>{T.countsLabel}:</b> {summary}</p>{/if}
    {#if envs.length > 0}
      <div class="actions" role="group" aria-label={T.filterLabel} data-testid="srclist-filter">
        <button type="button" class="btn" aria-pressed={envFilter === ''} onclick={() => { envFilter = ''; }}>{T.filterAll}</button>
        {#each envs as name (name)}
          <button type="button" class="btn" aria-pressed={envFilter === name} onclick={() => pickEnv(name)}>{name}</button>
        {/each}
      </div>
    {/if}
    {#if shown.length === 0}
      <p class="hint">{T.filterNone}</p>
    {:else}
      <ul class="rows srclist" data-testid="srclist-rows">
        {#each shown as row (row.key)}
          <li class="srcitem" data-source-row={row.key}><div class="stack">
            <div class="srow">
              <span class="sname"><b class="name">{row.name}</b></span>
              {#if row.sticker !== ''}<span class="src-sticker" data-testid="srclist-lane">{row.sticker}</span>{/if}
              {#if row.tz}<span class="src-sticker {stateClass(row)}" data-testid="srclist-tz" data-tz={row.tz.state}>{TZ_STATE_LABEL[row.tz.state]}</span>{/if}
              <span class="sp"></span>
              <button type="button" class="btn quiet" aria-expanded={!!open[row.key]} aria-controls="srclist-d-{row.key}" onclick={() => toggle(row.key)}>{open[row.key] ? T.lessLabel : T.moreLabel}<span class="sr"> {row.name}</span></button>
            </div>
            <p class="hint tags" data-testid="srclist-tags">
              {#if row.env !== ''}{row.env}{:else}<span class="warnc">{T.noEnv}</span>{/if}{#if row.portal !== ''}{' · ' + row.portal}{/if}{#if row.api !== ''}{' · ' + row.api}{/if}
            </p>
            {#if row.database !== '' && sizes.get(row.database)}<p class="hint tags" data-testid="srclist-size">{sizes.get(row.database)}</p>{/if}
            {#if open[row.key]}
              <div class="sedit" id="srclist-d-{row.key}">
                <dl class="facts">
                  <dt>{T.environment}</dt><dd>{row.env !== '' ? row.env : T.notNamed}</dd>
                  <dt>{T.portal}</dt><dd>{row.portal !== '' ? row.portal : T.notNamed}</dd>
                  <dt>{T.api}</dt><dd>{row.api !== '' ? row.api : T.notNamed}</dd>
                  {#if row.isFile}<dt>{T.computer}</dt><dd>{row.host !== '' ? row.host : T.notNamed}</dd>{/if}
                </dl>
                {#if row.tz}
                  <p class="say" class:bad={row.tz.state === 'needs-check'} data-testid="srclist-why">{tzWhyText(row.tz)}</p>
                  {#if row.isFile && row.tz.state !== 'known' && !(!readOnly && row.host !== '' && (row.tz.why === 'no-zone' || row.tz.why === 'host-gone'))}
                    <p class="hint">{T.fixHelp}</p>
                    {#if onAddFiles}<div class="actions"><button type="button" class="btn" disabled={readOnly} onclick={onAddFiles}>{T.addFiles}</button></div>{/if}
                  {/if}
                {/if}
                <SourceEdit {caseId} {row} {envs} {readOnly} {onTag} />
              </div>
            {/if}
          </div></li>
        {/each}
      </ul>
    {/if}
    <div class="actions"><button type="button" class="link" onclick={() => void read()}>{T.refresh}</button></div>
  {/if}
</div>

<style>
  .head { display: grid; gap: var(--space-1); }
  .stack { display: grid; gap: var(--space-1); flex: 1 1 100%; min-width: 0; }
  .srow { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); min-width: 0; }
  .sname { min-width: 0; overflow-wrap: anywhere; }
  .sp { flex: 1 1 auto; }
  .tags { margin: 0; }
  .warnc { color: var(--warn); font-weight: 600; }
  .sedit { display: grid; gap: var(--space-2); padding: var(--space-3); background: var(--surface); border: var(--hairline-width, 1px) solid var(--line); border-radius: var(--radius-sm); }
  .facts { display: grid; grid-template-columns: max-content 1fr; gap: var(--space-1) var(--space-3); margin: 0; font-size: var(--fs-3); }
  .facts dt { color: var(--text-2); }
  .facts dd { margin: 0; overflow-wrap: anywhere; }
  .quiet { min-height: 36px; }
  .sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
</style>
