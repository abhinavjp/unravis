<script lang="ts">
  import type { CheckFileVerdict, CheckReport } from '../../cases';
  import { POPUP_TEXT } from '../../copy/importPopupCopy';
  import { formatBytes, importerLabel, reasonText } from '../../copy/importCopy';
  import { timeZoneRule, type StagedFile } from '../../fileKinds';
  import FixCard from './FixCard.svelte';
  import GroupedList from './GroupedList.svelte';
  import NotSupported, { type UnsupportedFile } from './NotSupported.svelte';
  import { cardIdOf, fileKeyOf, groupOf, type CardState } from './popupState.svelte';

  // Step 2 of the "Import files" popup (wireframe: step2Checking and step2Results). While the server checks the files it shows a spinner.
  // Then, top to bottom: the groups that are ready, one fix card for each server name the computer list lacks, one card for each kind of
  // file that names no computer (or needs a time zone), and the files that are not supported. The popup state owns every decision;
  // this body only draws it and reports what the person did, using the card and group names of popupState.svelte.ts.
  // The footer (fix hint, acknowledgement, Import button) is drawn by the shell. `onResolve` is how the shell learns a fix was saved,
  // so it can check again and show a case the first check missed.
  let {
    report, files, checking = false, cards = {}, expanded = {}, selected = {}, kinds = {}, zone = 'UTC', totalServers,
    onResolve, onLeaveOut, onUndo, onAddAll, onRemoveUnsupported, onPickKind, onToggle, onGroupAll, onSelect
  }: {
    report: CheckReport | null;
    files: StagedFile[];
    checking?: boolean;
    /** Keyed by card id. A card with no entry is open. */
    cards?: Record<string, CardState>;
    /** Keyed by group or card name. */
    expanded?: Record<string, boolean>;
    /** Keyed by file key. A file with no entry counts as ticked. */
    selected?: Record<string, boolean>;
    /** The kind picked for a file the check could not place, keyed by file key. */
    kinds?: Record<string, string>;
    /** The time zone offered first on a card that asks for one. */
    zone?: string;
    /** The number of server names that need a computer. Counted from the report when not given. */
    totalServers?: number;
    onResolve?: (card: string, name: string, zone?: string) => void;
    onLeaveOut?: (card: string) => void;
    onUndo?: (card: string) => void;
    onAddAll?: () => void;
    onRemoveUnsupported?: (key: string) => void;
    onPickKind?: (key: string, kind: string) => void;
    onToggle?: (group: string) => void;
    onGroupAll?: (group: string, on: boolean) => void;
    onSelect?: (key: string, on: boolean) => void;
  } = $props();

  type Card = { id: string; verdicts: CheckFileVerdict[]; kind: string; needsZone: boolean; suggested: string };

  const SHORT_KINDS: Record<string, string> = { 'iis-w3c': 'IIS', 'sql-errorlog': 'SQL error log' };
  const kindText = (importer: string): string => SHORT_KINDS[importer] ?? importerLabel(importer);
  const listed = (verdicts: CheckFileVerdict[]) => verdicts.map((verdict) => ({ key: verdict.key, name: verdict.name, size: formatBytes(verdict.size) }));

  // A file picked from a folder carries the folder in its staged name; the folder is the best guess for the computer's name.
  // The check only sends leaf names, so the folder is read from the staged file with the verdict's key.
  function folderOf(verdicts: CheckFileVerdict[], staged: Map<string, StagedFile>): string {
    for (const verdict of verdicts) {
      const parts = (staged.get(verdict.key)?.name ?? verdict.name).split(/[\\/]/);
      if (parts.length > 1 && parts[parts.length - 2] !== '') return parts[parts.length - 2];
    }
    return '';
  }

  const parts = $derived.by(() => {
    const ready = new Map<string, CheckFileVerdict[]>();
    const servers = new Map<string, CheckFileVerdict[]>();
    const unnamed = new Map<string, CheckFileVerdict[]>();
    const unsupported: CheckFileVerdict[] = [];
    const add = (map: Map<string, CheckFileVerdict[]>, id: string, verdict: CheckFileVerdict) => {
      const list = map.get(id);
      if (list) list.push(verdict);
      else map.set(id, [verdict]);
    };
    for (const verdict of report?.files ?? []) {
      if (verdict.verdict === 'ready') add(ready, groupOf(verdict), verdict);
      else if (verdict.verdict === 'needs-computer') add(servers, cardIdOf(verdict) ?? '', verdict);
      else if (verdict.verdict === 'no-computer' || verdict.verdict === 'needs-zone') add(unnamed, cardIdOf(verdict) ?? '', verdict);
      else unsupported.push(verdict);
    }
    const serverCards: Card[] = [...servers].map(([id, verdicts]) => ({
      id, verdicts, needsZone: false, suggested: id,
      kind: [...new Set(verdicts.map((verdict) => kindText(verdict.importer)))].join(', ')
    }));
    const staged = new Map(files.map((item) => [fileKeyOf(item), item]));
    const unnamedCards: Card[] = [...unnamed].map(([id, verdicts]) => {
      const importer = verdicts[0].importer;
      return { id, verdicts, kind: kindText(importer), suggested: folderOf(verdicts, staged), needsZone: verdicts.some((verdict) => verdict.needsZone) || timeZoneRule(importer) === 'local' };
    });
    return { ready: [...ready], serverCards, unnamedCards, unsupported };
  });

  const serverTotal = $derived(totalServers ?? parts.serverCards.length);
  const ticked = (key: string): boolean => selected[key] !== false;
  const tickedMap = $derived(Object.fromEntries((report?.files ?? []).map((verdict) => [verdict.key, ticked(verdict.key)])));

  const tiedReason = (tied: string[]): string => 'Could be ' + tied.map(importerLabel).join(' or ') + '. Tell us which.';
  const unsupportedRows = $derived<UnsupportedFile[]>(parts.unsupported.map((verdict) => ({
    key: verdict.key,
    name: verdict.name,
    size: formatBytes(verdict.size),
    reason: verdict.tied.length > 1 ? tiedReason(verdict.tied) : (reasonText(verdict.reason) ?? ''),
    kindPick: !(verdict.reason === 'archive-extract-first' || verdict.reason.startsWith('binary-'))
  })));

  // The group stays on screen, saying so, once its last file is removed.
  let sawUnsupported = $state(false);
  $effect(() => {
    if (checking) sawUnsupported = false;
    else if (parts.unsupported.length > 0) sawUnsupported = true;
  });

  // A card that asks for a name shows the name the person typed once it is resolved.
  let named = $state<Record<string, string>>({});
  let fixRefs = $state<Record<string, { focus: () => Promise<void> } | undefined>>({});

  const allOn = (verdicts: CheckFileVerdict[]): boolean => verdicts.every((verdict) => ticked(verdict.key));

  const headOf = (host: HTMLElement) => host.parentElement?.querySelector<HTMLElement>('.grp-head')?.focus({ preventScroll: true });
