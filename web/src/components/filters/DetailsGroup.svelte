<script lang="ts">
  import { untrack } from 'svelte';
  import type { Query } from '../../lib/types';
  import { MAX_FILTER_VALUES, MAX_USER_IDS, isValidUserId, itemsOverLimit, splitList, syncListDraft } from '../../lib/investigation';
  import FilterGroup from './FilterGroup.svelte';
  // The Request details group of the More filters panel: the record fields (users, versions, addresses, status codes, browser, URL, audit ID).
  // The query only ever holds a filter that is in use (an emptied field becomes undefined), and the comma-separated text boxes keep their own
  // draft so that typing "1, " is not rewritten under the cursor. The panel never starts a run, so nothing here calls onScopeChange.
  let { query = $bindable(), count = 0 }: { query: Query; count?: number } = $props();
  const AUDIT_ID_PATTERN = /^[0-9a-fA-F]{24}$/;
  const joinList = (items: readonly (string | number)[] | undefined) => (items ?? []).join(', ');
  const initialText = untrack(() => ({ userIds: joinList(query.userIds), uiVersions: joinList(query.uiVersions), userIps: joinList(query.userIps), statusCodes: joinList(query.statusCodes) }));
  let userIdsText = $state(initialText.userIds);
  let uiVersionsText = $state(initialText.uiVersions);
  let userIpsText = $state(initialText.userIps);
  let statusCodesText = $state(initialText.statusCodes);
  const userIdOk = (item: string) => /^\d{1,10}$/.test(item) && isValidUserId(Number(item));
  // The query can be replaced after this component mounts (a restored or saved investigation), so each draft follows the query. A draft that
  // still parses to the applied values is left alone, which keeps partial typing intact.
  $effect(() => {
    const applied = { userIds: query.userIds, uiVersions: query.uiVersions, userIps: query.userIps, statusCodes: query.statusCodes };
    untrack(() => {
      userIdsText = syncListDraft(userIdsText, applied.userIds, userIdOk, MAX_USER_IDS);
      uiVersionsText = syncListDraft(uiVersionsText, applied.uiVersions, undefined, MAX_FILTER_VALUES);
      userIpsText = syncListDraft(userIpsText, applied.userIps, undefined, MAX_FILTER_VALUES);
      statusCodesText = syncListDraft(statusCodesText, applied.statusCodes, undefined, MAX_FILTER_VALUES);
    });
  });
  const rejectedUserIds = $derived(splitList(userIdsText).filter(item => !userIdOk(item)));
  const auditIdInvalid = $derived(!!query.auditId && !AUDIT_ID_PATTERN.test(query.auditId));
  const setUserIds = (text: string) => {
    userIdsText = text;
    const ids = [...new Set(splitList(text).filter(userIdOk).map(Number))].slice(0, MAX_USER_IDS);
    query.userIds = ids.length ? ids : undefined;
  };
  // The server refuses a longer list, so only the first values are applied and the box says so.
  const overIds = $derived(itemsOverLimit(userIdsText, MAX_USER_IDS));
  const overVersions = $derived(itemsOverLimit(uiVersionsText, MAX_FILTER_VALUES));
  const overIps = $derived(itemsOverLimit(userIpsText, MAX_FILTER_VALUES));
  const overStatus = $derived(itemsOverLimit(statusCodesText, MAX_FILTER_VALUES));
  const overText = (over: number, max: number) => `Only the first ${max} values are used. ${over} more ${over === 1 ? 'was' : 'were'} left out.`;
  const describedBy = (...ids: (string | false)[]) => ids.filter(Boolean).join(' ') || undefined;
  const setList = (key: 'uiVersions' | 'userIps' | 'statusCodes', text: string) => {
    if (key === 'uiVersions') uiVersionsText = text; else if (key === 'userIps') userIpsText = text; else statusCodesText = text;
    const items = splitList(text).slice(0, MAX_FILTER_VALUES);
    query[key] = items.length ? items : undefined;
  };
  const setText = (key: 'userAgentContains' | 'urlContains' | 'auditId', value: string) => {
    query[key] = value.trim() ? value.trim() : undefined;
  };
</script>

