<script lang="ts">
  import FilterGroup from './FilterGroup.svelte';
  import type { Query, RangeKey, NumberRange } from '../../lib/types';
  import { RANGE_FIELDS, buildRange, formatNumber, syncRangeDraft, rangeConflict } from '../../lib/investigation';

  // hasError lets the panel rail show a problem dot while this group is on a hidden pane.
  let { query = $bindable(), count = 0, hasError = $bindable(false) }: { query: Query; count: number; hasError?: boolean } = $props();

  const rangeMeta: Record<RangeKey, { label: string; describe: string; effect: string }> = {
    observedMs: {
      label: 'Observed time',
      describe: 'From the request start to its completion.',
      effect: 'Complete requests only, so it sets Completion to Complete. Its minimum also raises the slowest-requests floor.'
    },
    apiTimeMs: {
      label: 'API time',
      describe: 'Time spent in the API itself.',
      effect: 'Complete requests only, so it sets Completion to Complete. Its minimum also raises the slowest-requests floor when the time measured is API time.'
    },
    auditCreateTimeMs: {
      label: 'Audit write time',
      describe: 'Time spent creating the audit record.',
      effect: 'Complete requests only, so it sets Completion to Complete.'
    },
    requestKb: {
      label: 'Request size',
      describe: 'Size of the request body.',
      effect: 'Requests that do not record a size never match a bounded range.'
    },
    responseKb: {
      label: 'Response size',
      describe: 'Size of the response body.',
      effect: 'Requests that do not record a size never match a bounded range.'
    },
    incompleteAgeMs: {
      label: 'Time without finishing',
      describe: 'How long a request has gone without finishing, measured when the search runs.',
      effect: 'Incomplete and pending requests only, so it sets Completion to Incomplete. It cannot be combined with a time range.'
    }
  };

  type RangeDraft = { min: string; max: string };
  const draftOf = (range: NumberRange | undefined): RangeDraft => ({
    min: range?.min != null ? formatNumber(range.min) : '',
    max: range?.max != null ? formatNumber(range.max) : ''
  });

  let rangeText = $state<Record<RangeKey, RangeDraft>>(
    Object.fromEntries(RANGE_FIELDS.map(field => [field.key, draftOf(query[field.key])])) as Record<RangeKey, RangeDraft>
  );

  const rangeProblems = $derived(
    Object.fromEntries(
      RANGE_FIELDS.map(field => [
        field.key,
        buildRange(rangeText[field.key].min, rangeText[field.key].max, field.limit).problems
      ])
    ) as Record<RangeKey, string[]>
  );

  const rangeNote = $derived(rangeConflict(query));
  $effect(() => { hasError = !!rangeNote || RANGE_FIELDS.some(field => rangeProblems[field.key].length > 0); });

  const setRange = (key: RangeKey, side: 'min' | 'max', text: string, limit: number) => {
    const draft = { ...rangeText[key], [side]: text };
    rangeText[key] = draft;
    query[key] = buildRange(draft.min, draft.max, limit).range;
  };

  $effect(() => {
    const ranges = RANGE_FIELDS.map(field => ({ key: field.key, limit: field.limit, min: query[field.key]?.min, max: query[field.key]?.max }));
    for (const { key, limit, min, max } of ranges) {
      const next = syncRangeDraft(rangeText[key], min != null || max != null ? { ...(min != null ? { min } : {}), ...(max != null ? { max } : {}) } : undefined, limit);
      if (next !== rangeText[key]) rangeText[key] = next;
    }
  });
</script>

<FilterGroup id="ranges" title="Ranges" count={count}>
  {#snippet lede()}
    <span>Both ends are optional and included. A request that does not record the value never matches a range.</span>
  {/snippet}

  {#if rangeNote}<p class="inline-error" role="alert">{rangeNote}</p>{/if}

  <div class="ranges-table">
    <div class="ranges-head" aria-hidden="true">
      <span>Measure</span>
      <span>Min</span>
      <span>Max</span>
      <span>Note</span>
    </div>
    {#each RANGE_FIELDS as field (field.key)}
      <div class="range-row" role="group" aria-labelledby="rf-{field.key}-label" title={rangeMeta[field.key].describe}>
        <span class="range-name" id="rf-{field.key}-label">{rangeMeta[field.key].label} ({field.unit})</span>
        <span class="visually-hidden" id="rf-{field.key}-desc">{rangeMeta[field.key].describe}</span>
        <input
          id="rf-{field.key}-min"
          type="text"
          inputmode="decimal"
          autocomplete="off"
          aria-label="{rangeMeta[field.key].label} minimum ({field.unit})"
          aria-invalid={rangeProblems[field.key].length > 0}
          aria-describedby="rf-{field.key}-desc rf-{field.key}-note{rangeProblems[field.key].length > 0 ? ' rf-' + field.key + '-error' : ''}"
          value={rangeText[field.key].min}
          oninput={(event) => setRange(field.key, 'min', event.currentTarget.value, field.limit)}
          placeholder="Min"
        />
        <input
          id="rf-{field.key}-max"
          type="text"
          inputmode="decimal"
          autocomplete="off"
          aria-label="{rangeMeta[field.key].label} maximum ({field.unit})"
          aria-invalid={rangeProblems[field.key].length > 0}
          aria-describedby="rf-{field.key}-desc rf-{field.key}-note{rangeProblems[field.key].length > 0 ? ' rf-' + field.key + '-error' : ''}"
          value={rangeText[field.key].max}
          oninput={(event) => setRange(field.key, 'max', event.currentTarget.value, field.limit)}
          placeholder="Max"
        />
        <span class="range-note" id="rf-{field.key}-note">{rangeMeta[field.key].effect}</span>
        {#if rangeProblems[field.key].length > 0}
          <p class="inline-error range-error" id="rf-{field.key}-error" role="alert">Not applied. {rangeProblems[field.key][0]}.</p>
        {/if}
      </div>
    {/each}
  </div>
</FilterGroup>

<style>
  .range-row {
    display: grid;
    grid-template-columns: minmax(9rem, 1.1fr) minmax(0, .6fr) minmax(0, .6fr) minmax(0, 2fr);
    gap: 4px 10px;
    align-items: center;
    padding: 6px 0;
    border-top: 1px solid var(--line-soft);
  }
  .ranges-head {
    display: grid;
    grid-template-columns: minmax(9rem, 1.1fr) minmax(0, .6fr) minmax(0, .6fr) minmax(0, 2fr);
    gap: 4px 10px;
    padding: 12px 0;
    color: var(--text-2);
    font-weight: 500;
  }
  .range-error {
    grid-column: 1 / -1;
    margin: 0;
  }
  .range-note {
    font-size: 12px;
    color: var(--text-2);
  }
  input {
    font-variant-numeric: tabular-nums;
  }
  @media (max-width: 720px) {
    .ranges-head {
      display: none;
    }
    .range-row {
      grid-template-columns: repeat(2, minmax(0, 1fr));
      grid-template-areas:
        "name name"
        "min max"
        "note note"
        "err err";
    }
    .range-name {
      grid-area: name;
    }
    .range-row input[id$="-min"] {
      grid-area: min;
    }
    .range-row input[id$="-max"] {
      grid-area: max;
    }
    .range-note {
      grid-area: note;
    }
    .range-error {
      grid-area: err;
    }
  }
</style>