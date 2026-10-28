<script lang="ts">
  import { GUIDED_TEXT as T } from '../../copy/guidedCopy';
  import { SHELL_TEXT as S } from '../../copy/guidedShellCopy';
  import type { GuidedStore } from '../../guided/guided.svelte';
  import { cheapestCheck, hasNamedOrigin, originRows, stepOf, stickerFor, suspectGroups } from '../../guided/stepModel';
  import { likelyWithoutTier } from '../../guided/tools';
  import type { Suspect } from '../../guided/types';
  import StepShell from './StepShell.svelte';
  import SuspectCard from './SuspectCard.svelte';

  // Step 10: what moved first, which suspects fit the data, how sure we are and the one check that would overturn the answer.
  // Unravis does not name a culprit. A suspect that could not be tested is grey, never a pass.
  let { store, sourceIds, readOnly, onOpenChecklist }: { store: GuidedStore; sourceIds: string[]; readOnly: boolean; onOpenChecklist: () => void } = $props();

  const result = $derived(store.result);
  const sticker = $derived(stickerFor(result, 10, store.phase, store.outOfDate(sourceIds)));
  const engineStep = $derived(stepOf(result, 10));
  const groups = $derived(result ? suspectGroups(result) : { ranked: [], notTested: [], setAside: [] });
  const cheapest = $derived(result ? cheapestCheck(result) : null);
  const likelyNoTier = $derived(likelyWithoutTier(result));
  let showSetAside = $state(false);

  const checkText = (s: Suspect) => `${s.id}: ${s.nextCheck}`.slice(0, 300);
  // The same check is added once, however many times the button is pressed.
  const onList = (s: Suspect) => store.items.some((i) => i.text.trim().toLowerCase() === checkText(s).toLowerCase());
  const addCheck = (s: Suspect) => { if (!onList(s)) void store.addItem('todo', checkText(s), s.id); };
  const list = (items: string[]) => (items.length ? items : [T.noneWord]);
</script>

