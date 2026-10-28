<script lang="ts">
  import type { ArchiveDrawerKind } from '../archiveModel';
  import type { CaseInfo } from '../cases';
  import ArchiveDrawer from './ArchiveDrawer.svelte';
  import FreeDrawer from './FreeDrawer.svelte';
  import RestoreDrawer from './RestoreDrawer.svelte';


  let { drawer, info, onClose, onDone, onOpenNew, onChanged, onGone }: {
    drawer: ArchiveDrawerKind | null;
    info: CaseInfo | null;
    onClose: () => void;
    /** A move finished (archive, restore, free): the page reads again. `made` is the new case after Restore as new. */
    onDone: (kind: ArchiveDrawerKind, made: CaseInfo | null) => void;
    onOpenNew: (id: string) => void;
    onChanged: () => void;
    onGone: () => void;
  } = $props();
</script>

{#if info && drawer === 'archive'}
  {#key info.id}<ArchiveDrawer {info} {onClose} {onChanged} {onGone} onDone={() => onDone('archive', null)} />{/key}
{:else if info && (drawer === 'restore-in-place' || drawer === 'restore-as-new')}
  {#key info.id + drawer}<RestoreDrawer mode={drawer === 'restore-in-place' ? 'in-place' : 'as-new'} {info} file={info.archive?.current ?? null} {onClose} {onOpenNew} {onChanged} {onGone} onRestored={(made) => onDone(drawer as ArchiveDrawerKind, made)} />{/key}
{:else if info && drawer === 'free'}
  {#key info.id}<FreeDrawer {info} file={info.archive?.current ?? null} {onClose} {onChanged} {onGone} onFreed={() => onDone('free', null)} />{/key}
{/if}
