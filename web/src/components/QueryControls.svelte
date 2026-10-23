<script lang="ts">
  import { untrack } from 'svelte';
  import type { Completion, Query } from '../lib/types';
  import { bucketForWindows, fromLocalParts, quickRange, quickRanges, toLocalDatePart, toLocalTimePart, zones, type QuickRangeId } from '../lib/time';
  import { metricLabels } from '../lib/presentation';
  import { isValidOperation, isValidUserId, splitList, syncListDraft, syncOperationDraft } from '../lib/investigation';
  import DateTimeField from './DateTimeField.svelte';
  let { query = $bindable(), zone = $bindable(), targetMs = $bindable(), dateInputsValid = $bindable(), groupNames = [], now = '' }: { query: Query; zone: string; targetMs: number | null; dateInputsValid: boolean; groupNames?: string[]; now?: string } = $props();
  let timeInputError = $state('');
  let invalidWindows = $state<string[]>([]);
  $effect(() => { dateInputsValid = invalidWindows.length === 0; });
  // Record filters. The query only ever holds a filter that is in use (an
  // emptied field becomes undefined), and the comma-separated text boxes keep
  // their own draft so that typing "1, " is not rewritten under the cursor.
  const MAX_OPERATION_SUGGESTIONS = 500;
  const AUDIT_ID_PATTERN = /^[0-9a-fA-F]{24}$/;
  const joinList = (items: readonly (string | number)[] | undefined) => (items ?? []).join(', ');
  const initialText = untrack(() => ({ userIds: joinList(query.userIds), uiVersions: joinList(query.uiVersions), userIps: joinList(query.userIps), statusCodes: joinList(query.statusCodes), operation: query.operation ?? '' }));
  let userIdsText = $state(initialText.userIds);
  let uiVersionsText = $state(initialText.uiVersions);
  let userIpsText = $state(initialText.userIps);
  let statusCodesText = $state(initialText.statusCodes);
  let operationText = $state(initialText.operation);
  const userIdOk = (item: string) => /^\d{1,10}$/.test(item) && isValidUserId(Number(item));
  // The query can be replaced after this component mounts (a restored or saved
  // investigation), so each draft follows the query. A draft that still parses to
  // the applied values is left alone, which keeps partial typing intact.
  $effect(() => {
    const applied = { userIds: query.userIds, uiVersions: query.uiVersions, userIps: query.userIps, statusCodes: query.statusCodes, operation: query.operation };
    untrack(() => {
      userIdsText = syncListDraft(userIdsText, applied.userIds, userIdOk);
      uiVersionsText = syncListDraft(uiVersionsText, applied.uiVersions);
      userIpsText = syncListDraft(userIpsText, applied.userIps);
      statusCodesText = syncListDraft(statusCodesText, applied.statusCodes);
      operationText = syncOperationDraft(operationText, applied.operation);
    });
  });
  const rejectedUserIds = $derived(splitList(userIdsText).filter(item => !userIdOk(item)));
  const auditIdInvalid = $derived(!!query.auditId && !AUDIT_ID_PATTERN.test(query.auditId));
  const operationInvalid = $derived(!!operationText.trim() && !isValidOperation(operationText));
  const operationSuggestions = $derived([...new Set(groupNames)].slice(0, MAX_OPERATION_SUGGESTIONS));
  const setUserIds = (text: string) => {
    userIdsText = text;
    const ids = splitList(text).filter(userIdOk).map(Number);
    query.userIds = ids.length ? [...new Set(ids)] : undefined;
  };
  // An operation without a dot, or with nothing on one side of it, is shown as a
  // hint and not applied, so the query never carries a value the server rejects.
  const setOperation = (value: string) => {
    operationText = value;
    query.operation = value.trim() && isValidOperation(value) ? value.trim() : undefined;
  };
  const setList = (key: 'uiVersions' | 'userIps' | 'statusCodes', text: string) => {
    if (key === 'uiVersions') uiVersionsText = text; else if (key === 'userIps') userIpsText = text; else statusCodesText = text;
    const items = splitList(text);
    query[key] = items.length ? items : undefined;
  };
  const setText = (key: 'userAgentContains' | 'urlContains' | 'auditId', value: string) => {
    query[key] = value.trim() ? value.trim() : undefined;
  };
  const setCompletion = (value: string) => {
    query.completion = value && value !== 'all' ? (value as Completion) : undefined;
  };
  type Chip = { id: string; label: string; remove: () => void };
  const chips = $derived.by((): Chip[] => {
    const out: Chip[] = [];
    for (const id of query.userIds ?? []) out.push({ id: `user-${id}`, label: `User ID ${id}`, remove: () => setUserIds(joinList((query.userIds ?? []).filter(item => item !== id))) });
    for (const value of query.uiVersions ?? []) out.push({ id: `version-${value}`, label: `UI version ${value}`, remove: () => setList('uiVersions', joinList((query.uiVersions ?? []).filter(item => item !== value))) });
    if (query.userAgentContains) out.push({ id: 'agent', label: `Browser contains “${query.userAgentContains}”`, remove: () => setText('userAgentContains', '') });
    for (const value of query.userIps ?? []) out.push({ id: `ip-${value}`, label: `IP ${value}`, remove: () => setList('userIps', joinList((query.userIps ?? []).filter(item => item !== value))) });
    if (query.urlContains) out.push({ id: 'url', label: `URL contains “${query.urlContains}”`, remove: () => setText('urlContains', '') });
    if (query.auditId) out.push({ id: 'audit-id', label: `Audit ID ${query.auditId}`, remove: () => setText('auditId', '') });
    for (const value of query.statusCodes ?? []) out.push({ id: `status-${value}`, label: `Status ${value}`, remove: () => setList('statusCodes', joinList((query.statusCodes ?? []).filter(item => item !== value))) });
    if (query.completion && query.completion !== 'all') out.push({ id: 'completion', label: `Completion: ${completionLabels[query.completion]}`, remove: () => setCompletion('') });
    if (query.operation) out.push({ id: 'operation', label: `Operation ${query.operation}`, remove: () => setOperation('') });
    return out;
  });
  const clearRecordFilters = () => {
    userIdsText = ''; uiVersionsText = ''; userIpsText = ''; statusCodesText = ''; operationText = '';
    query.userIds = undefined; query.uiVersions = undefined; query.userAgentContains = undefined; query.userIps = undefined;
    query.urlContains = undefined; query.auditId = undefined; query.statusCodes = undefined; query.completion = undefined; query.operation = undefined;
  };
  const completionLabels: Record<Completion, string> = { all: 'Any', complete: 'Complete', incomplete: 'Incomplete', pending: 'Pending (accepted, not finished)', invalid: 'Invalid timing' };
  const browserZone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'local time';
  const localValue = (value: string) => {
    const date = new Date(value);
    return `${toLocalDatePart(date)}T${toLocalTimePart(date)}`;
  };
  const setWindow = (field: 'start' | 'end' | 'baselineStart' | 'baselineEnd', datePart: string, timePart: string) => {
    if (!datePart || !timePart) {
      invalidWindows = invalidWindows.includes(field) ? invalidWindows : [...invalidWindows, field];
      timeInputError = 'Enter both a date and time for every range. Choose another time if the clocks change made that local value invalid.';
      return;
    }
    try {
      query[field] = fromLocalParts(datePart, timePart);
      query.bucketSeconds = bucketForWindows(query.start, query.end, query.baselineStart, query.baselineEnd);
      invalidWindows = invalidWindows.filter(item => item !== field);
      timeInputError = invalidWindows.length ? 'Complete every date and time with a valid local value before running the comparison.' : '';
    } catch (error) {
      invalidWindows = invalidWindows.includes(field) ? invalidWindows : [...invalidWindows, field];
      timeInputError = error instanceof Error ? error.message : 'Choose a valid local date and time.';
    }
  };
  // A quick range ends now (the server's clock) and compares with the equal-length period right before it.
  const applyQuickRange = (id: QuickRangeId) => {
    const base = new Date(now);
    Object.assign(query, quickRange(id, Number.isFinite(base.getTime()) ? base : new Date()));
    invalidWindows = []; timeInputError = '';
  };
