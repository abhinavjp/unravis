<script lang="ts">
  import { onMount } from 'svelte';
  import { fetchLaneDays, groupSources, type LaneDay } from '../../lib/lanes';
  import { COMBO_TEXT, NUTSHELL } from '../../lib/copy/sourcesCopy';
  import './sources.css';

  // Screen 114: what each combination of sources can answer, so the owner knows what to add next. A step a source cannot answer says Cannot
  // test yet, never a pass. The last row says what this case holds today, counted from the days read; it reads nothing itself.
  let { caseId }: { caseId: string } = $props();
  let days = $state.raw<LaneDay[]>([]);
  let loaded = $state(false);
  const sources = $derived(groupSources(days));
  const today = $derived(sources.map(s => s.label + (s.database ? ' · ' + s.database : '') + ' (' + s.days.length + ')').join(', '));
  onMount(() => {
    const mine = new AbortController();
    void fetchLaneDays(caseId, mine.signal)
      .then(list => { if (!mine.signal.aborted) days = list; })
      .catch(() => {})
      .finally(() => { if (!mine.signal.aborted) loaded = true; });
    return () => mine.abort();
  });
</script>

<section class="src" aria-labelledby="combo-head" data-combinations>
  <div class="nutshell"><b>{NUTSHELL}</b><p>{COMBO_TEXT.lead}</p></div>
  <h3 id="combo-head">{COMBO_TEXT.heading}</h3>
  <div class="table-wrap">
    <table>
      <thead><tr><th scope="col">{COMBO_TEXT.colHolds}</th><th scope="col">{COMBO_TEXT.colAnswers}</th><th scope="col">{COMBO_TEXT.colMissing}</th></tr></thead>
      <tbody>
        {#each COMBO_TEXT.rows as row (row.holds)}
          <tr><td>{row.holds}</td><td>{row.answers}</td><td>{row.missing}</td></tr>
        {/each}
        <tr data-today>
          <th scope="row">{COMBO_TEXT.today}</th>
          <td colspan="2">{#if !loaded}...{:else if sources.length === 0}{COMBO_TEXT.todayNone}{:else}{today}{/if}</td>
        </tr>
      </tbody>
    </table>
  </div>
  <p class="hint">{COMBO_TEXT.next}</p>
  <p class="hint">{COMBO_TEXT.note}</p>
</section>
