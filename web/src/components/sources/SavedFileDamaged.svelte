<script lang="ts">
  import { tick } from 'svelte';
  import { api } from '../../lib/api';
  import { SAVED_FILE_TEXT, askText } from '../../lib/copy/savedFileCopy';
  import { setAsideFailure } from '../../lib/savedFile';

  // RV-DAT-1 (W-13): shown only when the server says the saved MongoDB connections file cannot be read. It replaces "Try again", which cannot
  // help, with one button. One press asks first (the whole list and its saved passwords go out of use); only the answer Yes calls the server.
  // The server renames the file and keeps it (never deletes it), and refuses unless the file is really unreadable.
  // detail: the server's own sentence (it names the real folder). shared: the list is the whole PC's, so the question says everyone is affected.
  // onDone gets the kept file's name and its folder, or '' when the file turned out to read fine. onRecheck: the set-aside failed, so the page
  // reads the list again and stops offering this when the file is not unreadable after all. page: the look of the Sources page, not the drawer.
  // readOnly: the page is read-only for this person (the same switch that disables Forget, Test, Edit and Add), so nothing here can be pressed.
  let { onDone, onRecheck, detail = '', shared = false, page = false, readOnly = false }: {
    onDone: (keptName: string, folder: string) => void | Promise<void>;
    onRecheck?: () => void | Promise<void>;
    detail?: string;
    shared?: boolean;
    page?: boolean;
    readOnly?: boolean;
  } = $props();

  let phase = $state<'idle' | 'ask'>('idle');
  let busy = $state(false);
  let problem = $state('');
  let keepButton = $state<HTMLButtonElement>();
  let askButton = $state<HTMLButtonElement>();

  // The question takes the focus on the safe answer, and No or Esc hands it back to the button that opened it.
  async function ask() { problem = ''; phase = 'ask'; await tick(); keepButton?.focus(); }
  async function keep() { phase = 'idle'; await tick(); askButton?.focus(); }
  function onKey(event: KeyboardEvent) {
    if (event.key !== 'Escape' || phase !== 'ask' || busy) return;
    event.preventDefault(); event.stopPropagation(); void keep();
  }

  async function setAside() {
    if (busy || readOnly || phase !== 'ask') return;
    busy = true; problem = '';
    try {
      const reply = await api.setAsideConnections();
      await onDone(typeof reply?.name === 'string' ? reply.name : '', typeof reply?.folder === 'string' ? reply.folder : '');
    } catch (cause) {
      const failure = setAsideFailure(cause);
      phase = 'idle';
      if (failure.healthy) await onDone('', '');
      else {
        problem = failure.text;
        if (failure.recheck) await onRecheck?.();
      }
    } finally { busy = false; }
  }
</script>

<div class="saved-file-damaged" data-saved-file-damaged>
  <p class={page ? 'say bad' : 'connection-status warning'} role="alert">{detail !== '' ? detail : SAVED_FILE_TEXT.damaged}</p>
  {#if problem !== ''}<p class={page ? 'say bad' : 'inline-error'} role="alert" data-set-aside-problem>{problem}</p>{/if}
  {#if phase === 'ask'}
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <div class="set-aside-confirm" role="group" aria-label={SAVED_FILE_TEXT.button} data-set-aside-confirm onkeydown={onKey}>
      <p>{askText(shared)}</p>
      <div class={page ? 'actions' : 'saved-file-actions'}>
        <button type="button" class={page ? 'btn' : 'secondary-button'} disabled={busy || readOnly} data-set-aside onclick={() => void setAside()}>{busy ? SAVED_FILE_TEXT.working : SAVED_FILE_TEXT.yes}</button>
        <button type="button" class={page ? 'btn' : 'secondary-button'} disabled={busy} bind:this={keepButton} data-set-aside-keep onclick={() => void keep()}>{SAVED_FILE_TEXT.keep}</button>
      </div>
    </div>
  {:else}
    <div class={page ? 'actions' : 'saved-file-actions'}>
      <button type="button" class={page ? 'btn' : 'secondary-button'} bind:this={askButton} disabled={readOnly} data-set-aside-ask onclick={() => void ask()}>{SAVED_FILE_TEXT.button}</button>
    </div>
  {/if}
</div>

<style>
  .saved-file-damaged { display: grid; gap: 8px; }
  .saved-file-actions { display: flex; flex-wrap: wrap; gap: 8px; }
  .set-aside-confirm { display: grid; gap: 8px; padding: 10px 12px; border: 1px solid var(--control-line); border-radius: var(--radius-sm); background: var(--surface-2); }
  .set-aside-confirm p { margin: 0; font-size: 13px; }
</style>