<StepShell step={10} {sticker} nutshell={engineStep?.nutshell ?? ''} reasons={engineStep?.reasons ?? []} onGo={(n) => store.goTo(n)} nav={store.mode === 'guided'}>
  {#if !result}
    <p class="note">{store.phase === 'running' ? T.running : T.jumpedAhead}</p>
  {:else}
    <p class="lead">{T.originLead}</p>

    <section aria-labelledby="origin-moved">
      <h4 id="origin-moved">{T.moved}</h4>
      {#if result.origin.status === 'not enough data'}
        <p class="finding">{T.notEnoughOrigin}</p>
      {:else}
        {#if !hasNamedOrigin(result)}
          <p class="finding"><strong>{T.noClearOrigin}.</strong> {T.noClearOriginNote}</p>
        {:else if result.origin.label}
          <p class="finding">{result.origin.label}</p>
        {/if}
        {#if originRows(result).length > 0}
          <div class="table-scroll">
            <table>
              <thead><tr><th scope="col">{T.tierCol}</th><th scope="col">{T.firstMovedCol} (UTC)</th><th scope="col">{T.whatMovedCol}</th><th scope="col">{T.roleCol}</th></tr></thead>
              <tbody>{#each originRows(result) as row (row.tier)}<tr><td>{row.tier}</td><td>{row.when}</td><td>{row.what}</td><td>{row.role}</td></tr>{/each}</tbody>
            </table>
          </div>
        {/if}
      {/if}
      {#if result.origin.note}<p class="note">{result.origin.note}</p>{/if}
      {#if result.origin.clockNote}<p class="note">{result.origin.clockNote}</p>{/if}
    </section>

    <section aria-labelledby="origin-colours">
      <h4 id="origin-colours">{T.colours}</h4>
      <ul class="legend">
        <li class="c-red"><span class="dot" aria-hidden="true"></span>{T.red}</li>
        <li class="c-orange"><span class="dot" aria-hidden="true"></span>{T.orange}</li>
        <li class="c-green"><span class="dot" aria-hidden="true"></span>{T.green}</li>
        <li class="c-grey"><span class="dot" aria-hidden="true"></span>{T.grey}</li>
      </ul>
    </section>

    <section aria-labelledby="origin-suspects">
      <h4 id="origin-suspects">{T.suspectsHeading}</h4>
      {#if readOnly && store.mode === 'guided' && (groups.ranked.length > 0 || groups.notTested.length > 0)}<p class="notice" role="status">{S.closedNoAdd}</p>{/if}
      {#if groups.ranked.length === 0}
        <p class="note">{T.noRanked}</p>
      {:else}
        <div class="stack">{#each groups.ranked as s (s.id)}<SuspectCard suspect={s} canAdd={!readOnly} added={onList(s)} onAdd={addCheck} />{/each}</div>
      {/if}
    </section>

    {#if groups.notTested.length > 0}
      <section aria-labelledby="origin-grey">
        <h4 id="origin-grey">{T.notTested}</h4>
        <div class="stack">{#each groups.notTested as s (s.id)}<SuspectCard suspect={s} compact canAdd={!readOnly} added={onList(s)} onAdd={addCheck} />{/each}</div>
      </section>
    {/if}

    {#if groups.setAside.length > 0}
      <section aria-labelledby="origin-green">
        <h4 id="origin-green">{T.setAside(groups.setAside.length)}</h4>
        <div><button type="button" class="quiet-button" aria-expanded={showSetAside} onclick={() => (showSetAside = !showSetAside)}>{showSetAside ? S.showFewerSuspects : S.showRuledOut}</button></div>
        {#if showSetAside}
          <div class="stack">{#each groups.setAside as s (s.id)}<SuspectCard suspect={s} compact />{/each}</div>
        {/if}
      </section>
    {/if}

    {#if cheapest}
      <section aria-labelledby="origin-cheapest">
        <h4 id="origin-cheapest">{T.cheapest}</h4>
        <p class="finding">{cheapest.suspect.id}: {cheapest.text}</p>
      </section>
    {/if}

    <section aria-labelledby="origin-sure">
      <h4 id="origin-sure">{T.howSure}</h4>
      <p class="finding"><strong>{T.confidenceWord(result.confidence.word)}.</strong> {result.confidence.why}</p>
      {#if likelyNoTier}<p class="note">{S.likelyNoTier}</p>{/if}
      <div class="table-scroll">
        <table>
          <thead><tr><th scope="col">{T.sureCol}</th><th scope="col">{T.stateCol}</th></tr></thead>
          <tbody>
            <tr><td>{T.sourcesUsed}</td><td>{list(result.confidence.sourcesUsed).join(', ')}</td></tr>
            <tr><td>{T.sourcesMissing}</td><td>{list(result.confidence.sourcesMissing).join(', ')}</td></tr>
            <tr><td>{T.suspectsNotTested}</td><td>{list(result.confidence.notTested).join(', ')}</td></tr>
          </tbody>
        </table>
      </div>
      <p class="finding"><strong>{T.oneCheck}:</strong> {result.confidence.overturn || T.noneWord}</p>
    </section>

    <div class="row"><button type="button" class="secondary-button" onclick={onOpenChecklist}>{T.openChecklist}</button></div>
  {/if}
</StepShell>

<style>
  section { display: grid; gap: 8px; min-width: 0; }
  h4 { margin: 0; font-size: 14px; }
  .lead, .note, .finding { margin: 0; font-size: 13px; overflow-wrap: anywhere; }
  .lead { font-size: 14px; }
  .note { color: var(--text-2); font-size: 12px; }
  .finding { padding: 8px 12px; background: var(--surface-2); border-radius: var(--radius-sm); font-size: 14px; }
  table { white-space: normal; }
  .legend { display: grid; gap: 4px; margin: 0; padding: 0; list-style: none; font-size: 13px; }
  .legend li { display: flex; align-items: center; gap: 8px; }
  .dot { flex: none; width: 10px; height: 10px; border-radius: 50%; background: var(--suspect-grey); }
  .c-red .dot { background: var(--suspect-red); }
  .c-orange .dot { background: var(--suspect-orange); }
  .c-green .dot { background: var(--suspect-green); }
  .stack { display: grid; gap: 8px; }
  .row { display: flex; flex-wrap: wrap; gap: 8px; }
</style>
