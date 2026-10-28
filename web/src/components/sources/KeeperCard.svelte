<script lang="ts">
  import { api } from '../../lib/api';
  import { keeperView, type KeeperStatus } from '../../lib/keeper';
  import { KEEPER_TEXT, keeperPendingText } from '../../lib/copy/sourcesCopy';

  // The key keeper's state on the connections page (P14): not running, not trusted, old saved files to move, or old copies to remove.
  // Where the shell promotes the page (primary), the move card holds its one main button and the page outlines its own add button; on case
  // home, which has its own main button, it stays outlined. Removing the old copies asks once. Nothing here ever holds a password. The server only reports counts.
  let { status, onStatus, onRetry, onLater, primary = false }: {
    primary?: boolean;
    status: KeeperStatus | null;
    onStatus: (next: KeeperStatus) => void;
    onRetry: () => Promise<void> | void;
    onLater: () => void;
  } = $props();

  const view = $derived(keeperView(status));
  let busy = $state('');
  let asking = $state(false);
  let problem = $state('');
  let done = $state('');

  async function run(kind: 'retry' | 'move' | 'remove') {
    if (busy) return;
    busy = kind; problem = ''; done = '';
    try {
      if (kind === 'retry') await onRetry();
      else if (kind === 'move') onStatus(await api.keeperMigrate());
      else { onStatus(await api.keeperRemoveOld()); done = KEEPER_TEXT.removed; }
    } catch (error) {
      problem = error instanceof Error && error.message ? error.message : KEEPER_TEXT.failed;
    } finally { busy = ''; asking = false; }
  }
</script>

{#if view === 'stopped' || view === 'impostor'}
  <div class="panel keeper-card down" role="alert" data-keeper={view}>
    <h4>{view === 'stopped' ? KEEPER_TEXT.stoppedHeading : KEEPER_TEXT.impostorHeading}</h4>
    <p class="lead">{view === 'stopped' ? KEEPER_TEXT.stoppedBody : KEEPER_TEXT.impostorBody}</p>
    <div class="actions"><button type="button" class="btn" disabled={busy !== ''} onclick={() => void run('retry')}>{busy === 'retry' ? KEEPER_TEXT.checking : KEEPER_TEXT.retry}</button></div>
  </div>
{:else if view === 'migrate' && status}
  <div class="panel keeper-card" data-keeper="migrate" aria-labelledby="keeper-move-head">
    <h4 id="keeper-move-head">{KEEPER_TEXT.migrateHeading}</h4>
    <p class="lead">{KEEPER_TEXT.migrateBody}</p>
    <p class="hint" data-pending>{keeperPendingText(status.pending.sql, status.pending.mongo, status.pending.blackbox)}</p>
    <div class="actions">
      <button type="button" class={primary ? 'btn-primary' : 'btn'} disabled={busy !== ''} onclick={() => void run('move')}>{busy === 'move' ? KEEPER_TEXT.moving : KEEPER_TEXT.move}</button>
      <button type="button" class="btn" disabled={busy !== ''} onclick={onLater}>{KEEPER_TEXT.notNow}</button>
    </div>
    {#if problem !== ''}<p class="say bad" role="alert">{problem}</p>{/if}
  </div>
{:else if view === 'moved'}
  <div class="panel keeper-card" data-keeper="moved" aria-labelledby="keeper-moved-head">
    <h4 id="keeper-moved-head">{KEEPER_TEXT.movedHeading}</h4>
    <p class="lead">{KEEPER_TEXT.movedBody}</p>
    {#if asking}
      <div role="alertdialog" aria-label={KEEPER_TEXT.removeAsk}>
        <p class="hint">{KEEPER_TEXT.removeAsk}</p>
        <div class="actions">
          <button type="button" class="btn" disabled={busy !== ''} onclick={() => void run('remove')}>{KEEPER_TEXT.removeYes}</button>
          <button type="button" class="btn" disabled={busy !== ''} onclick={() => { asking = false; }}>{KEEPER_TEXT.removeNo}</button>
        </div>
      </div>
    {:else}
      <div class="actions"><button type="button" class="btn" onclick={() => { asking = true; problem = ''; }}>{KEEPER_TEXT.removeOld}</button></div>
    {/if}
    {#if problem !== ''}<p class="say bad" role="alert">{problem}</p>{/if}
  </div>
{/if}
{#if done !== '' && view === 'ready'}<p class="say good" role="status" data-keeper-done>{done}</p>{/if}
