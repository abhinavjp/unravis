<script lang="ts">
  import { EXTRA_KIND_IDS, FILE_KIND_IDS } from '../../fileKinds';
  import type { StagedFile } from '../../fileKinds';
  import { fileKindLabel, FILES_TEXT } from '../../copy/fileKindsCopy';
  import { formatBytes } from '../../copy/importCopy';
  import { POPUP_TEXT } from '../../copy/importPopupCopy';
  import GroupedList from './GroupedList.svelte';
  import { fileKeyOf } from './popupState.svelte';

  // Step 1 of the "Import files" popup (wireframe: step1()). The person drops files or a folder, or picks them, and the list shows what each
  // name suggests, one group per kind. Nothing is sent or read here: the picked or dropped files go up through `onAddFiles`, `onAddFolder` and
  // `onDrop`, and the caller stages them in the popup state. The footer (the read-only note and Continue) is drawn by the shell.
  let { files, expanded, onAddFiles, onAddFolder, onDrop, onToggle }: {
    files: StagedFile[];
    expanded: Record<string, boolean>;
    onAddFiles: (files: File[]) => void;
    onAddFolder: (files: File[]) => void;
    onDrop: (event: DragEvent) => void;
    onToggle: (key: string) => void;
    /** Kept for the shell's footer, which draws Continue. This step does not call it. */
    onContinue: () => void;
  } = $props();
  const uid = $props.id();

  // Names as the wireframe shows them. A kind without an entry is named by its catalog label.
  const WORDING: Partial<Record<string, { title: string; tag: string }>> = {
    'iis-w3c': { title: 'IIS web logs', tag: 'IIS web log' },
    'app-log': { title: 'log4net', tag: 'log4net' },
    'sql-errorlog': { title: 'SQL error log', tag: 'SQL error log' }
  };
  const NOT_RECOGNISED = 'Not recognised yet';
  const UNRECOGNISED_KEY = 'unsupported';
  const KIND_ORDER: readonly string[] = [...FILE_KIND_IDS, ...EXTRA_KIND_IDS];

  const countText = (n: number): string => (n === 1 ? '1 file' : n + ' files');

  /** The folder a file was picked from (the first part of its path), or ''. */
  const folderOf = (item: StagedFile): string => {
    const parts = item.name.split(/[\\/]/);
    return parts.length > 1 ? parts[0] : '';
  };

  /** The folder all the files of a group came from, or '' when they do not share one. */
  function sharedFolder(rows: StagedFile[]): string {
    const first = folderOf(rows[0]);
    return first !== '' && rows.every((row) => folderOf(row) === first) ? first : '';
  }

  interface Group {
    key: string;
    title: string;
    tag?: string;
    badge: 'found' | 'needs checking';
    meta: string;
    rows: { key: string; name: string; size: string }[];
  }

  const groups = $derived.by((): Group[] => {
    const out: Group[] = [];
    for (const kind of KIND_ORDER) {
      const members = files.filter((item) => item.choice === kind);
      if (members.length === 0) continue;
      const folder = sharedFolder(members);
      const wording = WORDING[kind] ?? { title: fileKindLabel(kind), tag: fileKindLabel(kind) };
      out.push({
        key: 'kind:' + kind,
        title: wording.title,
        tag: wording.tag,
        badge: 'found',
        meta: countText(members.length) + (folder !== '' ? ' · folder ' + folder : ''),
        rows: members.map((item) => ({ key: fileKeyOf(item), name: item.name, size: formatBytes(item.size) }))
      });
    }
    const unknown = files.filter((item) => item.choice === '');
    if (unknown.length > 0) {
      out.push({
        key: UNRECOGNISED_KEY,
        title: NOT_RECOGNISED,
        badge: 'needs checking',
        meta: countText(unknown.length),
        rows: unknown.map((item) => ({ key: fileKeyOf(item), name: item.name, size: formatBytes(item.size) }))
      });
    }
    return out;
  });

  let filesInput = $state<HTMLInputElement>();
  let folderInput = $state<HTMLInputElement>();

  // The picker's list is copied out and the input cleared, so the same file can be picked again later.
  function pick(input: HTMLInputElement, folder: boolean) {
    const picked = Array.from(input.files ?? []);
    input.value = '';
    if (picked.length === 0) return;
    if (folder) onAddFolder(picked);
    else onAddFiles(picked);
  }

  function onDragOver(event: DragEvent) {
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = 'copy';
  }

  function dropped(event: DragEvent) {
    event.preventDefault();
    onDrop(event);
  }
</script>

<div class="drop" role="group" aria-labelledby="{uid}-drop" ondragenter={onDragOver} ondragover={onDragOver} ondrop={dropped}>
  <p class="drop-title" id="{uid}-drop">{POPUP_TEXT.dropTitle}</p>
  <p class="drop-sub">{POPUP_TEXT.dropSub}</p>
  <div class="drop-actions">
    <button type="button" class="btn btn-secondary" onclick={() => filesInput?.click()}>{POPUP_TEXT.addFiles}</button>
    <button type="button" class="btn btn-secondary" onclick={() => folderInput?.click()}>{POPUP_TEXT.addFolder}</button>
  </div>
</div>
<input bind:this={filesInput} hidden type="file" multiple tabindex="-1" aria-label={FILES_TEXT.filesInputLabel} onchange={(event) => pick(event.currentTarget, false)} />
<input bind:this={folderInput} hidden type="file" webkitdirectory tabindex="-1" aria-label={FILES_TEXT.folderInputLabel} onchange={(event) => pick(event.currentTarget, true)} />

<div class="groups">
  {#each groups as group (group.key)}
    <GroupedList
      groupKey={group.key}
      title={group.title}
      meta={group.meta}
      badge={group.badge}
      kind={group.tag}
      files={group.rows}
      expanded={!!expanded[group.key]}
      {onToggle}
    />
  {/each}
</div>

<style>
  .drop { display: grid; justify-items: center; gap: 10px; padding: 28px 16px; text-align: center; background: var(--bg); border: 2px dashed var(--control-line); border-radius: var(--radius); }
  .drop-title { margin: 0; font-size: 15px; font-weight: 600; }
  .drop-sub { margin: 0; color: var(--text-2); font-size: 13px; }
  .drop-actions { display: flex; flex-wrap: wrap; justify-content: center; gap: 8px; }

  .btn { display: inline-flex; align-items: center; justify-content: center; gap: 8px; min-height: 36px; padding: 8px 14px; border-radius: var(--radius); font-size: 13px; font-weight: 600; cursor: pointer; transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease), color var(--dur) var(--ease); }
  .btn-secondary { color: var(--text); background: transparent; border: 1px solid var(--line); }
  .btn-secondary:hover { border-color: var(--text-2); background: var(--surface-2); }

  .groups { margin-top: 14px; }

  @media (pointer: coarse), (max-width: 720px) {
    .btn { min-height: 44px; }
  }
</style>
