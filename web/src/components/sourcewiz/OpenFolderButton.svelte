<script lang="ts">
  import { openFolder } from '../../lib/sourceWizardApi';
  import type { OpenTarget } from '../../lib/sourceWizardApi';
  import { WIZ } from '../../lib/copy/sourceWizardCopy';

  // "Open folder" next to a folder path or a found source. Asks the local server to open Explorer; the answer is a plain line, never a path.
  let { target, what, onLine }: { target: OpenTarget; what: string; onLine?: (line: string) => void } = $props();
  let busy = $state(false);

  async function open() {
    if (busy) return;
    busy = true;
    const result = await openFolder(target);
    busy = false;
    onLine?.(result.opened ? '' : result.line);
  }
</script>

<button type="button" class="quiet-button open-folder" disabled={busy} aria-label={WIZ.openFolderLabel(what)} title="Show this folder in Windows Explorer" onclick={() => void open()}>{WIZ.openFolder}</button>
