<script lang="ts">
  import type { RecordDetail } from '../lib/types';
  import PayloadViewer from './PayloadViewer.svelte';
  import PlainLine from './PlainLine.svelte';
  import { plainAuditCreateSummary } from '../lib/copy/plainSummary';
  import { formatTime, zoneAbbreviation } from '../lib/time';
  import { timeZone } from '../lib/timeZone.svelte';
  let { detail, sourceLabel, onClose }: { detail: RecordDetail | null; sourceLabel: string; onClose: () => void } = $props();
  let panel: HTMLElement | undefined = $state();
  // Move focus to the panel when a record opens so keyboard and screen-reader users land on it.
  $effect(() => { if (detail && panel) panel.focus(); });
  const NONE = 'not recorded';
  const text = (value: string | undefined | null) => value === undefined || value === null || value === '' ? NONE : value;
  // Row = [field name, shown value, optional hover text]. Times show milliseconds (these records can be a few ms apart); the zone is named once in the table caption, and the stored ISO text is kept as hover text.
  type Row = [string, string, string?];
  const stamp = (name: string, value: string | undefined | null): Row => [name, value ? formatTime(value, { zone: timeZone.zone, precision: 'ms', suffix: false, fallback: value }) : NONE, value ?? undefined];
  const zoneName = $derived(zoneAbbreviation(timeZone.zone, detail?.initiatedAt ?? Date.now()));
  const ms = (value: number | undefined | null) => value == null || !Number.isFinite(value) ? NONE : value + ' ms';
  const kb = (value: number | undefined | null) => value == null || !Number.isFinite(value) ? NONE : value + ' KB';
  let fields = $derived<Row[]>(detail ? [
    ['_id', detail.id], ['Source', sourceLabel],
    ['ControllerName', text(detail.controller)], ['ActionName', text(detail.action)], ['RequestMainUrl', text(detail.url)],
    ['UserId', detail.userId == null ? NONE : String(detail.userId)], ['UserIp', text(detail.userIp)], ['UserAgent', text(detail.userAgent)],
    ['PayrollUIVersion', text(detail.uiVersion)],
    ['RequestPollingType', text(detail.pollingTypeRaw)], ['StatusCode', text(detail.status)],
    ['RequestLengthInKiloBytes', kb(detail.requestKB)], ['ResponseLengthInKiloBytes', kb(detail.responseKB)],
    stamp('RequestInitiatedAt', detail.initiatedAt), stamp('CreatedOn', detail.createdOn),
    stamp('RequestCompletedAt', detail.completedAt), stamp('UpdatedOn', detail.updatedOn),
    ['AuditCreateTime', ms(detail.auditCreateTimeMs)], ['ApiTime', ms(detail.apiTimeMs)], ['PayloadRecordTime', ms(detail.payloadRecordTimeMs)],
  ] : []);
  // The saving time in plain words. It states what was measured and names no cause. AuditCreateTime is CreatedOn minus RequestInitiatedAt, so that is the window; with either end missing no window is stated.
  let plain = $derived(detail ? plainAuditCreateSummary({ auditCreateTimeMs: detail.auditCreateTimeMs, window: detail.initiatedAt && detail.createdOn ? { start: detail.initiatedAt, end: detail.createdOn } : null }) : null);
  let evidence = $derived<Row[]>(detail ? [
    ['Kind', text(detail.kind)], ['State', text(detail.state) + (detail.pendingAsync ? ' (accepted, still pending)' : '')],
    ['Status class', text(detail.outcome)], ['Observed time', ms(detail.durationMs)],
  ] : []);
</script>

{#if detail}
  <section class="record-panel" aria-label="Record details" tabindex="-1" bind:this={panel}>
    <div class="payload-head">
      <div><h3>Record {detail.id}</h3><p class="record-note">Every field stored for this audit record. Payloads are limited by the server.</p></div>
      <button class="quiet-button" onclick={onClose} aria-label="Close record details">Close</button>
    </div>
    {#if plain}<PlainLine result={plain} hints={['audit-create-time']} />{/if}
    <div class="record-tables">
      <table class="record-table" aria-label="Stored fields">
        <caption>Stored fields · times in {zoneName}</caption>
        <tbody>{#each fields as [name, value, hover]}<tr><th scope="row">{name}</th><td class:record-none={value === NONE} title={hover}>{value}</td></tr>{/each}</tbody>
      </table>
      <table class="record-table" aria-label="Derived values">
        <caption>Derived by Unravis</caption>
        <tbody>{#each evidence as [name, value]}<tr><th scope="row">{name}</th><td class:record-none={value === NONE}>{value}</td></tr>{/each}</tbody>
      </table>
    </div>
    <PayloadViewer payload={detail.payload} embedded />
  </section>
{/if}
