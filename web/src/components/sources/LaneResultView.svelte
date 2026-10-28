<script lang="ts">
  import { nothingRead, type LaneDay } from '../../lib/lanes';
  import { STATE_NOTE, STEP_TEXT, GRANT_TEXT } from '../../lib/copy/sourcesCopy';
  import NothingRead from './NothingRead.svelte';
  import StateSticker from './StateSticker.svelte';
  import './sources.css';

  // What one day of a SQL Server or MongoDB read found: the word for the whole read, each check with its word, the permission a check was
  // missing with the line to fix it, and the questions it answered. Only checks that ran count; a check that could not run is never a pass.
  let { day, onAddDay, onAddData }: { day: LaneDay; onAddDay?: () => void; onAddData?: () => void } = $props();

  const lane = $derived(day.lane);
  const word = $derived(lane ? lane.state : day.state);
  const fixes = $derived((lane?.checks ?? []).filter(c => c.fix !== null));
  const answers = $derived(Object.entries(lane?.answers ?? {}).sort(([a], [b]) => a.localeCompare(b)));
  const range = (w: { start: string; end: string }) => `${w.start.slice(0, 16).replace('T', ' ')} to ${w.end.slice(0, 16).replace('T', ' ')} UTC`;
  let copied = $state('');
  async function copy(id: string, text: string) {
    try { await navigator.clipboard.writeText(text); copied = id; } catch { copied = 'no'; }
  }
</script>

<div class="src" data-day={day.id}>
  <div class="actions">
    <StateSticker word={word} />
    <span class="hint">{STATE_NOTE[word] ?? ''}</span>
  </div>
  <p class="hint">{day.label}{day.database ? ' · ' + day.database : ''} · {range(day.incident)}</p>

  {#if lane && nothingRead(lane)}
    <NothingRead kind={day.kind} {onAddDay} {onAddData} />
  {/if}

  {#if lane && lane.checks.length > 0}
    <div class="table-wrap">
      <table>
        <thead><tr><th scope="col">{STEP_TEXT.checkCol}</th><th scope="col">{STEP_TEXT.stateCol}</th><th scope="col"></th></tr></thead>
        <tbody>
          {#each lane.checks as check (check.id)}
            <tr data-check={check.id}>
              <td>{check.title}</td>
              <td><StateSticker word={check.state} /></td>
              <td>{check.summary}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}

  {#if fixes.length > 0}
    <div class="field">
      <h5>{STEP_TEXT.fixHeading}</h5>
      <p class="hint">{GRANT_TEXT.notRun}</p>
      {#each fixes as check (check.id)}
        <div class="field" data-fix={check.id}>
          <p><b>{check.title}</b>{check.fix && check.fix.permission ? ' · ' + check.fix.permission : ''}</p>
          <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
          <pre class="script" tabindex="0" aria-label={check.title}>{check.fix?.script}</pre>
          <div class="actions">
            <button type="button" class="btn" onclick={() => void copy(check.id, check.fix?.script ?? '')}>{GRANT_TEXT.copy}</button>
            <span class="hint" aria-live="polite">{copied === check.id ? GRANT_TEXT.copied : copied === 'no' ? GRANT_TEXT.copyFailed : ''}</span>
          </div>
        </div>
      {/each}
    </div>
  {/if}

  <div class="field">
    <h5>{STEP_TEXT.answersHeading}</h5>
    {#if answers.length === 0}
      <p class="hint">{STEP_TEXT.noAnswers}</p>
    {:else}
      <ul class="rows">
        {#each answers as [id, value] (id)}
          <li><span class="mono">{id}</span><span>{value === 'yes' ? STEP_TEXT.answerYes : STEP_TEXT.answerNo}</span></li>
        {/each}
      </ul>
    {/if}
    {#if lane && lane.onset !== ''}<p class="hint">{STEP_TEXT.onsetLabel} {lane.onset.slice(0, 16).replace('T', ' ')} UTC</p>{/if}
    <p class="hint">{STEP_TEXT.conclusion}</p>
  </div>
</div>
