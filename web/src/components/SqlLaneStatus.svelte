<script lang="ts">
  // One quiet line above the chart saying what the Timeline is doing about the database for this window: reading it, why it could not, or
  // that it is only a suggestion. It never connects anything; the page decides, and the button only asks the page to read.
  import { SQL_AUTO_TEXT } from '../lib/copy/sqlAutoCopy';
  import type { SqlStatusKind } from '../lib/tracing/sqlAuto';

  let { kind, source, reason = '', enabled, onToggle, onRead, onOpenSources }: {
    kind: SqlStatusKind; source: string; reason?: string; enabled: boolean;
    onToggle: (on: boolean) => void; onRead: () => void; onOpenSources: () => void;
  } = $props();
</script>

<div class="sqlstat" data-testid="sql-auto" data-kind={kind} role="status">
  <div class="sqltext">
    {#if kind === 'reading'}
      <p><b>{SQL_AUTO_TEXT.reading(source)}</b> <span class="dim">{SQL_AUTO_TEXT.readingNote}</span></p>
    {:else if kind === 'failed'}
      <p>{SQL_AUTO_TEXT.failed(reason)}</p>
    {:else if kind === 'overlap'}
      <p>{SQL_AUTO_TEXT.overlap}</p>
    {:else if kind === 'password'}
      <p>{SQL_AUTO_TEXT.password}</p>
    {:else if kind === 'suggest'}
      <p>{SQL_AUTO_TEXT.suggest(source)}</p>
    {:else}
      <p class="dim">{SQL_AUTO_TEXT.kept(source)}</p>
    {/if}
  </div>
  <div class="sqlact">
    {#if kind === 'failed'}<button type="button" class="secondary-button" data-testid="sql-auto-retry" onclick={onRead}>{SQL_AUTO_TEXT.retry}</button>{/if}
    {#if kind === 'suggest'}<button type="button" class="primary-button" data-testid="sql-auto-read" onclick={onRead}>{SQL_AUTO_TEXT.suggestAction}</button>{/if}
    {#if kind === 'password'}<button type="button" class="secondary-button" onclick={onOpenSources}>{SQL_AUTO_TEXT.openSources}</button>{/if}
    <label class="sqltoggle" title={SQL_AUTO_TEXT.toggleHelp}>
      <input type="checkbox" data-testid="sql-auto-toggle" checked={enabled} disabled={kind === 'reading'} onchange={(event) => onToggle(event.currentTarget.checked)} />
      <span>{SQL_AUTO_TEXT.toggle}</span>
    </label>
  </div>
</div>

<style>
  .sqlstat { display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: var(--space-2) var(--space-4); min-width: 0; padding: var(--space-2) var(--space-3); background: var(--surface); border: var(--hairline-width) solid var(--line); border-left: 3px solid var(--pending); border-radius: var(--radius-sm); font-size: var(--fs-2); }
  .sqlstat[data-kind='failed'], .sqlstat[data-kind='overlap'] { border-left-color: var(--warn); }
  .sqltext { min-width: 0; flex: 1 1 320px; }
  .sqltext p { margin: 0; overflow-wrap: anywhere; }
  .dim { color: var(--text-2); font-weight: 400; }
  .sqlact { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2) var(--space-3); }
  .sqltoggle { display: inline-flex; align-items: center; gap: var(--space-2); color: var(--text-2); cursor: pointer; }
  .sqltoggle input:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
</style>
