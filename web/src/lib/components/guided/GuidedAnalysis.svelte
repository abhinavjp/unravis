<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import { GUIDED_TEXT as T } from '../../copy/guidedCopy';
  import { SHELL_TEXT as S } from '../../copy/guidedShellCopy';
  import { storeFor } from '../../guided/guided.svelte';
  import { nextTabIndex, railItems, stepOf, stickerFor } from '../../guided/stepModel';
  import type { SourceNeed } from '../../guided/tools';
  import { app } from '../../appState.svelte';
  import AfterFix from './AfterFix.svelte';
  import Checklist from './Checklist.svelte';
  import ManualTools from './ManualTools.svelte';
  import NearbyActivity from './NearbyActivity.svelte';
  import ReportExport from './ReportExport.svelte';
  import RunBar from './RunBar.svelte';
  import Step0Frame from './Step0Frame.svelte';
  import Step10Origin from './Step10Origin.svelte';
  import StepFacts from './StepFacts.svelte';
  import StepRail from './StepRail.svelte';
  import StepShell from './StepShell.svelte';

  // The guided analysis of one case: Steps 0 to 10 on a rail, or the Manual tools, plus the checklist, the after-the-fix comparison,
  // nearby activity and the export. Switching mode keeps the notes, the checklist and the saved views; a run only ever reads.
  let { caseId, closed = false, readOnly = false, hasIis = false, onAddData, onClose }: {
    caseId: string; closed?: boolean; readOnly?: boolean; hasIis?: boolean; onAddData: () => void; onClose: () => void;
  } = $props();

  type Panel = 'main' | 'checklist' | 'fix' | 'nearby' | 'export';
  // One store per case, held in memory: leaving this screen and coming back finds the run, windows and note as they were.
  const store = storeFor(untrack(() => caseId));
  let panel = $state<Panel>('main');
  let menu = $state<HTMLDetailsElement | null>(null);
  let heading = $state<HTMLHeadingElement | null>(null);
  // A step change (rail, Previous, Next, a link inside a step) moves the keyboard to the new step title. The first value is read
  // once, so remounting a case that already has a count does not steal focus.
  let seenFocus = untrack(() => store.titleFocus);
  $effect(() => {
    const wanted = store.titleFocus;
    if (wanted === seenFocus) return;
    seenFocus = wanted;
    if (untrack(() => store.mode !== 'guided' || panel !== 'main')) return;
    void tick().then(() => document.getElementById('step-title-' + store.step)?.focus());
  });

  const sourceIds = $derived(app.sources.map((s) => s.id));
  // SQL, Mongo and host files have no importer yet, so only the audit source and the IIS file can be present.
  const have = $derived<ReadonlySet<SourceNeed>>(new Set<SourceNeed>([...(sourceIds.length > 0 ? ['audit' as const] : []), ...(hasIis ? ['iis' as const] : [])]));
  const stale = $derived(store.outOfDate(sourceIds));
  const rail = $derived(railItems(store.result, store.phase, store.step, stale));
  const engineStep = $derived(stepOf(store.result, store.step));
  const sticker = $derived(stickerFor(store.result, store.step, store.phase, stale && store.step > 0));
  const PANELS: ReadonlyArray<{ id: Panel; label: () => string }> = [
    { id: 'main', label: () => (store.mode === 'guided' ? T.tabSteps : T.tabTools) },
    { id: 'checklist', label: () => T.checklistHeading },
    { id: 'fix', label: () => T.fixHeading },
    { id: 'nearby', label: () => T.nearbyHeading },
    { id: 'export', label: () => T.exportHeading }
  ];

  onMount(() => {
    void store.loadPlan();
    // Coming back to the window after adding data elsewhere: read what the case holds again.
    const onFocus = () => { void store.refreshStamp(); };
    window.addEventListener('focus', onFocus);
    return () => { window.removeEventListener('focus', onFocus); store.dispose(); };
  });
  // Read when the panel opens, and again when an IIS file arrives or goes (`hasIis` is the page's own view of the case's files).
  $effect(() => {
    void hasIis;
    untrack(() => { void store.refreshStamp(); });
  });

  function switchMode() {
    store.setMode(store.mode === 'guided' ? 'manual' : 'guided');
    panel = 'main';
    if (menu) menu.open = false;
    // The button that was pressed is gone with the menu; keep the keyboard on the page.
    void tick().then(() => heading?.focus());
  }

  function onTabKey(event: KeyboardEvent, index: number) {
    const next = nextTabIndex(event.key, index, PANELS.length);
    if (next === null) return;
    event.preventDefault();
    const target = PANELS[next];
    panel = target.id;
    document.getElementById('guided-tab-' + target.id)?.focus();
  }