</script>

<!-- Focus targets for the shell (`data-focus`): the cards and groups draw their own buttons, so each target hands focus on to the right one. -->
{#snippet anchor(value: string, run: (host: HTMLElement) => void)}
  <span class="anchor" tabindex="-1" data-focus={value} onfocus={(event) => run(event.currentTarget)}></span>
{/snippet}

{#if checking}
  <div class="checking" role="status">
    <div class="spinner" aria-hidden="true"></div>
    <p>{POPUP_TEXT.checking(files.length)}</p>
    <p class="sub">{POPUP_TEXT.checkingSub}</p>
  </div>
{:else if report}
  {#each parts.ready as [group, verdicts] (group)}
    <div class="slot">
      {@render anchor('head-' + group, headOf)}
      <GroupedList
        groupKey={group}
        title={importerLabel(verdicts[0].importer)}
        meta={verdicts.length + (verdicts.length === 1 ? ' file, ready' : ' files, ready')}
        badge="ready"
        kind={importerLabel(verdicts[0].importer)}
        files={listed(verdicts)}
        selectable
        selected={tickedMap}
        expanded={expanded[group] === true}
        onToggle={(key) => onToggle?.(key)}
        onGroupAll={(key) => onGroupAll?.(key, !allOn(verdicts))}
        onSelect={(key, on) => onSelect?.(key, on)}
      />
    </div>
  {/each}

  {#each [...parts.serverCards, ...parts.unnamedCards] as card (card.id)}
    {@const cardState = cards[card.id] ?? 'open'}
    <div class="slot">
      {@render anchor('card-' + card.id, () => void fixRefs[card.id]?.focus())}
      {@render anchor('name:' + card.id, () => void fixRefs[card.id]?.focus())}
      {@render anchor('head-' + card.id, headOf)}
      <FixCard
        bind:this={fixRefs[card.id]}
        card={cardState}
        kind={card.kind}
        serverName={named[card.id] ?? card.suggested}
        fileCount={card.verdicts.length}
        files={listed(card.verdicts)}
        selected={tickedMap}
        needsZone={card.needsZone}
        askName={parts.unnamedCards.includes(card)}
        suggestedZone={zone}
        expanded={expanded[card.id] === true}
        totalServers={serverTotal}
        onResolve={(name, picked) => { named[card.id] = name; onResolve?.(card.id, name, picked); }}
        onLeaveOut={() => onLeaveOut?.(card.id)}
        onUndo={() => onUndo?.(card.id)}
        onAddAll={() => onAddAll?.()}
        onToggle={() => onToggle?.(card.id)}
        onGroupAll={() => onGroupAll?.(card.id, !allOn(card.verdicts))}
        onSelect={(key, on) => onSelect?.(key, on)}
      />
    </div>
  {/each}

  {#if parts.unsupported.length > 0 || sawUnsupported}
    <NotSupported files={unsupportedRows} pickedKind={kinds} onRemove={(key) => onRemoveUnsupported?.(key)} onPickKind={(key, kind) => onPickKind?.(key, kind)} />
  {/if}
{/if}

<style>
  .slot { position: relative; }
  .anchor { position: absolute; top: 0; left: 0; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); outline: none; }

  .checking { display: grid; justify-items: center; gap: 12px; padding: 40px 16px; text-align: center; }
  .spinner { width: 28px; height: 28px; border: 3px solid var(--line); border-top-color: var(--accent); border-radius: 50%; animation: spin 1s linear infinite; }
  .checking p { margin: 0; color: var(--text-2); font-size: 13px; }
  .checking p.sub { font-size: 12px; }
  @keyframes spin { to { transform: rotate(360deg); } }
  @media (prefers-reduced-motion: reduce) {
    .spinner { animation-duration: 4s; }
  }
</style>
