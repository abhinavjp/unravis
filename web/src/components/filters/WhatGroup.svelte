<script lang="ts">
  import { untrack } from 'svelte';
  import type { Completion, Query } from '../../lib/types';
  import { unknownKindToggle } from '../../lib/depth';
  import { isValidOperation, syncOperationDraft } from '../../lib/investigation';
  import FilterGroup from './FilterGroup.svelte';
  import Hint from '../Hint.svelte';
  // The What group of the More filters panel: which requests, by outcome, completion and name. It edits the shared query only; the panel
  // never starts a run on its own.
  let { query = $bindable(), groupNames = [], count = 0 }: { query: Query; groupNames?: string[]; count?: number } = $props();
  const MAX_OPERATION_SUGGESTIONS = 500;
  const completionLabels: Record<Completion, string> = { all: 'Any', complete: 'Complete', incomplete: 'Incomplete', pending: 'Pending (accepted, not finished)', invalid: 'Unusable timing' };
  // The operation box keeps its own draft, so that a half-typed name is not rewritten under the cursor.
  let operationText = $state(untrack(() => query.operation ?? ''));
  // The query can be replaced after this component mounts (a restored or saved investigation), so the draft follows it.
  $effect(() => {
    const applied = query.operation;
    untrack(() => { operationText = syncOperationDraft(operationText, applied); });
  });
  const operationInvalid = $derived(!!operationText.trim() && !isValidOperation(operationText));
  const operationSuggestions = $derived([...new Set(groupNames)].slice(0, MAX_OPERATION_SUGGESTIONS));
  // An operation without a dot, or with nothing on one side of it, is shown as a hint and not applied, so the query never carries a value the server rejects.
  const setOperation = (value: string) => {
    operationText = value;
    query.operation = value.trim() && isValidOperation(value) ? value.trim() : undefined;
  };
  const setCompletion = (value: string) => {
    query.completion = value && value !== 'all' ? (value as Completion) : undefined;
  };
</script>

<FilterGroup id="what" title="What" {count}>
  <div class="field-grid">
    <div class="control-group"><span class="label-line"><label for="outcome">Status</label><Hint termId="status-class" /></span><select id="outcome" bind:value={query.outcome}><option value="all">All statuses</option><option value="success">Success</option><option value="failure">Failure</option><option value="client_error">Client errors (4xx)</option><option value="server_error">Server errors (5xx, incl. failed Hangfire jobs)</option></select></div>
    <div class="control-group"><span class="label-line"><label for="rf-completion">Completion</label><Hint termId="incomplete-request" /></span><select id="rf-completion" value={query.completion ?? ''} onchange={(event) => setCompletion(event.currentTarget.value)}><option value="">Any</option>{#each ['complete', 'incomplete', 'pending', 'invalid'] as value}<option value={value}>{completionLabels[value as Completion]}</option>{/each}</select></div>
    <div class="control-group"><label for="controller">Controller name</label><input id="controller" bind:value={query.controller} placeholder="Any controller" /></div>
    <div class="control-group"><label for="action">Action name</label><input id="action" bind:value={query.action} placeholder="Any action" /></div>
    <div class="control-group wide">
      <label for="rf-operation">Operation</label>
      <input id="rf-operation" list="rf-operation-options" maxlength="200" autocomplete="off" aria-invalid={operationInvalid} aria-describedby={operationInvalid ? 'rf-operation-error' : undefined} value={operationText} oninput={(event) => setOperation(event.currentTarget.value)} placeholder="Controller.Action" />
      {#if operationInvalid}<p class="inline-error" id="rf-operation-error" role="alert">Not applied. Write the operation as Controller.Action, with a name on both sides of the dot.</p>{/if}
      <datalist id="rf-operation-options">{#each operationSuggestions as name (name)}<option value={name}></option>{/each}</datalist>
      <p class="field-note">Suggestions come from the loaded results.</p>
    </div>
  </div>
  <label class="check-row"><input type="checkbox" id="kind-unknown" checked={query.kind === 'http_unknown'} onchange={e => query.kind = unknownKindToggle(e.currentTarget.checked)} /> Only HTTP requests whose polling type is unknown</label>
</FilterGroup>

<style>
  .label-line { display: flex; align-items: center; gap: 0; }
  .label-line label { margin: 0; }
  input[aria-invalid='true'] { border-color: var(--bad); }
  .field-grid > .wide { grid-column: 1 / -1; }
  .check-row { display: flex; gap: 8px; align-items: center; min-height: 30px; margin-top: 8px; color: var(--text); font-size: 13px; }
  .check-row input { accent-color: var(--accent); }
</style>
