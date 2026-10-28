<script lang="ts">
  import { ARCHIVE_TEXT } from '../copy/archiveCopy';

  // Free and Restore as new need the master key of this computer. When none is set up yet there is nothing to unlock, so say that before the
  // person starts a check or types anything, and offer the one way forward. `what` picks the words ('free' or 'restore').
  let { what, onSetup, onClose }: { what: 'free' | 'restore'; onSetup: () => void; onClose: () => void } = $props();
</script>

<section class="no-key" aria-label={ARCHIVE_TEXT.noMasterKeyTitle}>
  <p class="warn" role="status">{what === 'free' ? ARCHIVE_TEXT.noMasterKeyFree : ARCHIVE_TEXT.noMasterKeyRestore}</p>
  <div class="actions">
    <button type="button" class="primary-button" onclick={onSetup}>{ARCHIVE_TEXT.noMasterKeySetup}</button>
    <button type="button" class="secondary-button" onclick={onClose}>{ARCHIVE_TEXT.noMasterKeyLater}</button>
  </div>
</section>

<style>
  .no-key { display: grid; gap: var(--space-2); }
  .warn { margin: 0; font-size: var(--fs-3); }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); margin-top: var(--space-2); }
</style>
