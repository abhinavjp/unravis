<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api } from '../lib/api';
  import { setDrillOpen } from '../lib/appState.svelte';
  import { DRILL_HELP, DRILL_TEXT } from '../lib/drill/drillCopy';
  import DrillOverview from './DrillOverview.svelte';
  import { drillQuery, drillSourceIds, pickCall, slotWindow } from '../lib/drill/target';
  import { parsePerf, type DrillRecord } from '../lib/drill/perf';
  import type { TracingLane, TracingSourceRef, TracingWindow } from '../lib/tracing/model';

  // The drill pop-up: the slowest call in one timeline slot. D1 has the Overview tab only; `tabs` takes more entries later.
  // Focus: the dialog takes focus on open and gives it back on close; Tab stays inside; Esc closes.
  const FOCUSABLE = 'button:not(:disabled), input:not(:disabled), select:not(:disabled), [href], [tabindex]:not([tabindex="-1"])';

  let {
    window: timeWindow,
    slot,
    lane,
    sources,
    zone,
    onClose,
    onOpenAnalysis
  }: {
    window: TracingWindow;
    slot: number;
    lane: TracingLane | undefined;
    sources: TracingSourceRef[];
    zone: string;
    onClose: () => void;
    onOpenAnalysis: (slot: number) => void;
  } = $props();

  type Phase = 'loading' | 'notFound' | 'failed' | 'ready';

  const tabs = [{ id: 'overview', label: DRILL_TEXT.tabOverview }];

  let popupEl = $state<HTMLElement>();
  let phase = $state<Phase>('loading');
  let record = $state<DrillRecord | null>(null);
  let activeTab = $state(tabs[0].id);
  let controller: AbortController | null = null;
  let opener: Element | null = null;

  async function load() {
    controller?.abort();
    const ctl = new AbortController();
    controller = ctl;
    const signal = ctl.signal;
    phase = 'loading';
    record = null;
    const win = slotWindow(timeWindow, slot);
    const ids = lane ? drillSourceIds(lane, sources) : [];
    if (!win || ids.length === 0) { phase = 'notFound'; return; }
    try {
      const page = await api.slowest(drillQuery(win, ids), signal);
      if (signal.aborted) return;
      const call = pickCall(page);
      if (!call) { phase = 'notFound'; return; }
      const rec = await api.record(call.sourceId, call.id, signal);
      if (signal.aborted) return;
      record = { ...rec, perf: parsePerf((rec as { perf?: unknown }).perf) };
      phase = 'ready';
    } catch {
      if (signal.aborted) return;
      phase = 'failed';
    }
  }

  onMount(() => {
    setDrillOpen(true);
    opener = document.activeElement;
    popupEl?.focus({ preventScroll: true });
    void load();
  });

  onDestroy(() => {
    setDrillOpen(false);
    controller?.abort();
    if (opener instanceof HTMLElement && opener.isConnected) opener.focus({ preventScroll: true });
  });

  function onKey(event: KeyboardEvent) {
    if (event.key === 'Escape') { event.preventDefault(); onClose(); return; }
    if (event.key !== 'Tab' || !popupEl) return;
    const items = [...popupEl.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((element) => element.getClientRects().length > 0);
    if (!items.length) { event.preventDefault(); popupEl.focus({ preventScroll: true }); return; }
    const first = items[0], last = items[items.length - 1];
    const active = document.activeElement;
    const outside = !(active instanceof Node) || active === popupEl || !popupEl.contains(active);
    if (event.shiftKey) {
      if (outside || active === first) { event.preventDefault(); last.focus(); }
    } else if (outside || active === last) {
      event.preventDefault(); first.focus();
    }
  }
</script>

<svelte:window onkeydown={onKey} />

<button type="button" class="scrim" tabindex="-1" aria-label={DRILL_TEXT.close} onclick={onClose}></button>
<div class="popup" role="dialog" aria-modal="true" aria-labelledby="drillTitle" tabindex="-1" data-testid="drill-popup" data-zone={zone} bind:this={popupEl}>
  <div class="popup-head">
    <h2 id="drillTitle" title={DRILL_HELP.title}>{DRILL_TEXT.title}</h2>
    <div class="head-actions">
      <button type="button" class="btn btn-secondary" title={DRILL_HELP.openAnalysis} onclick={() => onOpenAnalysis(slot)}>{DRILL_TEXT.openAnalysis}</button>
      <button type="button" class="btn btn-secondary" onclick={onClose}>{DRILL_TEXT.close}</button>
    </div>
  </div>

  <div class="tabbar" role="tablist" aria-label={DRILL_TEXT.tabsLabel}>
    {#each tabs as tab (tab.id)}
      <button type="button" role="tab" class="tab" id={'drillTab-' + tab.id} aria-selected={activeTab === tab.id} aria-controls={'drillPanel-' + tab.id} tabindex={activeTab === tab.id ? 0 : -1} onclick={() => (activeTab = tab.id)}>{tab.label}</button>
    {/each}
  </div>

  <div class="popup-body" role="tabpanel" id={'drillPanel-' + activeTab} aria-labelledby={'drillTab-' + activeTab}>
    {#if phase === 'loading'}
      <p class="note" role="status">{DRILL_TEXT.finding}</p>
    {:else if phase === 'notFound'}
      <p class="note">{DRILL_TEXT.notFound}</p>
    {:else if phase === 'failed'}
      <div class="problem" role="alert">
        <p>{DRILL_TEXT.loadFailed}</p>
        <button type="button" class="btn btn-secondary" onclick={() => { popupEl?.focus({ preventScroll: true }); void load(); }}>{DRILL_TEXT.retry}</button>
      </div>
    {:else if record}
      {#if activeTab === 'overview'}<DrillOverview {record} />{/if}
    {/if}
  </div>
</div>

<style>
  .scrim { position: fixed; inset: 0; z-index: 40; width: 100%; height: 100%; padding: 0; border: 0; background: var(--scrim); cursor: default; }
  .popup {
    position: fixed; top: 50%; left: 50%; transform: translate(-50%, -50%);
    width: min(860px, calc(100vw - 48px)); max-height: calc(100dvh - 32px);
    display: flex; flex-direction: column;
    color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius);
    box-shadow: 0 24px 64px color-mix(in srgb, var(--bg) 70%, transparent);
    z-index: 45; outline: none;
  }
  .popup-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; padding: 16px 20px 12px; border-bottom: 1px solid var(--line); }
  .popup-head h2 { margin: 0; font-size: 18px; font-weight: 600; letter-spacing: -.01em; }
  .head-actions { display: flex; gap: 8px; flex-wrap: wrap; }
  .tabbar { display: flex; gap: 4px; padding: 0 20px; border-bottom: 1px solid var(--line-soft); }
  .tab { padding: 10px 12px; color: var(--text-2); background: none; border: 0; border-bottom: 2px solid transparent; font-size: 13px; font-weight: 600; cursor: pointer; }
  .tab[aria-selected='true'] { color: var(--text); border-bottom-color: var(--accent); }
  .tab:focus-visible { outline: 2px solid var(--focus); outline-offset: -2px; }
  .popup-body { flex: 1 1 auto; overflow-y: auto; overscroll-behavior: contain; padding: 18px 20px; scrollbar-width: thin; }
  .note { margin: 0; color: var(--text-2); font-size: 13px; }
  .problem { display: grid; justify-items: start; gap: 12px; padding: 12px; background: var(--surface-2); border: 1px solid var(--warn); border-radius: var(--radius); }
  .problem p { margin: 0; font-size: 13px; }

  .btn { display: inline-flex; align-items: center; justify-content: center; gap: 8px; min-height: 36px; padding: 8px 14px; border-radius: var(--radius); font-size: 13px; font-weight: 600; cursor: pointer; transition: background var(--dur) var(--ease), border-color var(--dur) var(--ease), color var(--dur) var(--ease); }
  .btn-secondary { color: var(--text); background: transparent; border: 1px solid var(--line); }
  .btn-secondary:hover { border-color: var(--text-2); background: var(--surface-2); }
  .btn:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }

  @media (pointer: coarse), (max-width: 720px) {
    .btn { min-height: 44px; }
  }
  @media (max-width: 720px) {
    .popup { top: 0; left: 0; width: 100vw; height: 100dvh; max-height: 100dvh; transform: none; border-radius: 0; }
  }
  @media (prefers-reduced-motion: reduce) {
    *, *::before, *::after { animation-duration: .01ms !important; transition-duration: .01ms !important; }
  }
</style>
