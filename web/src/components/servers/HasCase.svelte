<script lang="ts">
  import type { ServersFlow } from '../../lib/serversFlow.svelte';
  import { SV_TEXT as T } from '../../lib/copy/serversCopy';
  import { caseRangeProblem, formatUtc, rangeChoices, rangeLabel, type RangeChoice } from '../../lib/servers';

  // The "What is this for?" panel of step 5. The case's own windows are read from the case and shown read only.
  let { flow, inCase, loading, onChangeWindows }: { flow: ServersFlow; inCase: boolean; loading: boolean; onChangeWindows: () => void } = $props();

  const range = $derived(flow.caseRange());
  const choices = $derived(rangeChoices(range));
  const problem = $derived(caseRangeProblem(range));
</script>

<div class="sv-panel">
  <header><h4>{T.hasWhyHeading}</h4><span class="sv-sp"></span><span class="sv-chip">{inCase ? T.hasForCase : T.hasNotForCase}</span></header>
  <div class="sv-in">
    {#if inCase}
      {#if range.state === 'ok'}
        <dl class="sv-kv">
          <dt>{T.hasWindow}</dt><dd><b>{formatUtc(range.incident.start)} to {formatUtc(range.incident.end)} UTC</b></dd>
          <dt>{T.hasReference}</dt><dd><b>{formatUtc(range.comparison.start)} to {formatUtc(range.comparison.end)} UTC</b></dd>
          <dt>{T.hasPulledFor}</dt><dd>{T.hasPulledForValue}</dd>
        </dl>
        <p class="sv-note">{T.hasReadOnly}</p>
      {:else}
        <p class="sv-note">{problem}</p>
      {/if}
      <div class="sv-actions"><button type="button" class="secondary-button" onclick={onChangeWindows}>{T.hasCaseChange}</button></div>
    {:else}
      <p class="sv-note">{T.hasNotForCaseNote} {T.hasCaseOutside}</p>
    {/if}
    {#if flow.targets.length > 1}
      <label class="sv-field sv-inline"><span>{T.hasRangeAll}</span>
        <select disabled={loading} onchange={(e) => { const v = e.currentTarget.value; e.currentTarget.value = ''; if (v) void flow.setAllRanges(v as RangeChoice); }}>
          <option value="">-</option>
          {#each choices as c (c)}<option value={c}>{rangeLabel(c)}</option>{/each}
        </select></label>
    {/if}
  </div>
</div>
