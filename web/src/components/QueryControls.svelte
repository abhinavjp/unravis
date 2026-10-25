<script lang="ts">
  import type { Query } from '../lib/types';
  import { quickRange, quickRanges, type QuickRangeId } from '../lib/time';
  import { intervalReplacedNote } from '../lib/autorun';
  import { intervalNote } from '../lib/intervalNote.svelte';
  import { isManualBucket } from '../lib/zoom';
  import { activeRangeId, filterChips, panelGroupCounts } from '../lib/depth';
  import { RAIL_GROUPS, RAIL_TEXT, railTarget, type RailGroup } from '../lib/filterRail';
  import { KIND_TITLES } from '../lib/copy/screenCopy';
  import Hint from './Hint.svelte';
  import WhenGroup from './filters/WhenGroup.svelte';
  import WhatGroup from './filters/WhatGroup.svelte';
  import DetailsGroup from './filters/DetailsGroup.svelte';
  import RangesGroup from './filters/RangesGroup.svelte';
  import ChartGroup from './filters/ChartGroup.svelte';
  // part "bar" is the always-visible scope (quick range chips and the request-type control); part "panel" is the five filter groups
  // as a group rail, kept behind the More filters disclosure; "all" renders both. Only the bar starts a run (onScopeChange).
  // activeGroup is the selected rail group. The owner keeps it, so a remount (a chip removed) keeps the same group on screen.
  let { query = $bindable(), targetMs = $bindable(), dateInputsValid = $bindable(), activeGroup = $bindable('when'), groupNames = [], now = '', part = 'all', onScopeChange }: { query: Query; targetMs: number | null; dateInputsValid: boolean; activeGroup?: RailGroup; groupNames?: string[]; now?: string; part?: 'all' | 'bar' | 'panel'; onScopeChange?: () => void } = $props();
  const kindSegments: { value: Query['kind']; label: string; title: string }[] = [
    { value: 'all', label: 'All', title: KIND_TITLES.all },
    { value: 'synchronous', label: 'Sync', title: KIND_TITLES.synchronous },
    { value: 'asynchronous', label: 'Async', title: KIND_TITLES.asynchronous },
    { value: 'hangfire', label: 'Hangfire', title: KIND_TITLES.hangfire },
    { value: 'http', label: 'HTTP', title: KIND_TITLES.http }
  ];
  const setKind = (value: Query['kind']) => {
    if (query.kind === value) return;
    query.kind = value;
    onScopeChange?.();
  };
  // A quick range ends now (the server's clock) and compares with the equal-length period right before it.
  const applyQuickRange = (id: QuickRangeId) => {
    // Choosing the range that is already active changes nothing, unless a panel date is invalid: then the click repairs it.
    if (activeRange === id && dateInputsValid) return;
    const base = new Date(now);
    const previous = query.bucketSeconds, typed = isManualBucket(query);
    Object.assign(query, quickRange(id, Number.isFinite(base.getTime()) ? base : new Date()));
    intervalNote.set(typed ? intervalReplacedNote(previous, query) : '');
    dateInputsValid = true;
    onScopeChange?.();
  };
  const activeRange = $derived(activeRangeId(query, quickRanges));
  // The group rail. Every pane stays mounted (hidden, not removed) so the drafts and sync effects in each group keep working.
  const counts = $derived(panelGroupCounts(filterChips(query)));
  // The other groups (What, Details, Ranges, Chart) report a problem through a bound hasError; When already reports it through dateInputsValid.
  const errors = $state<Record<RailGroup, boolean>>({ when: false, what: false, details: false, ranges: false, chart: false });
  const hasProblem = (id: RailGroup) => (id === 'when' ? !dateInputsValid : errors[id]);
  const problemGroups = $derived(RAIL_GROUPS.filter(group => hasProblem(group.id)));
  const countOf = (id: RailGroup) => (id === 'when' ? 0 : counts[id]);
  const current = $derived<RailGroup>(RAIL_GROUPS.some(group => group.id === activeGroup) ? activeGroup : 'when');
  let vertical = $state(true);
  $effect(() => {
    if (part === 'bar' || typeof window === 'undefined' || !window.matchMedia) return;
    const media = window.matchMedia('(min-width: 721px)');
    const sync = () => { vertical = media.matches; };
    sync();
    media.addEventListener('change', sync);
    return () => media.removeEventListener('change', sync);
  });
  let tabs = $state<HTMLButtonElement[]>([]);
  const select = (id: RailGroup, focus = false) => {
    activeGroup = id;
    if (focus) tabs[RAIL_GROUPS.findIndex(group => group.id === id)]?.focus();
  };
  // Arrow keys move and select, Home and End jump. Other keys (Esc, Tab) are left alone so Esc still closes the panel.
  const onRailKey = (event: KeyboardEvent) => {
    const index = RAIL_GROUPS.findIndex(group => group.id === current);
    const next = railTarget(event.key, index, RAIL_GROUPS.length);
    if (next === null) return;
    event.preventDefault();
    select(RAIL_GROUPS[next].id, true);
  };
