<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api, ApiError } from '../../lib/api';
  import { keeperView, onKeeperPreview, type KeeperStatus } from '../../lib/keeper';
  import { KEEPER_TEXT, keeperDuplicatesText, keeperPendingText } from '../../lib/copy/sourcesCopy';

  // The key keeper's state on the connections page (P14): not running, not trusted, old saved files to move, or old copies to remove.
  // Where the shell promotes the page (primary), the move card holds its one main button and the page outlines its own add button; on case
  // home, which has its own main button, it stays outlined. Removing the old copies asks once. Nothing here ever holds a password. The server only reports counts.
  // Each swap of the card's content puts the keyboard focus on the new heading (or on "Keep them" when the remove question opens), so
  // nobody is sent back to the top of the page.
  let { status, onStatus, onRetry, onLater, primary = false }: {
    primary?: boolean;
    status: KeeperStatus | null;
    onStatus: (next: KeeperStatus) => void;
    onRetry: () => Promise<void> | void;
    onLater: () => void;
  } = $props();

  const view = $derived(keeperView(status));
  const duplicates = $derived(status?.pending.duplicates ?? 0);
  let busy = $state('');
  let asking = $state(false);
  let problem = $state('');
  let done = $state('');
  // Set when the server kept the old copies because a record is not in the keeper yet: the moved card then offers the move again.
  let refused = $state(false);
  let box = $state<HTMLElement | null>(null);

  async function focusCard() {
    await tick();
    if (!box) return;
    const target = box.querySelector<HTMLElement>(asking ? '[data-keep]' : '[data-keeper-head]') ?? box.querySelector<HTMLElement>('[data-keeper-done]');
    target?.focus();
  }

  async function ask(open: boolean) {
    asking = open;
    if (open) problem = '';
    await focusCard();
  }

  async function run(kind: 'retry' | 'move' | 'remove') {
    if (busy) return;
    busy = kind; problem = ''; done = '';
    if (kind !== 'remove') refused = false;
    try {
      if (kind === 'retry') await onRetry();
      else if (kind === 'move') onStatus(await api.keeperMigrate());
      else { onStatus(await api.keeperRemoveOld(duplicates > 0)); done = KEEPER_TEXT.removed; }
    } catch (error) {
      problem = error instanceof Error && error.message ? error.message : KEEPER_TEXT.failed;
      const code = error instanceof ApiError ? error.code : undefined;
      if (code === 'not-verified' || code === 'duplicates-unconfirmed') {
        // The old copies were kept: fetch the counts again so the card shows what still waits (and the move, when anything does).
        refused = code === 'not-verified';
        try { await onRetry(); } catch { /* the message above still says what happened */ }
      }
    } finally { busy = ''; asking = false; }
    await focusCard();
  }

  function escape(event: KeyboardEvent) {
    if (event.key === 'Escape' && busy === '') { event.preventDefault(); void ask(false); }
  }

  // D-A-35: a screenshot preview named in the address after this page opened (#keeper=migrate and so on) re-reads the page's own
  // list and the keeper's counts, through the same reader the Try again button uses, so the rows and this card change together.
  // A read that is already running (busy) makes the page's own refresh, so this one waits for the next change.
  async function refreshFromPreview() {
    if (busy !== '') return;
    try { await onRetry(); } catch { /* the page keeps what it showed */ }
  }
  onMount(() => onKeeperPreview(() => void refreshFromPreview()));
</script>

<div class="keeper-box" bind:this={box}>
{#if view === 'stopped' || view === 'impostor'}
  <div class="panel keeper-card down" role="alert" data-keeper={view}>
    <h4 tabindex="-1" data-keeper-head>{view === 'stopped' ? KEEPER_TEXT.stoppedHeading : KEEPER_TEXT.impostorHeading}</h4>
    <p class="lead">{view === 'stopped' ? KEEPER_TEXT.stoppedBody : KEEPER_TEXT.impostorBody}</p>
    <div class="actions"><button type="button" class="btn" disabled={busy !== ''} onclick={() => void run('retry')}>{busy === 'retry' ? KEEPER_TEXT.checking : KEEPER_TEXT.retry}</button></div>
  </div>
{:else if view === 'migrate' && status}
  <div class="panel keeper-card" data-keeper="migrate" aria-labelledby="keeper-move-head">
    <h4 id="keeper-move-head" tabindex="-1" data-keeper-head>{KEEPER_TEXT.migrateHeading}</h4>
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
    <h4 id="keeper-moved-head" tabindex="-1" data-keeper-head>{KEEPER_TEXT.movedHeading}</h4>
    <p class="lead">{KEEPER_TEXT.movedBody}</p>
    {#if duplicates > 0}<p class="hint" data-keeper-duplicates>{keeperDuplicatesText(duplicates)}</p>{/if}
    {#if asking}
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
      <div role="alertdialog" aria-label={KEEPER_TEXT.removeAsk} tabindex="-1" onkeydown={escape}>
        <p class="hint">{KEEPER_TEXT.removeAsk}</p>
        <div class="actions">
          <button type="button" class="btn" disabled={busy !== ''} onclick={() => void run('remove')}>{KEEPER_TEXT.removeYes}</button>
          <button type="button" class="btn" data-keep disabled={busy !== ''} onclick={() => void ask(false)}>{KEEPER_TEXT.removeNo}</button>
        </div>
      </div>
    {:else}
      <div class="actions">
        <button type="button" class="btn" onclick={() => void ask(true)}>{KEEPER_TEXT.removeOld}</button>
        {#if refused}<button type="button" class="btn" disabled={busy !== ''} onclick={() => void run('move')}>{busy === 'move' ? KEEPER_TEXT.moving : KEEPER_TEXT.moveAgain}</button>{/if}
      </div>
    {/if}
    {#if problem !== ''}<p class="say bad" role="alert">{problem}</p>{/if}
  </div>
{/if}
{#if done !== '' && view === 'ready'}<p class="say good" role="status" tabindex="-1" data-keeper-done>{done}</p>{/if}
</div>

<style>
  /* The box only gives the focus code a root: its children sit in the page's grid as before, and an empty box takes no room. */
  .keeper-box { display: contents; }
  [data-keeper-head]:focus:not(:focus-visible), [data-keeper-done]:focus:not(:focus-visible) { outline: none; }
</style>
