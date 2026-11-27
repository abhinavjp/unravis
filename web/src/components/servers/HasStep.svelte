<script lang="ts">
  import StepFooter from './StepFooter.svelte';
  import HasCase from './HasCase.svelte';
  import HasMachine from './HasMachine.svelte';
  import type { ServersFlow } from '../../lib/serversFlow.svelte';
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';
  import { formatBytes, pickedTotals, planSummary } from '../../lib/servers';

  // Step 5. The file list of each folder that could be read, for a time range. Nothing is copied here. The case's own windows are
  // shown read only; changing them is done in the Investigation timeline.
  let { flow, inCase, onChangeWindows }: { flow: ServersFlow; inCase: boolean; onChangeWindows: () => void } = $props();

  const totals = $derived(pickedTotals(flow.chosenKeys.map((k) => flow.rows[k]?.plan ?? null)));
  const loading = $derived(flow.targets.some((t) => flow.rows[t.key]?.state === 'loading'));
  const machines = $derived([...new Set(flow.targets.map((t) => t.machine))]);
  const seenFiles = $derived(flow.targets.reduce((n, t) => { const p = flow.rows[t.key]?.plan; return n + (p ? planSummary(p).files : 0); }, 0));
</script>

<section class="sv-block" aria-labelledby="sv-has-title">
  <h3 id="sv-has-title">{T.hasTitle}</h3>
  <p class="sv-lead">{T.hasLead}</p>
  {#if !loading}<p class="sv-note">{T.hasSeen(seenFiles, machines.length)}</p>{:else}<p class="sv-note" role="status">{T.hasPlanning}</p>{/if}

  <HasCase {flow} {inCase} {loading} {onChangeWindows} />
  {#each machines as m (m)}<HasMachine {flow} machine={m} />{/each}

  <p class="sv-note">{T.hasLimit}</p>
  <StepFooter back={() => flow.go('reach')} summary={T.hasTotals(totals.items, totals.files, formatBytes(totals.bytes))} primary={{ label: T.hasGo(totals.items), onclick: () => flow.startPulls(), disabled: totals.items === 0 || loading || !!flow.busy }} reason={totals.items === 0 && !loading ? T.hasNothing : ''} />
</section>
