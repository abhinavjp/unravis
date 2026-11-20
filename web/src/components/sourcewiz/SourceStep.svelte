<script lang="ts">
  import { tick } from 'svelte';
  import { wizard } from '../../lib/wizard.svelte';
  import { sourcesView, removeChosen } from '../../lib/wizardSources.svelte';
  import { sourceKey } from '../../lib/wizardSources';
  import { groupOf } from '../../lib/discover';
  import { releaseOrphanFiles } from '../../lib/wizardFiles.svelte';
  import { railGroups, connectPrefill } from '../../lib/sourceWizard';
  import { DISCOVER_TEXT } from '../../lib/copy/discoverCopy';
  import { kindLabel } from '../../lib/copy/wizardSourcesCopy';
  import { WIZ } from '../../lib/copy/sourceWizardCopy';
  import { FILES_TEXT, dropUnreadableText } from '../../lib/copy/fileKindsCopy';
  import { filesFromDrop } from '../../lib/dropEntries';
  import MongoClassify from '../wizard/MongoClassify.svelte';
  import SqlServerForm from '../wizard/SqlServerForm.svelte';
  import ReachableCheck from '../wizard/ReachableCheck.svelte';
  import ImportFilesPanel from '../wizard/ImportFilesPanel.svelte';
  import SourceTagRow from '../wizard/SourceTagRow.svelte';
  import AddAnotherDay from '../sources/AddAnotherDay.svelte';
  import FindOnPc from './FindOnPc.svelte';
  import BlackboxCard from './BlackboxCard.svelte';

  // The one "Add sources" step, for a new case and for an open one. Left: what is already chosen, grouped by type. Right: pick a kind, fill
  // its short card, come back to the list. Nothing is read here: the last button of the wizard does the reading.
  let { adding = false, caseId = '', closed = false, existing = [] }: { adding?: boolean; caseId?: string; closed?: boolean; existing?: Array<{ kind: string; label: string }> } = $props();

  type Card = '' | 'pc' | 'mongo' | 'sql' | 'redis' | 'files' | 'blackbox' | 'another';
  let card = $state<Card>('');
  let heading = $state<HTMLHeadingElement>();
  let dropNote = $state('');
  let cardEl = $state<HTMLDivElement>();
  let seed = $state<{ files: File[]; folder: boolean; says: string[] } | undefined>();
  let from = $state('');
  let connected = $state<string[]>([]);

  const tiles: Array<{ id: Exclude<Card, ''>; name: string; sub: string; badge?: string }> = $derived([
    { id: 'pc', ...WIZ.tiles.pc }, { id: 'mongo', ...WIZ.tiles.mongo }, { id: 'sql', ...WIZ.tiles.sql }, { id: 'redis', ...WIZ.tiles.redis },
    { id: 'files', ...WIZ.tiles.files }, { id: 'blackbox', ...WIZ.tiles.blackbox },
    ...(adding ? [{ id: 'another' as const, ...WIZ.tiles.another }] : [])
  ]);

  // "N added" on a tile: how many of the chosen sources came from that kind of card.
  const addedOf = (id: Exclude<Card, ''>): number => {
    const kinds: Record<string, string[]> = { mongo: ['mongo-database'], sql: ['sql-server'], redis: ['redis'], files: ['iis-logs', 'app-log', 'hangfire-csv', 'redis-slowlog-csv', 'file'], blackbox: ['blackbox'] };
    return wizard.sources.filter((s) => (kinds[id] ?? []).includes(s.kind)).length;
  };
  const titleOf = (s: { kind: string; ref: string }): string => {
    if (s.kind !== 'discover') return kindLabel(s.kind);
    const f = sourcesView.findings.find((x) => x.id === s.ref);
    return f ? DISCOVER_TEXT.groups[groupOf(f)] : kindLabel('discover');
  };
  const groups = $derived(railGroups(wizard.sources, titleOf));
  const existingGroups = $derived(railGroups(existing.map((e) => ({ ...e, ref: e.label })), (e) => kindLabel(e.kind)));

  async function back() { card = ''; dropNote = ''; seed = undefined; await tick(); heading?.focus(); }
  async function open(c: Card, finding = '', dropped?: { files: File[]; folder: boolean; says: string[] }) { seed = dropped; card = c; dropNote = ''; from = finding; await tick(); cardEl?.focus(); }
  function markConnected() { if (from !== '' && !connected.includes(from)) connected = [...connected, from]; }
  function connectedDone() { if (from !== '' && !connected.includes(from)) connected = [...connected, from]; from = ''; card = 'pc'; }
  async function drop(event: DragEvent) {
    event.preventDefault();
    if (closed) return;
    const transfer = event.dataTransfer;
    if (!transfer) return;
    // Taken now: the browser clears the drop's items as soon as this handler yields.
    const items = Array.from(transfer.items ?? []);
    const plain = Array.from(transfer.files ?? []);
    const dropped = await filesFromDrop(items, plain);
    // A slow folder read must not override a card the person opened meanwhile, or a case that closed.
    if (closed || card !== '') return;
    if (dropped.files.length === 0) { dropNote = dropped.unreadable > 0 ? dropUnreadableText(dropped.unreadable) : FILES_TEXT.dropNoFiles; return; }
    const says: string[] = [];
    if (dropped.tooMany) says.push(FILES_TEXT.dropTruncated);
    if (dropped.tooDeep) says.push(FILES_TEXT.dropTooDeep);
    if (dropped.unreadable > 0) says.push(dropUnreadableText(dropped.unreadable));
    await open('files', '', { files: dropped.files, folder: dropped.folder, says });
  }
  async function remove(key: string) { removeChosen(key); releaseOrphanFiles(); await tick(); heading?.focus({ preventScroll: true }); }
