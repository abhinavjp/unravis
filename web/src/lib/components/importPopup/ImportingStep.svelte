<script lang="ts">
  import { POPUP_TEXT } from '../../copy/importPopupCopy';

  // The Importing step of the "Import files" popup (wireframe: "Step 3 importing"). It shows the whole batch as one bar with its count,
  // then one line per group (a kind of log) with its own bar and "done of count". The numbers come from the parent, which updates them as
  // each file ends. Cancel does not stop the file being sent: `onCancel` asks the parent to stop the queue after it.
  let { progress, total, groups, onCancel }: {
    /** Files finished so far. */
    progress: number;
    /** Files in this import. */
    total: number;
    groups: { name: string; done: number; count: number }[];
    /** Asks the parent to stop after the current file. */
    onCancel: () => void;
  } = $props();

  /** A whole percentage from 0 to 100. Nothing to count gives 0. */
  const percentOf = (done: number, count: number): number => (count > 0 ? Math.min(100, Math.max(0, Math.round((done / count) * 100))) : 0);

  const pct = $derived(percentOf(progress, total));
  const line = $derived(POPUP_TEXT.impLine(progress, total));
</script>

<div class="imp">
  <div class="imp-bar-wrap">
    <div class="imp-line">
      <span class="big">{line}</span>
      <span class="sub">{pct}%</span>
    </div>
    <div class="imp-track" role="progressbar" aria-label={line} aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct}>
      <div class="imp-fill" style:width="{pct}%"></div>
    </div>
  </div>

  <div class="imp-groups">
    {#each groups as group (group.name)}
      {@const groupPct = percentOf(group.done, group.count)}
      <div class="imp-grp">
        <span class="gname">{group.name}</span>
        <div class="imp-mini"><span style:width="{groupPct}%"></span></div>
        <span class="gcount" class:done={group.count > 0 && group.done >= group.count}>{group.done} of {group.count}</span>
      </div>
    {/each}
  </div>

  <div class="cancel-row">
    <button type="button" class="btn btn-secondary" onclick={onCancel}>{POPUP_TEXT.cancel}</button>
  </div>
</div>

<style>
  .imp-bar-wrap { margin-bottom: 14px; }
  .imp-line { display: flex; justify-content: space-between; align-items: baseline; margin-bottom: 6px; }
  .imp-line .big { font-family: var(--mono); font-size: 13px; font-weight: 600; }
  .imp-line .sub { color: var(--text-2); font-size: 12px; font-family: var(--mono); }
  .imp-track { height: 6px; background: var(--line); border-radius: var(--radius); overflow: hidden; }
  .imp-fill { height: 100%; background: var(--accent); transition: width 300ms var(--ease); }

  .imp-grp { display: flex; align-items: center; gap: 10px; padding: 7px 0; border-bottom: 1px solid var(--line-soft); font-size: 13px; }
  .imp-grp:last-child { border-bottom: 0; }
  .gname { font-weight: 600; }
  .gcount { margin-left: auto; font-family: var(--mono); color: var(--text-2); font-size: 12px; }
  .gcount.done { color: var(--tone-ok); }
  .imp-mini { flex: 0 0 auto; width: 80px; height: 4px; background: var(--line); border-radius: var(--radius); overflow: hidden; }
  .imp-mini span { display: block; height: 100%; background: var(--accent); }

  .cancel-row { display: flex; justify-content: flex-end; margin-top: 14px; }
  .btn { display: inline-flex; align-items: center; justify-content: center; min-height: 36px; padding: 8px 14px; border-radius: var(--radius); font-size: 13px; font-weight: 600; cursor: pointer; transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease), color var(--dur) var(--ease); }
  .btn-secondary { color: var(--text); background: transparent; border: 1px solid var(--line); }
  .btn-secondary:hover { border-color: var(--text-2); background: var(--surface-2); }
  .btn:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  @media (pointer: coarse) {
    .btn { min-height: 44px; }
  }
  @media (prefers-reduced-motion: reduce) {
    .imp-fill { transition-duration: .01ms; }
  }
</style>
