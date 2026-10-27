<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../lib/api';
  import { fetchLaneDays, groupSources, readableByDay, type LaneDay } from '../../lib/lanes';
  import { ADD_TEXT, NUTSHELL } from '../../lib/copy/sourcesCopy';
  import './sources.css';

  // Screen 112: add data to a case after it was made. Every card reads on its own and does not touch what is there. This screen only counts what
  // exists and hands the choice on; nothing is read or changed here. A step that ran before the new data arrived is marked Out of date by the
  // screen that finishes the add, never by itself.
  export type AddChoice = 'sql' | 'mongo' | 'blackbox' | 'files' | 'day';
  let { caseId, onChoose }: { caseId: string; onChoose?: (what: AddChoice) => void } = $props();

  let days = $state.raw<LaneDay[]>([]);
  let sqlSaved = $state(0);
  let mongoSaved = $state(0);

  const sources = $derived(groupSources(days));
  const sqlHeld = $derived(sources.filter(s => s.kind === 'sql').length);
  const mongoHeld = $derived(sources.filter(s => s.kind === 'mongo').length);
  const byDay = $derived(sources.filter(s => readableByDay(s.kind)).length);

  onMount(() => {
    const mine = new AbortController();
    void (async () => {
      const [list, sql, mongo] = await Promise.allSettled([fetchLaneDays(caseId, mine.signal), api.sqlConnections(), api.recentConnections()]);
      if (mine.signal.aborted) return;
      if (list.status === 'fulfilled') days = list.value;
      if (sql.status === 'fulfilled' && Array.isArray(sql.value)) sqlSaved = sql.value.length;
      if (mongo.status === 'fulfilled' && Array.isArray(mongo.value)) mongoSaved = mongo.value.length;
    })();
    return () => mine.abort();
  });
  const choose = (what: AddChoice) => onChoose?.(what);
</script>

<section class="src" aria-labelledby="add-head">
  <div class="nutshell"><b>{NUTSHELL}</b><p>{ADD_TEXT.lead}</p></div>
  <h3 id="add-head">{ADD_TEXT.heading}</h3>
  <div class="cards">
    <div class="card" data-card="sql">
      <h4>{ADD_TEXT.sqlTitle}</h4>
      <p class="hint">{ADD_TEXT.sqlBody}</p>
      <p class="hint">{ADD_TEXT.connReady(sqlSaved)}</p>
      <p class="hint">{ADD_TEXT.held(sqlHeld)}</p>
      <div class="actions"><button type="button" class="btn" onclick={() => choose('sql')}>{ADD_TEXT.addSql}</button></div>
    </div>
    <div class="card" data-card="mongo">
      <h4>{ADD_TEXT.mongoTitle}</h4>
      <p class="hint">{ADD_TEXT.mongoBody}</p>
      <p class="hint">{ADD_TEXT.connReady(mongoSaved)}</p>
      <p class="hint">{ADD_TEXT.held(mongoHeld)}</p>
      <div class="actions"><button type="button" class="btn" onclick={() => choose('mongo')}>{ADD_TEXT.addMongo}</button></div>
    </div>
    <div class="card" data-card="blackbox">
      <h4>{ADD_TEXT.blackboxTitle}</h4>
      <p class="hint">{ADD_TEXT.blackboxBody}</p>
      <div class="actions"><button type="button" class="btn" onclick={() => choose('blackbox')}>{ADD_TEXT.addBlackbox}</button></div>
    </div>
    <div class="card" data-card="files">
      <h4>{ADD_TEXT.filesTitle}</h4>
      <p class="hint">{ADD_TEXT.filesBody}</p>
      <div class="actions"><button type="button" class="btn" onclick={() => choose('files')}>{ADD_TEXT.addFiles}</button></div>
    </div>
    <div class="card" data-card="day">
      <h4>{ADD_TEXT.dayTitle}</h4>
      <p class="hint">{ADD_TEXT.dayBody}</p>
      <p class="hint">{ADD_TEXT.byDay(byDay, sources.length)}</p>
      <div class="actions"><button type="button" class="btn" disabled={byDay === 0} onclick={() => choose('day')}>{ADD_TEXT.addDay}</button></div>
    </div>
  </div>
  <p class="hint">{ADD_TEXT.outOfDate}</p>
  <p class="hint">{ADD_TEXT.readsOnly}</p>
</section>