</script>

<div class="step">
  <p class="nutshell"><strong>{WIZ.nutshell}:</strong> {WIZ.nutshellText}</p>
  {#if closed}<p class="inline-error" role="alert">{WIZ.closedCase}</p>{/if}

  <div class="layout">
    <aside class="rail" aria-label={WIZ.railHeading}>
      {#if adding && existing.length > 0}
        <h4>{WIZ.alreadyHeading}</h4>
        {#each existingGroups as g (g.title)}
          <p class="gtitle">{g.title}</p>
          <ul>{#each g.items as e, i (i)}<li class="ro">{e.label}</li>{/each}</ul>
        {/each}
      {/if}
      <div class="railtop"><h4 tabindex="-1" bind:this={heading}>{adding ? WIZ.toAddHeading : WIZ.railHeading}</h4><span class="count" aria-hidden="true">{wizard.sources.length}</span></div>
      {#if wizard.sources.length === 0}<p class="empty">{WIZ.railEmpty}</p>{/if}
      {#each groups as g (g.title)}
        <p class="gtitle">{g.title}</p>
        <ul>
          {#each g.items as s (sourceKey(s))}
            <li class="item"><span class="lbl">{s.label || s.ref}</span><button type="button" class="quiet-button" aria-label="{WIZ.remove} {s.label || s.ref}" onclick={() => void remove(sourceKey(s))}>{WIZ.remove}</button></li>
          {/each}
        </ul>
      {/each}
      <p class="tray" role="status">{WIZ.tray(wizard.sources.length)}</p>
    </aside>

    <section class="main">
      {#if card === ''}
        <h3>{adding ? WIZ.askAdd : WIZ.askNew}</h3>
        <ul class="tiles">
          {#each tiles as t (t.id)}
            <li><button type="button" class="tile" class:start={t.id === 'pc'} disabled={closed} onclick={() => void open(t.id)}>
              <span class="tname">{t.name}{#if t.badge}<span class="badge">{t.badge}</span>{/if}{#if addedOf(t.id) > 0}<span class="added">{WIZ.addedBadge(addedOf(t.id))}</span>{/if}</span><span class="tsub">{t.sub}</span></button></li>
          {/each}
        </ul>
        <p class="hint">{WIZ.hint}</p>
        {#if dropNote}<p class="hint" role="status">{dropNote}</p>{/if}
        <div class="dropzone" class:off={closed} role="button" tabindex={closed ? -1 : 0} aria-disabled={closed} ondragover={(e) => e.preventDefault()} ondrop={(e) => void drop(e)} onclick={() => { if (!closed) void open('files'); }} onkeydown={(e) => { if (!closed && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); void open('files'); } }}>{WIZ.drop}</div>
        {#if wizard.sources.length > 0}
          <details class="names">
            <summary>{WIZ.namesHeading}</summary>
            <ul class="mine">{#each wizard.sources as s (sourceKey(s))}<SourceTagRow source={s} onRemoved={() => void remove(sourceKey(s))} />{/each}</ul>
          </details>
        {/if}
      {:else}
        <div class="cardwrap" tabindex="-1" bind:this={cardEl}>
        <div class="cardbar"><button type="button" class="secondary-button" onclick={() => void back()}>{WIZ.back}</button></div>
        {#if dropNote}<p class="hint" role="status">{dropNote}</p>{/if}
        {#if card === 'pc'}<FindOnPc {connected} onCard={(c, f) => void open(c, f ?? '')} onDone={() => void back()} />
        {:else if card === 'mongo'}{#if from !== ''}<p class="hint">{WIZ.find.mongoPrefill} <code>{connectPrefill('mongo')}</code></p>{/if}<MongoClassify prefill={from !== '' ? connectPrefill('mongo') : ''} onTested={markConnected} onDone={() => (from !== '' ? connectedDone() : void back())} />
        {:else if card === 'sql'}{#if from !== ''}<p class="hint">{WIZ.find.sqlPrefill} <code>{connectPrefill('sql')}</code></p>{/if}<SqlServerForm prefill={from !== '' ? connectPrefill('sql') : ''} onDone={() => (from !== '' ? connectedDone() : void back())} />
        {:else if card === 'redis'}<ReachableCheck kind="redis" onDone={() => void back()} />
        {:else if card === 'files'}<ImportFilesPanel {seed} onCancel={() => void back()} />
        {:else if card === 'blackbox'}<BlackboxCard onDone={() => void back()} />
        {:else if card === 'another' && caseId !== ''}<AddAnotherDay {caseId} readOnly={closed} onDone={() => void back()} onCancel={() => void back()} />{/if}
        </div>
      {/if}
      <p class="hint foot">{adding ? WIZ.footerAdd : WIZ.footer}</p>
    </section>
  </div>
</div>

<style>
  .step { display: grid; gap: var(--space-4); min-width: 0; max-width: 1000px; }
  .nutshell { margin: 0; color: var(--text-2); font-size: var(--fs-4); max-width: 70ch; }
  .layout { display: grid; grid-template-columns: minmax(180px, 260px) minmax(0, 1fr); gap: var(--space-5); align-items: start; }
  @media (max-width: 720px) { .layout { grid-template-columns: minmax(0, 1fr); } }
  .rail { display: grid; gap: var(--space-2); align-content: start; min-width: 0; padding: var(--space-4); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .railtop { display: flex; align-items: center; justify-content: space-between; gap: var(--space-2); }
  .count { min-width: 22px; padding: 0 var(--space-2); text-align: center; color: var(--text-2); background: var(--surface-2); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-size: var(--fs-2); }
  .empty { margin: 0; padding: var(--space-3); color: var(--text-2); font-size: var(--fs-3); border: var(--hairline-width) dashed var(--control-line); border-radius: var(--radius-sm); }
  .rail h4, h3 { margin: 0; font-size: var(--fs-5); font-weight: 600; }
  .rail h4:focus { outline: none; } .rail h4:focus-visible { outline: 2px solid var(--focus); outline-offset: 4px; }
  .gtitle { margin: var(--space-3) 0 0; color: var(--text-2); font-weight: 600; font-size: var(--fs-3); }
  .rail ul { margin: 0; padding: 0; list-style: none; display: grid; gap: var(--space-1); max-height: 28vh; overflow-y: auto; }
  .rail li { display: flex; gap: var(--space-2); align-items: center; justify-content: space-between; min-width: 0; }
  .item { padding: var(--space-2) var(--space-3); background: var(--bg); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .item .quiet-button { flex-shrink: 0; font-size: var(--fs-2); }
  .lbl { overflow-wrap: anywhere; min-width: 0; }
  .ro { color: var(--text-2); }
  .tray { margin: var(--space-2) 0 0; font-weight: 600; }
  .main { display: grid; gap: var(--space-3); min-width: 0; align-content: start; }
  .tiles { margin: 0; padding: 0; list-style: none; display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); grid-auto-rows: 1fr; gap: var(--space-3); }
  @media (max-width: 900px) { .tiles { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
  @media (max-width: 520px) { .tiles { grid-template-columns: minmax(0, 1fr); } }
  .tiles li { display: grid; min-width: 0; }
  .tile { box-sizing: border-box; width: 100%; height: 100%; display: grid; align-content: start; gap: var(--space-1); text-align: left; padding: var(--space-3) var(--space-4); min-height: 84px; background: var(--surface); border: var(--hairline-width) solid var(--control-line); border-radius: var(--radius-sm); cursor: pointer; font: inherit; color: inherit; }
  .tile:hover:not(:disabled) { background: var(--surface-2); }
  .tile:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .tile.start { border-color: var(--accent); box-shadow: inset 0 0 0 1px var(--accent); }
  .tile:disabled { opacity: .55; cursor: not-allowed; }
  .tname { font-weight: 600; font-size: var(--fs-4); display: flex; flex-wrap: wrap; gap: var(--space-2); align-items: center; justify-content: space-between; }
  .badge { padding: 0 var(--space-2); font-size: var(--fs-2); border: var(--hairline-width) solid var(--accent); border-radius: var(--radius-sm); }
  .added { padding: 0 var(--space-2); font-size: var(--fs-2); color: var(--accent-ink); background: var(--accent); border-radius: var(--radius-sm); }
  .tsub { color: var(--text-2); font-size: var(--fs-3); }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .dropzone { padding: var(--space-4); text-align: center; color: var(--text-2); border: 2px dashed var(--control-line); border-radius: var(--radius-sm); cursor: pointer; }
  .dropzone:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .names summary { cursor: pointer; font-weight: 600; padding: var(--space-2) 0; }
  .mine { margin: 0; padding: 0; list-style: none; display: grid; gap: var(--space-3); }
  .cardwrap { display: grid; gap: var(--space-3); min-width: 0; padding: var(--space-4); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); }
  .cardbar { display: flex; justify-content: flex-end; } .cardwrap:focus { outline: none; }
  .dropzone.off { opacity: .55; cursor: not-allowed; }
  .foot { margin-top: var(--space-2); }
</style>
