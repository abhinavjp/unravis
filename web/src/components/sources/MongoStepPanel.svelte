<script lang="ts">
  import SourceStep from './SourceStep.svelte';

  // Step 8: the MongoDB server of this case (profiler and health), not the audit logs. Says Cannot test yet until a server has been read.
  let { caseId, readOnly = false, onGone, onChanged, onAddData }: { caseId: string; readOnly?: boolean; onGone?: () => void; onChanged?: () => void; onAddData?: () => void } = $props();
</script>

<SourceStep {caseId} kind="mongo" {readOnly} {onGone} {onChanged} {onAddData} />
