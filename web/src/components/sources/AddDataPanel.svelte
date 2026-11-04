<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../../lib/api';
  import { BLACKBOX_TAG_KIND } from '../../lib/blackboxCase';
  import { listImports } from '../../lib/cases';
  import { caseUseApi } from '../../lib/blackboxCaseApi';
  import { fetchLaneDays, groupSources, readableByDay, type LaneDay } from '../../lib/lanes';
  import { ADD_TEXT, HUB_TEXT, NUTSHELL } from '../../lib/copy/sourcesCopy';
  import './sources.css';

  // Screen 112: add data to a case after it was made. Every card reads on its own and does not touch what is there. This screen only counts what
  // exists and hands the choice on; nothing is read or changed here. A step that ran before the new data arrived is marked Out of date by the
  // screen that finishes the add, never by itself.
  export type AddChoice = 'sql' | 'mongo' | 'blackbox' | 'files' | 'day';
  // files says whether the page that adds files exists in this build. A card whose page is missing is switched off, never a dead click. The Blackbox page is always there.
  // In the import drawer (scope 'drawer') the same cards open over the case page, which has this panel too: the ids and the card marks differ so the two never collide.
  // staged is how many files the drawer holds that are not imported yet; busy is an import sending now (nothing else starts, and the card says why).
  let { caseId, readOnly = false, files = false, scope = 'hub', staged = 0, busy = false, onChoose }: { caseId: string; readOnly?: boolean; files?: boolean; scope?: 'hub' | 'drawer'; staged?: number; busy?: boolean; onChoose?: (what: AddChoice) => void } = $props();
  const drawer = $derived(scope === 'drawer');
  const mark = (id: string): Record<string, string> => (drawer ? { 'data-drawer-card': id } : { 'data-card': id });
  const headId = $derived(drawer ? 'drawer-add-head' : 'add-head');
  const off = $derived(readOnly || busy);

  let days = $state.raw<LaneDay[]>([]);
  let sqlSaved = $state(0);
  let mongoSaved = $state(0);
  // The case keeps one tag per Blackbox it reads (a case reads one), so the card can say whether this case does.
  let blackboxHeld = $state(0);
  // How many files the case already holds (each imported file is one record), so the Files card says None yet only when that is true.
  let filesHeld = $state(0);

  const sources = $derived(groupSources(days));
  const sqlHeld = $derived(sources.filter(s => s.kind === 'sql').length);
  const mongoHeld = $derived(sources.filter(s => s.kind === 'mongo').length);
  const byDay = $derived(sources.filter(s => readableByDay(s.kind)).length);

  onMount(() => {
    const mine = new AbortController();
    void (async () => {
      const [list, sql, mongo, tags, imports] = await Promise.allSettled([fetchLaneDays(caseId, mine.signal), api.sqlConnections(), api.recentConnections(), caseUseApi.listTags(caseId), listImports(caseId, mine.signal)]);
      if (mine.signal.aborted) return;
      if (list.status === 'fulfilled') days = list.value;
      if (sql.status === 'fulfilled' && Array.isArray(sql.value)) sqlSaved = sql.value.length;
      if (mongo.status === 'fulfilled' && Array.isArray(mongo.value)) mongoSaved = mongo.value.length;
      if (imports.status === 'fulfilled') filesHeld = imports.value.length;
      if (tags.status === 'fulfilled') blackboxHeld = tags.value.filter((t) => t.kind === BLACKBOX_TAG_KIND).length;
    })();
    return () => mine.abort();
  });
  const choose = (what: AddChoice) => { if (!busy) onChoose?.(what); };
</script>

<section class="src add-data" aria-labelledby={headId}>
  {#if drawer}
    <div class="head"><h2 id={headId}>{ADD_TEXT.heading}</h2></div>
  {:else}
    <div class="nutshell"><b>{NUTSHELL}</b><p>{ADD_TEXT.lead}</p></div>
    <h3 id={headId}>{ADD_TEXT.heading}</h3>
  {/if}
  {#if readOnly}<p class="hint" data-read-only>{HUB_TEXT.readOnlyWhy}</p>{/if}
  {#if busy}<p class="hint" data-busy role="status">{ADD_TEXT.busy}</p>{/if}
  <div class="cards">
    <div class="card" {...mark('sql')}>
      <h4>{ADD_TEXT.sqlTitle}</h4>
      <p class="card-text">{ADD_TEXT.sqlBody}</p>
      <div class="chipline">
        <span class="pill" data-tone={sqlSaved > 0 ? 'good' : 'none'}>{ADD_TEXT.connReady(sqlSaved)}</span>
        {#if sqlHeld > 0}<span class="pill" data-tone="warn">{ADD_TEXT.held(sqlHeld)}</span>{/if}
      </div>
      <div class="actions"><button type="button" class="btn" disabled={off} onclick={() => choose('sql')}>{ADD_TEXT.addSql}</button></div>
    </div>
    <div class="card" {...mark('mongo')}>
      <h4>{ADD_TEXT.mongoTitle}</h4>
      <p class="card-text">{ADD_TEXT.mongoBody}</p>
      <div class="chipline">
        <span class="pill" data-tone={mongoSaved > 0 ? 'good' : 'none'}>{ADD_TEXT.connReady(mongoSaved)}</span>
        {#if mongoHeld > 0}<span class="pill" data-tone="warn">{ADD_TEXT.held(mongoHeld)}</span>{/if}
      </div>
      <div class="actions"><button type="button" class="btn" disabled={off} onclick={() => choose('mongo')}>{ADD_TEXT.addMongo}</button></div>
    </div>
    <div class="card" {...mark('blackbox')}>
      <h4>{ADD_TEXT.blackboxTitle}</h4>
      <p class="card-text">{ADD_TEXT.blackboxBody}</p>
      {#if blackboxHeld > 0}<div class="chipline"><span class="pill" data-tone="warn">{ADD_TEXT.held(blackboxHeld)}</span></div>{/if}
      <div class="actions"><button type="button" class="btn" disabled={off} onclick={() => choose('blackbox')}>{ADD_TEXT.addBlackbox}</button></div>
    </div>
    <div class="card" {...mark('files')}>
      <h4>{ADD_TEXT.filesTitle}</h4>
      <p class="card-text">{ADD_TEXT.filesBody}</p>
      <div class="chipline">
        {#if drawer && staged > 0}<span class="pill" data-tone="warn" data-staged>{ADD_TEXT.staged(staged)}</span>
        {:else if filesHeld > 0}<span class="pill" data-tone="warn">{ADD_TEXT.held(filesHeld)}</span>
        {:else}<span class="pill" data-tone="none">{ADD_TEXT.none}</span>{/if}
      </div>
      <div class="actions"><button type="button" class="btn" disabled={off || !files} onclick={() => choose('files')}>{ADD_TEXT.addFiles}</button></div>
    </div>
    <div class="card" {...mark('day')}>
      <h4>{ADD_TEXT.dayTitle}</h4>
      <p class="card-text">{ADD_TEXT.dayBody}</p>
      <div class="chipline"><span class="pill" data-tone={byDay > 0 ? 'good' : 'none'}>{ADD_TEXT.byDay(byDay, sources.length)}</span></div>
      <div class="actions"><button type="button" class="btn" disabled={off || byDay === 0} onclick={() => choose('day')}>{ADD_TEXT.addDay}</button></div>
    </div>
  </div>
  {#if drawer}<p class="hint">{ADD_TEXT.drawerLead}</p>{/if}
  <p class="hint">{ADD_TEXT.outOfDate}</p>
  <p class="hint">{ADD_TEXT.readsOnly}</p>
</section>