</script>

<div class="guided">
  <div class="top">
    <button type="button" class="secondary-button" onclick={onClose}>&larr; {T.closeGuided}</button>
  </div>

  <header class="head">
    <div class="titles">
      <h2 tabindex="-1" bind:this={heading}>{store.mode === 'guided' ? T.heading : S.manualHeading}</h2>
      <p class="note">{store.mode === 'guided' ? `${T.modeGuidedNow} ${T.intro}` : `${T.modeManualNow} ${T.introManual}`}</p>
    </div>
    <details class="menu" bind:this={menu}>
      <summary>{T.caseActions}</summary>
      <div class="menu-list">
        <button type="button" onclick={switchMode}>{store.mode === 'guided' ? T.toManual : T.toGuided}</button>
        <p class="kept">{T.modeKept}</p>
      </div>
    </details>
  </header>
  {#if closed}<p class="notice" role="status">{T.closedNote}</p>{/if}

  <div class="tabs" role="tablist" aria-label={T.panelsLabel}>
    {#each PANELS as p, i (p.id)}
      <button type="button" role="tab" id={'guided-tab-' + p.id} class="tab" class:on={panel === p.id} aria-selected={panel === p.id} aria-controls="guided-panel" tabindex={panel === p.id ? 0 : -1} onclick={() => (panel = p.id)} onkeydown={(e) => onTabKey(e, i)}>{p.label()}</button>
    {/each}
  </div>

  <div id="guided-panel" role="tabpanel" aria-labelledby={'guided-tab-' + panel} class="panel">
    {#if panel === 'checklist'}
      <Checklist {store} {readOnly} />
    {:else if panel === 'fix'}
      <AfterFix {store} {sourceIds} {readOnly} />
    {:else if panel === 'nearby'}
      <NearbyActivity {store} {sourceIds} />
    {:else if panel === 'export'}
      <ReportExport {store} {sourceIds} />
    {:else if store.mode === 'manual'}
      <ManualTools {store} {sourceIds} {readOnly} {have} {onAddData} onOpenChecklist={() => (panel = 'checklist')} />
    {:else}
      <div class="steps">
        <StepRail items={rail} onSelect={(n) => store.goTo(n)} />
        <div class="page">
          <RunBar {store} {sourceIds} {readOnly} {onAddData} />
          {#if store.step === 0}
            <StepShell step={0} {sticker} nutshell={engineStep?.nutshell ?? ''} reasons={engineStep?.reasons ?? []} onGo={(n) => store.goTo(n)}>
              <Step0Frame {store} />
            </StepShell>
          {:else if store.step === 10}
            <Step10Origin {store} {sourceIds} {readOnly} onOpenChecklist={() => (panel = 'checklist')} />
          {:else}
            <StepFacts {store} {sourceIds} step={store.step} {readOnly} {onAddData} />
          {/if}
        </div>
      </div>
    {/if}
  </div>
</div>

<style>
  .guided { display: grid; gap: 16px; min-width: 0; }
  .top { display: flex; }
  .head { display: flex; flex-wrap: wrap; align-items: flex-start; justify-content: space-between; gap: 8px 16px; }
  .titles { display: grid; gap: 4px; min-width: 0; flex: 1 1 260px; }
  h2 { margin: 0; font-size: 22px; }
  .note { margin: 0; color: var(--text-2); font-size: 14px; }
  .kept { margin: 0; padding: 8px 10px; color: var(--text-2); font-size: 12px; }
  .tabs { display: flex; flex-wrap: wrap; gap: 4px 16px; border-bottom: 1px solid var(--line); }
  .tab { padding: 8px 4px; color: var(--text-2); background: transparent; border: 0; border-bottom: 2px solid transparent; border-radius: 0; font-size: 14px; font-weight: 500; }
  .tab:hover { color: var(--text); }
  .tab.on { color: var(--text); border-bottom-color: var(--accent); font-weight: 600; }
  .tab:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .panel { min-width: 0; }
  .steps { display: grid; grid-template-columns: minmax(200px, 240px) minmax(0, 1fr); gap: 16px; align-items: start; }
  .page { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; min-width: 0; }
  @media (max-width: 760px) {
    .steps { grid-template-columns: minmax(0, 1fr); }
  }
</style>
