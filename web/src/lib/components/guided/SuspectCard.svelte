<script lang="ts">
  import { GUIDED_TEXT as T } from '../../copy/guidedCopy';
  import { COLOUR_WORD, evidenceKind } from '../../guided/stepModel';
  import type { Suspect } from '../../guided/types';

  // One suspect. The colour is a left bar and a word, never colour alone. Grey is "not sure", never a pass.
  let { suspect, compact = false, canAdd = false, onAdd }: { suspect: Suspect; compact?: boolean; canAdd?: boolean; onAdd?: (suspect: Suspect) => void } = $props();
  const kind = $derived(evidenceKind(suspect));
</script>

<article class={'suspect colour-' + suspect.colour} aria-label={`${suspect.id}: ${suspect.title}`}>
  <header>
    <span class="colour"><span class="dot" aria-hidden="true"></span>{COLOUR_WORD[suspect.colour]}</span>
    <h4>{suspect.id}. {suspect.title}</h4>
    <span class="result">{suspect.result} · {kind === 'Observed' ? T.observed : T.inferred}</span>
  </header>
  {#if suspect.why}<p class="why"><strong>{T.whyLabel}:</strong> {suspect.why}</p>{/if}
  {#if suspect.needs && suspect.colour === 'grey'}<p class="needs"><strong>{T.needsLabel}:</strong> {suspect.needs}</p>{/if}
  {#if !compact && suspect.evidence.length > 0}
    <div class="evidence">
      <strong>{T.evidenceLabel}</strong>
      <ul>{#each suspect.evidence as e, i (i)}<li>{e.why || e.signal}</li>{/each}</ul>
    </div>
  {/if}
  {#if suspect.nextCheck}<p class="check"><strong>{T.cheapest}:</strong> {suspect.nextCheck}</p>{/if}
  {#if !compact && suspect.overturn}<p class="overturn"><strong>{T.overturn}:</strong> {suspect.overturn}</p>{/if}
  {#if canAdd && suspect.nextCheck && onAdd}
    <div><button type="button" class="quiet-button" onclick={() => onAdd(suspect)}>+ {T.addCheck}</button></div>
  {/if}
</article>

<style>
  .suspect { display: grid; gap: 6px; padding: 10px 12px; background: var(--surface); border: 1px solid var(--line); border-left-width: 4px; border-radius: var(--radius-sm); min-width: 0; }
  .colour-red { border-left-color: var(--suspect-red); }
  .colour-orange { border-left-color: var(--suspect-orange); }
  .colour-green { border-left-color: var(--suspect-green); }
  .colour-grey { border-left-color: var(--suspect-grey); }
  header { display: flex; flex-wrap: wrap; align-items: baseline; gap: 4px 12px; }
  h4 { margin: 0; font-size: 14px; overflow-wrap: anywhere; }
  .colour { display: inline-flex; align-items: center; gap: 6px; padding: 0 8px; font-size: 12px; font-weight: 600; border-radius: var(--radius-sm); }
  .colour-red .colour { background: var(--suspect-red-soft); }
  .colour-orange .colour { background: var(--suspect-orange-soft); }
  .colour-green .colour { background: var(--suspect-green-soft); }
  .colour-grey .colour { background: var(--suspect-grey-soft); }
  .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--suspect-grey); }
  .colour-red .dot { background: var(--suspect-red); }
  .colour-orange .dot { background: var(--suspect-orange); }
  .colour-green .dot { background: var(--suspect-green); }
  .result { color: var(--text-2); font-size: 12px; }
  p { margin: 0; font-size: 13px; overflow-wrap: anywhere; }
  .evidence { display: grid; gap: 2px; font-size: 13px; }
  ul { margin: 0; padding-left: 18px; color: var(--text-2); }
</style>
