<script lang="ts">
  import { api } from '../../lib/api';
  import { SAVED_FILE_TEXT } from '../../lib/copy/savedFileCopy';
  import { setAsideFailure } from '../../lib/savedFile';

  // RV-DAT-1 (W-13): shown only when the server says the saved MongoDB connections file cannot be read. It replaces "Try again", which cannot
  // help, with one button. The server renames the file and keeps it (never deletes it), and refuses unless the file is really unreadable.
  // onDone gets the kept file's name, or '' when the file turned out to read fine. page: the look of the Sources page instead of the drawer.
  let { onDone, page = false }: { onDone: (keptName: string) => void | Promise<void>; page?: boolean } = $props();

  let busy = $state(false);
  let problem = $state('');

  async function setAside() {
    if (busy) return;
    busy = true; problem = '';
    try {
      const reply = await api.setAsideConnections();
      await onDone(typeof reply?.name === 'string' ? reply.name : '');
    } catch (cause) {
      const failure = setAsideFailure(cause);
      if (failure.healthy) await onDone('');
      else problem = failure.text;
    } finally { busy = false; }
  }
</script>

<div class="saved-file-damaged" data-saved-file-damaged>
  <p class={page ? 'say bad' : 'connection-status warning'} role="alert">{SAVED_FILE_TEXT.damaged}</p>
  {#if problem !== ''}<p class={page ? 'say bad' : 'inline-error'} role="alert" data-set-aside-problem>{problem}</p>{/if}
  <div class={page ? 'actions' : 'saved-file-actions'}>
    <button type="button" class={page ? 'btn' : 'secondary-button'} disabled={busy} data-set-aside onclick={() => void setAside()}>{busy ? SAVED_FILE_TEXT.working : SAVED_FILE_TEXT.button}</button>
  </div>
</div>

<style>
  .saved-file-damaged { display: grid; gap: 8px; }
  .saved-file-actions { display: flex; }
</style>