<FilterGroup id="details" title="Request details" {count}>
  {#snippet lede()}Narrow to records that match all of these. Separate several values with commas; leave a box empty to ignore it.{/snippet}
  <div class="field-grid">
    <div class="control-group">
      <label for="rf-user-ids">User IDs</label>
      <input id="rf-user-ids" inputmode="numeric" autocomplete="off" aria-invalid={rejectedUserIds.length > 0} aria-describedby={describedBy(rejectedUserIds.length > 0 && 'rf-user-ids-error', overIds > 0 && 'rf-user-ids-over')} value={userIdsText} oninput={(event) => setUserIds(event.currentTarget.value)} placeholder="e.g. 1042, 1043" />
      {#if rejectedUserIds.length}<p class="inline-error" id="rf-user-ids-error" role="alert">Ignored, not whole numbers from 1 to 2147483647: {rejectedUserIds.join(', ')}.</p>{/if}
      {#if overIds}<p class="inline-error" id="rf-user-ids-over" role="alert" data-testid="over-user-ids">{overText(overIds, MAX_USER_IDS)}</p>{/if}
    </div>
    <div class="control-group">
      <label for="rf-ui-versions">UI versions<span class="label-aside">exact match</span></label>
      <input id="rf-ui-versions" autocomplete="off" aria-describedby={describedBy(overVersions > 0 && 'rf-ui-versions-over')} value={uiVersionsText} oninput={(event) => setList('uiVersions', event.currentTarget.value)} placeholder="e.g. 2.14.0" />
      {#if overVersions}<p class="inline-error" id="rf-ui-versions-over" role="alert" data-testid="over-ui-versions">{overText(overVersions, MAX_FILTER_VALUES)}</p>{/if}
    </div>
    <div class="control-group">
      <label for="rf-user-ips">User IPs<span class="label-aside">exact match</span></label>
      <input id="rf-user-ips" autocomplete="off" aria-describedby={describedBy(overIps > 0 && 'rf-user-ips-over')} value={userIpsText} oninput={(event) => setList('userIps', event.currentTarget.value)} placeholder="e.g. 203.0.113.7" />
      {#if overIps}<p class="inline-error" id="rf-user-ips-over" role="alert" data-testid="over-user-ips">{overText(overIps, MAX_FILTER_VALUES)}</p>{/if}
    </div>
    <div class="control-group">
      <label for="rf-status-codes">Status codes</label>
      <input id="rf-status-codes" autocomplete="off" aria-describedby={describedBy(overStatus > 0 && 'rf-status-codes-over')} value={statusCodesText} oninput={(event) => setList('statusCodes', event.currentTarget.value)} placeholder="e.g. 404, 500" />
      {#if overStatus}<p class="inline-error" id="rf-status-codes-over" role="alert" data-testid="over-status-codes">{overText(overStatus, MAX_FILTER_VALUES)}</p>{/if}
    </div>
    <div class="control-group">
      <label for="rf-agent">Browser (user agent) contains<span class="label-aside">not case sensitive</span></label>
      <input id="rf-agent" maxlength="200" autocomplete="off" value={query.userAgentContains ?? ''} oninput={(event) => setText('userAgentContains', event.currentTarget.value)} placeholder="e.g. Firefox" />
    </div>
    <div class="control-group">
      <label for="rf-url">Request URL contains<span class="label-aside">not case sensitive</span></label>
      <input id="rf-url" maxlength="200" autocomplete="off" value={query.urlContains ?? ''} oninput={(event) => setText('urlContains', event.currentTarget.value)} placeholder="e.g. /api/payslips" />
    </div>
    <div class="control-group wide">
      <label for="rf-audit-id">Audit ID</label>
      <input id="rf-audit-id" maxlength="24" autocomplete="off" spellcheck="false" aria-invalid={auditIdInvalid} aria-describedby={auditIdInvalid ? 'rf-audit-id-error' : undefined} value={query.auditId ?? ''} oninput={(event) => setText('auditId', event.currentTarget.value)} placeholder="24 hexadecimal characters" />
      {#if auditIdInvalid}<p class="inline-error" id="rf-audit-id-error" role="alert">An audit ID is exactly 24 hexadecimal characters.</p>{/if}
    </div>
  </div>
</FilterGroup>

<style>
  input[aria-invalid='true'] { border-color: var(--bad); }
  .field-grid > .wide { grid-column: 1 / -1; }
</style>
