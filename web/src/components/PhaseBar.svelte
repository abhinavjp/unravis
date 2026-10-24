<script lang="ts">
  import type { PhaseMeans } from '../lib/types';
  import { phaseMs, phaseSegments, phaseTip } from '../lib/phases';
  let { phases = null, delta = null }: { phases?: PhaseMeans | null; delta?: PhaseMeans | null } = $props();
  let segments = $derived(phaseSegments(phases));
  let hasPayload = $derived(phases?.payloadMs != null && Number.isFinite(phases.payloadMs));
</script>

{#if segments || hasPayload}
  <div class="phase-bar" title={phaseTip(delta)}>
    {#if segments}
      <div class="phase-track" role="img" aria-label={`Mean time split: audit ${phaseMs(segments.audit)}, API ${phaseMs(segments.api)}`}>
        {#if segments.audit != null && segments.auditPct > 0}<span class="phase-audit" style:width={segments.auditPct + '%'}></span>{/if}
        {#if segments.api != null && segments.apiPct > 0}<span class="phase-api" style:width={segments.apiPct + '%'}></span>{/if}
      </div>
      <small>audit {phaseMs(segments.audit)} · api {phaseMs(segments.api)}</small>
    {/if}
    {#if hasPayload}<small>payload {phaseMs(phases?.payloadMs)} (after response, not latency)</small>{/if}
  </div>
{/if}

<style>
  .phase-bar { display: grid; gap: 2px; margin-top: 4px; max-width: 340px; }
  .phase-track { display: flex; height: 6px; overflow: hidden; border-radius: 3px; background: var(--line-soft); }
  .phase-audit { background: var(--pending); }
  .phase-api { background: var(--accent); }
  small { color: var(--text-2); font-size: 12px; white-space: normal; }
</style>
