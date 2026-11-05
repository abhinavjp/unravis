<script module lang="ts">
  export type UnsupportedFile = {
    key: string;
    name: string;
    size: string;
    reason: string;
    /** False for a file the kind picker does not cover: its row shows only "Remove from list". Defaults to true. */
    kindPick?: boolean;
  };
</script>

<script lang="ts">
  import { POPUP_TEXT } from '../../copy/importPopupCopy';

  // The "not supported" group of the Import files popup (wireframe, "Not supported group"). A file here is one Unravis cannot read.
  // Each row shows the file name, its size and why it was refused. The person can say what the file is (the kind picker) and remove it.
  // The list belongs to the parent: `onRemove` asks it to drop the file, and the row goes once `files` no longer holds it.
  let { files, onRemove, onPickKind, pickedKind = {} }: {
    files: UnsupportedFile[];
    onRemove?: (key: string) => void;
    onPickKind?: (key: string, kind: string) => void;
    /** The kind the person picked for each file, keyed by `key`. */
    pickedKind?: Record<string, string>;
  } = $props();

  // Shown once every file has been removed. It is not in POPUP_TEXT yet, so it lives here.
  const ALL_REMOVED = 'All unsupported files removed.';
</script>

<div class="notsupp">
  {#if files.length > 0}
    <div class="notsupp-head">
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
        <circle cx="8" cy="8" r="6.5" stroke="currentColor" stroke-width="1.3" />
        <path d="M8 5v3.2" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
        <circle cx="8" cy="11.3" r=".8" fill="currentColor" />
      </svg>
      <span>{POPUP_TEXT.notSupportedHead(files.length)}</span>
    </div>
    {#each files as file (file.key)}
      <div class="notsupp-row">
        <span class="file-name">{file.name}</span>
        <span class="file-size">{file.size}</span>
        <span class="reason">{file.reason || POPUP_TEXT.notSupportedReason}</span>
        <div class="row-actions">
          {#if file.kindPick !== false}
            <select
              class="kind-pick"
              aria-label={POPUP_TEXT.kindPickerPlaceholder}
              value={pickedKind[file.key] ?? ''}
              onchange={(event) => onPickKind?.(file.key, event.currentTarget.value)}
            >
              <option value="">{POPUP_TEXT.kindPickerPlaceholder}</option>
              {#each POPUP_TEXT.kinds as kind (kind)}
                <option value={kind}>{kind}</option>
              {/each}
            </select>
            <button type="button" class="btn-quiet" onclick={() => onRemove?.(file.key)}>{POPUP_TEXT.remove}</button>
          {:else}
            <button type="button" class="btn-quiet" onclick={() => onRemove?.(file.key)}>{POPUP_TEXT.removeFromList}</button>
          {/if}
        </div>
      </div>
    {/each}
  {:else}
    <div class="notsupp-row"><span class="empty-hint">{ALL_REMOVED}</span></div>
  {/if}
</div>

<style>
  .notsupp {
    border: 1px solid var(--line);
    border-left: 3px solid var(--bad);
    border-radius: var(--radius);
    margin-bottom: 10px;
    background: color-mix(in srgb, var(--bad) 5%, var(--surface));
  }
  .notsupp-head {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px 14px 8px;
    font-size: 13px;
    font-weight: 600;
    color: var(--bad);
  }
  .notsupp-row {
    display: flex;
    align-items: flex-start;
    gap: 10px;
    flex-wrap: wrap;
    padding: 8px 14px;
    border-top: 1px solid var(--line-soft);
  }
  .file-name {
    flex: 0 1 auto;
    min-width: 0;
    font-family: var(--mono);
    font-size: 12px;
    overflow-wrap: anywhere;
  }
  .file-size {
    flex: 0 0 auto;
    margin-left: auto;
    color: var(--text-2);
    font-family: var(--mono);
    font-size: 12px;
    white-space: nowrap;
  }
  .reason {
    flex: 1 1 200px;
    min-width: 0;
    color: var(--text-2);
    font-size: 12px;
  }
  .row-actions {
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
  }
  .kind-pick {
    min-width: 180px;
  }
  .btn-quiet {
    min-height: 36px;
    padding: 6px 10px;
    border: 1px solid transparent;
    border-radius: var(--radius);
    background: transparent;
    color: var(--text-2);
    font: inherit;
    font-size: 12px;
    font-weight: 500;
    cursor: pointer;
  }
  .btn-quiet:hover {
    color: var(--text);
    background: var(--surface-2);
  }
  .btn-quiet:focus-visible {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }
  .empty-hint {
    padding: 5px 4px;
    color: var(--text-2);
    font-size: 12px;
  }
</style>