</script>

<section class="query-controls" aria-label="Comparison settings">
  <div class="quick-ranges" role="group" aria-label="Quick ranges">
    <span>Quick range</span>
    {#each quickRanges as preset (preset.id)}<button type="button" class="secondary-button" title={'Investigate the ' + preset.label.toLowerCase() + ' and compare with the period right before it.'} onclick={() => applyQuickRange(preset.id)}>{preset.label}</button>{/each}
  </div>
  <div class="query-ranges">
    <section class="range-group" aria-labelledby="current-range-label">
      <div class="range-title"><h2 id="current-range-label">Investigate</h2><p>Requests in the period you want to inspect.</p></div>
      <DateTimeField id="current-start" label="From" value={localValue(query.start)} onChange={(date, time) => setWindow('start', date, time)} />
      <DateTimeField id="current-end" label="To" value={localValue(query.end)} onChange={(date, time) => setWindow('end', date, time)} />
    </section>
    <section class="range-group reference-range" aria-labelledby="reference-range-label">
      <div class="range-title"><h2 id="reference-range-label">Reference</h2><p>An earlier period to compare with.</p></div>
      <DateTimeField id="reference-start" label="From" value={localValue(query.baselineStart)} onChange={(date, time) => setWindow('baselineStart', date, time)} />
      <DateTimeField id="reference-end" label="To" value={localValue(query.baselineEnd)} onChange={(date, time) => setWindow('baselineEnd', date, time)} />
    </section>
  </div>
  <div class="query-footer">
    <p class="browser-time-note">Times use your browser time zone: <strong>{browserZone}</strong>.</p>
    {#if timeInputError}<p class="inline-error" role="alert">{timeInputError}</p>{/if}
    <details class="exact-times"><summary>Exact UTC times</summary><dl><dt>Current from</dt><dd>{query.start}</dd><dt>Current to</dt><dd>{query.end}</dd><dt>Reference from</dt><dd>{query.baselineStart}</dd><dt>Reference to</dt><dd>{query.baselineEnd}</dd></dl></details>
    <details class="advanced-filters">
      <summary>Filters and chart settings</summary>
      <div class="advanced-content">
        <div class="field-grid">
          <div class="control-group"><label for="kind">Request type</label><select id="kind" bind:value={query.kind}><option value="synchronous">Synchronous</option><option value="asynchronous">Asynchronous</option><option value="hangfire">Hangfire</option><option value="http">HTTP (synchronous, asynchronous and polling type unknown)</option><option value="http_unknown">HTTP (polling type unknown)</option><option value="all">All types</option></select></div>
          <div class="control-group"><label for="outcome">Status</label><select id="outcome" bind:value={query.outcome}><option value="all">All statuses</option><option value="success">Success</option><option value="failure">Failure</option><option value="client_error">Client errors (4xx)</option><option value="server_error">Server errors (5xx, incl. failed Hangfire jobs)</option></select></div>
          <div class="control-group"><label for="metric">Time measured</label><select id="metric" bind:value={query.metric}><option value="total">{metricLabels.total}</option><option value="api">{metricLabels.api}</option></select></div>
          <div class="control-group"><label for="bucket">Chart interval (seconds)</label><input id="bucket" type="number" min="10" max="86400" step="1" bind:value={query.bucketSeconds} /><p class="field-note">10–86,400 seconds; no more than 120 points.</p></div>
        </div>
        <div class="control-group"><label for="latency-target">Optional response-time target (ms)</label><input id="latency-target" type="number" min="0.01" step="any" bind:value={targetMs} placeholder="No target" /><p class="field-note">Adds a chart line and compares it with P95: the estimated time within which about 95 of 100 requests with usable timings finish.</p></div>
        <div class="field-grid"><div class="control-group"><label for="controller">Controller name</label><input id="controller" bind:value={query.controller} placeholder="Any controller" /></div><div class="control-group"><label for="action">Action name</label><input id="action" bind:value={query.action} placeholder="Any action" /></div></div>
        <div class="control-group"><label for="display-zone">Chart labels time zone</label><select id="display-zone" bind:value={zone}>{#each zones as item}<option value={item}>{item}</option>{/each}</select><p class="field-note">Changes chart labels only; it does not change either selected period.</p></div>
      </div>
    </details>
    <details class="advanced-filters record-filters">
      <summary>Record filters{#if chips.length} <span class="record-count" aria-label="{chips.length} record filters in use">{chips.length}</span>{/if}</summary>
      <div class="advanced-content">
        <p class="field-note">Narrow every number, chart and request list to the audit records that match all of these. Leave a box empty to ignore it. Separate several values with commas.</p>
        {#if chips.length}
          <ul class="record-chips" aria-label="Record filters in use">
            {#each chips as chip (chip.id)}
              <li class="record-chip"><span title={chip.label}>{chip.label}</span><button type="button" aria-label="Remove filter: {chip.label}" onclick={chip.remove}>×</button></li>
            {/each}
          </ul>
          <button type="button" class="record-clear" onclick={clearRecordFilters}>Clear all record filters</button>
        {/if}
        <div class="field-grid">
          <div class="control-group"><label for="rf-user-ids">User IDs</label><input id="rf-user-ids" inputmode="numeric" autocomplete="off" aria-invalid={rejectedUserIds.length > 0} value={userIdsText} oninput={(event) => setUserIds(event.currentTarget.value)} placeholder="e.g. 1042, 1043" />{#if rejectedUserIds.length}<p class="inline-error" role="alert">Ignored, not whole numbers from 1 to 2147483647: {rejectedUserIds.join(', ')}.</p>{/if}</div>
          <div class="control-group"><label for="rf-ui-versions">UI versions</label><input id="rf-ui-versions" autocomplete="off" value={uiVersionsText} oninput={(event) => setList('uiVersions', event.currentTarget.value)} placeholder="e.g. 2.14.0" /><p class="field-note">Exact match.</p></div>
          <div class="control-group"><label for="rf-user-ips">User IPs</label><input id="rf-user-ips" autocomplete="off" value={userIpsText} oninput={(event) => setList('userIps', event.currentTarget.value)} placeholder="e.g. 203.0.113.7" /><p class="field-note">Exact match.</p></div>
          <div class="control-group"><label for="rf-status-codes">Status codes</label><input id="rf-status-codes" autocomplete="off" value={statusCodesText} oninput={(event) => setList('statusCodes', event.currentTarget.value)} placeholder="e.g. 404, 500" /></div>
          <div class="control-group"><label for="rf-agent">Browser (user agent) contains</label><input id="rf-agent" maxlength="200" autocomplete="off" value={query.userAgentContains ?? ''} oninput={(event) => setText('userAgentContains', event.currentTarget.value)} placeholder="e.g. Firefox" /><p class="field-note">Not case sensitive.</p></div>
          <div class="control-group"><label for="rf-url">Request URL contains</label><input id="rf-url" maxlength="200" autocomplete="off" value={query.urlContains ?? ''} oninput={(event) => setText('urlContains', event.currentTarget.value)} placeholder="e.g. /api/payslips" /><p class="field-note">Not case sensitive.</p></div>
          <div class="control-group"><label for="rf-audit-id">Audit ID</label><input id="rf-audit-id" maxlength="24" autocomplete="off" spellcheck="false" aria-invalid={auditIdInvalid} value={query.auditId ?? ''} oninput={(event) => setText('auditId', event.currentTarget.value)} placeholder="24 hexadecimal characters" />{#if auditIdInvalid}<p class="inline-error" role="alert">An audit ID is exactly 24 hexadecimal characters.</p>{/if}</div>
          <div class="control-group"><label for="rf-completion">Completion</label><select id="rf-completion" value={query.completion ?? ''} onchange={(event) => setCompletion(event.currentTarget.value)}><option value="">Any</option>{#each ['complete', 'incomplete', 'pending', 'invalid'] as value}<option value={value}>{completionLabels[value as Completion]}</option>{/each}</select></div>
        </div>
        <div class="control-group"><label for="rf-operation">Operation</label><input id="rf-operation" list="rf-operation-options" maxlength="200" autocomplete="off" aria-invalid={operationInvalid} value={operationText} oninput={(event) => setOperation(event.currentTarget.value)} placeholder="Controller.Action" />{#if operationInvalid}<p class="inline-error" role="alert">Not applied. Write the operation as Controller.Action, with a name on both sides of the dot.</p>{/if}<datalist id="rf-operation-options">{#each operationSuggestions as name (name)}<option value={name}></option>{/each}</datalist><p class="field-note">Suggestions come from the API groups of the loaded results. It is the controller before the first dot and the action after it.</p></div>
      </div>
    </details>
  </div>
</section>

<style>
  .record-count { display: inline-block; min-width: 18px; padding: 0 5px; border-radius: 999px; background: var(--cobalt); color: var(--paper); font-size: 11px; line-height: 18px; text-align: center; }
  .record-chips { display: flex; flex-wrap: wrap; gap: 6px; margin: 6px 0 0; padding: 0; list-style: none; }
  .record-chip { display: inline-flex; align-items: center; gap: 2px; max-width: 100%; padding: 1px 3px 1px 9px; border: 1px solid var(--line); border-radius: 999px; background: var(--soft-line); color: var(--ink); font-size: 12px; }
  .record-chip span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .record-chip button { flex: none; border: 0; background: transparent; color: var(--muted); cursor: pointer; font-size: 15px; line-height: 1; padding: 2px 6px; border-radius: 999px; }
  .record-chip button:hover, .record-chip button:focus-visible { color: var(--danger); }
  .record-clear { justify-self: start; margin-top: 4px; border: 0; background: transparent; color: var(--cobalt); cursor: pointer; font-size: 12px; font-weight: 600; padding: 2px 0; }
  .record-clear:hover { text-decoration: underline; }
  input[aria-invalid='true'] { border-color: var(--danger); }
</style>