</script>

<section class="query-controls part-{part}" aria-label="Comparison settings">
  {#if part !== 'panel'}
  <div class="scope-row">
    <div class="quick-ranges" role="group" aria-label="Quick ranges">
      <span>Range<Hint termId="time-window" /></span>
      {#each quickRanges as preset (preset.id)}<button type="button" class="chip" class:on={activeRange === preset.id} aria-pressed={activeRange === preset.id} title={'Investigate the ' + preset.label.toLowerCase() + ' and compare with the period right before it.'} onclick={() => applyQuickRange(preset.id)}>{preset.label}</button>{/each}
    </div>
    <div class="kind-control" role="group" aria-label="Request type">
      <span>Type<Hint termId="synchronous" /></span>
      <div class="segmented">{#each kindSegments as segment (segment.value)}<button type="button" class:on={query.kind === segment.value} aria-pressed={query.kind === segment.value} title={segment.title} onclick={() => setKind(segment.value)}>{segment.label}</button>{/each}</div>
    </div>
  </div>
  {/if}
  {#if part !== 'bar'}
  <div class="filter-panel">
    {#if problemGroups.length}
      <p class="panel-problems">{RAIL_TEXT.problemsLead} {#each problemGroups as group, index (group.id)}<button type="button" class="problem-link" onclick={() => select(group.id, true)}>{group.label}</button>{index < problemGroups.length - 1 ? ', ' : ''}{/each}</p>
    {/if}
    <div class="rail-layout">
      <div class="filter-rail" role="tablist" aria-label={RAIL_TEXT.railLabel} aria-orientation={vertical ? 'vertical' : 'horizontal'} tabindex="-1" onkeydown={onRailKey}>
        {#each RAIL_GROUPS as group, index (group.id)}
          <button type="button" role="tab" class="rail-item" class:on={current === group.id} id={'rail-tab-' + group.id} aria-selected={current === group.id} aria-controls={'rail-pane-' + group.id} tabindex={current === group.id ? 0 : -1} bind:this={tabs[index]} onclick={() => select(group.id)}>
            <span class="rail-name">{group.label}</span>
            {#if countOf(group.id)}<span class="rail-count">{countOf(group.id)}<span class="visually-hidden"> {RAIL_TEXT.inUse}</span></span>{/if}
            {#if hasProblem(group.id)}<span class="rail-dot" aria-hidden="true"></span><span class="visually-hidden"> {RAIL_TEXT.problem}</span>{/if}
          </button>
        {/each}
      </div>
      <div class="rail-panes">
        <div class="rail-pane" role="tabpanel" id="rail-pane-when" aria-labelledby="rail-tab-when" hidden={current !== 'when'}><WhenGroup bind:query bind:dateInputsValid /></div>
        <div class="rail-pane" role="tabpanel" id="rail-pane-what" aria-labelledby="rail-tab-what" hidden={current !== 'what'}><WhatGroup bind:query {groupNames} count={counts.what} bind:hasError={errors.what} /></div>
        <div class="rail-pane" role="tabpanel" id="rail-pane-details" aria-labelledby="rail-tab-details" hidden={current !== 'details'}><DetailsGroup bind:query count={counts.details} bind:hasError={errors.details} /></div>
        <div class="rail-pane" role="tabpanel" id="rail-pane-ranges" aria-labelledby="rail-tab-ranges" hidden={current !== 'ranges'}><RangesGroup bind:query count={counts.ranges} bind:hasError={errors.ranges} /></div>
        <div class="rail-pane" role="tabpanel" id="rail-pane-chart" aria-labelledby="rail-tab-chart" hidden={current !== 'chart'}><ChartGroup bind:query bind:targetMs count={counts.chart} bind:hasError={errors.chart} /></div>
      </div>
    </div>
  </div>
  {/if}
</section>
