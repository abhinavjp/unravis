<script lang="ts">
  import { NOTHING_TEXT } from '../../lib/copy/sourcesCopy';
  import './sources.css';

  // Screen 115: every source answered, but the hours held nothing to read. The case is not broken. This says so, names the likely reason, and
  // lists what the case can and cannot answer, each as Cannot test yet with its reason. It never says a check passed.
  let { kind = 'sql', onAddDay, onAddData }: { kind?: 'sql' | 'mongo'; onAddDay?: () => void; onAddData?: () => void } = $props();
</script>

<section class="src panel" aria-labelledby="nothing-head" data-nothing-read>
  <h4 id="nothing-head">{NOTHING_TEXT.heading}</h4>
  <p class="lead">{NOTHING_TEXT.lead}</p>
  <p>{kind === 'sql' ? NOTHING_TEXT.reasonSql : NOTHING_TEXT.reasonMongo}</p>
  <p class="hint">{NOTHING_TEXT.hours}</p>
  <p class="hint">{NOTHING_TEXT.change}</p>
  <div class="table-wrap">
    <table class="stack">
      <caption class="hint">{NOTHING_TEXT.tableHeading}</caption>
      <thead><tr><th scope="col">{NOTHING_TEXT.colQuestion}</th><th scope="col">{NOTHING_TEXT.colNeeds}</th><th scope="col">{NOTHING_TEXT.colCan}</th></tr></thead>
      <tbody>
        <tr><td data-label={NOTHING_TEXT.colQuestion}>{NOTHING_TEXT.qSlower}</td><td data-label={NOTHING_TEXT.colNeeds}>{NOTHING_TEXT.needAudit}</td><td data-label={NOTHING_TEXT.colCan}>{NOTHING_TEXT.cannotNoRecords}</td></tr>
        <tr><td data-label={NOTHING_TEXT.colQuestion}>{NOTHING_TEXT.qBlocking}</td><td data-label={NOTHING_TEXT.colNeeds}>{NOTHING_TEXT.needSql}</td><td data-label={NOTHING_TEXT.colCan}>{NOTHING_TEXT.cannotNoWindow}</td></tr>
      </tbody>
    </table>
  </div>
  <div class="actions">
    {#if onAddDay}<button type="button" class="btn" onclick={() => onAddDay?.()}>{NOTHING_TEXT.addDay}</button>{/if}
    {#if onAddData}<button type="button" class="btn" onclick={() => onAddData?.()}>{NOTHING_TEXT.addData}</button>{/if}
  </div>
</section>
