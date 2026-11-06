<script lang="ts">
  import { filesWord } from '../../copy/importPopupCopy';

  // One group line of the import popup: a head that expands on demand, and under it an "All N files" box and one row per file.
  // A "needs checking" group always shows its files and its head is not a toggle, so a problem file cannot be hidden.
  let {
    groupKey,
    title,
    meta,
    badge,
    kind,
    files,
    selectable = false,
    selected = {},
    expanded = false,
    onToggle,
    onGroupAll,
    onSelect
  }: {
    groupKey: string;
    title: string;
    meta: string;
    badge: 'ready' | 'found' | 'needs checking';
    /** Shown on every file row, for example "IIS web log". */
    kind?: string;
    files: { key: string; name: string; size: string }[];
    /** Shows the checkboxes. Without it the rows are read-only. */
    selectable?: boolean;
    /** Selected state by file key. */
    selected?: Record<string, boolean>;
    expanded?: boolean;
    onToggle?: (key: string) => void;
    /** Called when the "All N files" box is pressed. The caller decides whether that selects or clears every file. */
    onGroupAll?: (key: string) => void;
    onSelect?: (key: string, on: boolean) => void;
  } = $props();

  const problem = $derived(badge === 'needs checking');
  const open = $derived(problem || expanded);
  const badgeClass = $derived(badge === 'ready' ? 'badge-ok' : 'badge-muted');
  const selectedCount = $derived(files.filter((f) => selected[f.key]).length);
  const allOn = $derived(selectedCount === files.length);
  const someOn = $derived(selectedCount > 0 && !allOn);

  let allBox: HTMLInputElement | null = $state(null);
  $effect(() => {
    if (allBox) allBox.indeterminate = someOn;
  });
</script>

{#snippet headLabel()}
  {#if badge === 'ready'}
    <span class="grp-ic" aria-hidden="true">
      <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
        <circle cx="8" cy="8" r="6.5" stroke="currentColor" stroke-width="1.3" />
        <path d="M5.2 8.1l1.9 1.9 3.7-3.9" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" />
      </svg>
    </span>
  {/if}
  <span class="grp-name">{title}</span>
  <span class="grp-meta">{meta}</span>
  <span class="grp-badge {badgeClass}">{badge}</span>
{/snippet}

<div class="grp" class:open data-grp={groupKey}>
  {#if problem}
    <div class="grp-head grp-static">{@render headLabel()}</div>
  {:else}
    <button type="button" class="grp-head" aria-expanded={open} onclick={() => onToggle?.(groupKey)}>
      <span class="chev" aria-hidden="true"></span>
      {@render headLabel()}
    </button>
  {/if}
  {#if open}
    <div class="grp-files">
      {#if selectable}
        <label class="grp-all">
          <input bind:this={allBox} type="checkbox" checked={allOn} onchange={() => onGroupAll?.(groupKey)} />
          <span>All {files.length}{filesWord(files.length)}</span>
        </label>
      {/if}
      {#each files as f (f.key)}
        <div class="file-row">
          {#if selectable}
            <span class="file-sel">
              <input
                type="checkbox"
                aria-label={f.name}
                checked={!!selected[f.key]}
                onchange={(e) => onSelect?.(f.key, e.currentTarget.checked)}
              />
            </span>
          {:else}
            <span class="file-sel-gap" aria-hidden="true"></span>
          {/if}
          <span class="file-name">{f.name}</span>
          <span class="file-size">{f.size}</span>
          {#if kind}<span class="file-tag">{kind}</span>{/if}
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .grp { border: 1px solid var(--line); border-radius: var(--radius); margin-bottom: 10px; background: var(--surface); }
  .grp-head { display: flex; align-items: center; gap: 10px; width: 100%; padding: 10px 12px; background: none; border: 0; text-align: left; color: var(--text); font-size: 13px; }
  .grp-head:hover { background: var(--surface-2); }
  .grp-static:hover { background: none; }
  .chev { flex: 0 0 auto; width: 8px; height: 8px; border-right: 1.5px solid var(--text-2); border-bottom: 1.5px solid var(--text-2); transform: rotate(-45deg) translateY(-1px); transition: transform var(--dur) var(--ease); }
  .grp.open .chev { transform: rotate(45deg) translateY(-1px); }
  .grp-ic { flex: 0 0 auto; display: grid; place-items: center; width: 18px; height: 18px; color: var(--tone-ok); }
  .grp-name { font-weight: 600; }
  .grp-meta { color: var(--text-2); font-size: 12px; font-family: var(--mono); }
  .grp-badge { margin-left: auto; padding: 2px 8px; border-radius: var(--radius); font-size: 12px; font-weight: 600; }
  .badge-ok { color: var(--tone-ok); background: color-mix(in srgb, var(--tone-ok) 14%, var(--surface)); border: 1px solid color-mix(in srgb, var(--tone-ok) 30%, transparent); }
  .badge-muted { color: var(--text-2); background: var(--surface-2); border: 1px solid var(--line); }
  .grp-files { border-top: 1px solid var(--line-soft); padding: 6px 8px 8px; }
  .grp-all { display: flex; align-items: center; gap: 8px; padding: 5px 4px; color: var(--text-2); font-size: 12px; border-bottom: 1px solid var(--line-soft); margin-bottom: 2px; }
  .grp-all input, .file-sel input { accent-color: var(--accent); width: 15px; height: 15px; margin: 0; }
  .file-row { display: flex; align-items: center; gap: 10px; padding: 5px 4px; border-radius: var(--radius); }
  .file-row:hover { background: var(--surface-2); }
  .file-sel, .file-sel-gap { display: flex; align-items: center; flex: 0 0 auto; }
  .file-sel-gap { width: 15px; }
  .file-name { font-family: var(--mono); font-size: 12px; overflow-wrap: anywhere; min-width: 0; }
  .file-size { flex: 0 0 auto; margin-left: auto; color: var(--text-2); font-family: var(--mono); font-size: 12px; white-space: nowrap; }
  .file-tag { flex: 0 0 auto; color: var(--text-2); font-size: 12px; }
  @media (pointer: coarse) { .file-row { min-height: 40px; } }
</style>
