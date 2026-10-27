<script lang="ts">
  import SourceStep from './SourceStep.svelte';

  // Step 7: the SQL Server checks of this case. The guided shell mounts this; the page here only reads what the case already holds.
  let { caseId, onGone, onChanged, onAddData }: { caseId: string; onGone?: () => void; onChanged?: () => void; onAddData?: () => void } = $props();
</script>

<SourceStep {caseId} kind="sql" {onGone} {onChanged} {onAddData} />
