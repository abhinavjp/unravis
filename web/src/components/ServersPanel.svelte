<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { app, closeSidePanel, openSidePanel, openTracing } from '../lib/appState.svelte';
  import { SV_TEXT as T } from '../lib/copy/serversCopy';
  import { failureFix, failureText } from '../lib/servers';
  import { STEP_ORDER, ServersFlow, type Step } from '../lib/serversFlow.svelte';
  import { getCaseView } from '../lib/cases';
  import ConnectStep from './servers/ConnectStep.svelte';
  import TrustStep from './servers/TrustStep.svelte';
  import PickStep from './servers/PickStep.svelte';
  import ReachStep from './servers/ReachStep.svelte';
  import HasStep from './servers/HasStep.svelte';
  import PullStep from './servers/PullStep.svelte';
  import LandStep from './servers/LandStep.svelte';

  // The Servers screen: get Blackbox recordings and logs from a remote server through a Salt master. Everything on it comes from the
  // backend. The case's time windows are read from the case and are never written here.
  let { onClose }: { onClose: () => void } = $props();

  const flow = new ServersFlow({ windows: () => app.savedWindows, now: () => Date.now() });
  const inCase = $derived(app.screen === 'case');
  const fix = $derived(flow.error ? failureFix(flow.error.reason) : 'none');

  let heading = $state<HTMLElement>();
  let page = $state<HTMLElement>();
  let caseTitle = $state('');
  onMount(() => { heading?.focus(); void flow.load(); if (app.activeCaseId) void getCaseView(app.activeCaseId).then((view) => { caseTitle = view.title; }).catch(() => {}); });
  onDestroy(() => flow.dispose());

  async function changeWindows() {
    await closeSidePanel();
    openTracing();
  }

  async function openBlackbox() {
    await closeSidePanel();
    openSidePanel('blackbox');
  }

  function applyFix() {
    if (fix === 'trust') { flow.go('trust'); void flow.fetchKeys(); }
    else if (fix === 'connect') { flow.go('connect'); flow.needSecret = !!flow.server; }
    else if (fix === 'folders') flow.go('reach');
    else if (fix === 'machines') flow.go('pick');
  }

  const fixLabel = $derived(fix === 'trust' ? T.fixTrust : fix === 'connect' ? T.fixConnect : fix === 'folders' ? T.fixFolders : fix === 'machines' ? T.fixMachines : '');
  const stepIndex = $derived(STEP_ORDER.indexOf(flow.step));
  const goStep = (step: Step) => flow.go(step);
  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose(); return; }
    if (e.key !== 'Tab' || !page) return;
    const items = [...page.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), summary, [href], [tabindex]:not([tabindex="-1"])')].filter((el) => el.getClientRects().length);
    if (!items.length) return;
    if (e.shiftKey && document.activeElement === items[0]) { e.preventDefault(); items[items.length - 1].focus(); }
    else if (!e.shiftKey && document.activeElement === items[items.length - 1]) { e.preventDefault(); items[0].focus(); }
  }
</script>

<!-- svelte-ignore a11y_no_noninteractive_tabindex -->
<div class="servers-page" role="dialog" aria-modal="true" aria-labelledby="servers-title" tabindex="-1" bind:this={page} onkeydown={onKey}>
  <header class="sv-header"><h2 id="servers-title" tabindex="-1" bind:this={heading}>{#if caseTitle}Case: {caseTitle} / {/if}{T.title} / {T.pageTitle}</h2><button type="button" class="secondary-button" onclick={onClose}>{T.closeButton}</button></header>
  <div class="sv-purpose"><strong>{T.purpose}</strong><p>{T.purposeDetail}</p></div>
  <div class="sv">

    <nav aria-label={T.stepsLabel}>
      <ol class="sv-steps">
        {#each T.steps as label, i (label)}
          {@const step = STEP_ORDER[i]}
          <li>
            <button type="button" class="sv-step" class:sv-step-on={i === stepIndex} class:sv-step-done={i < flow.reached && i !== stepIndex}
              aria-current={i === stepIndex ? 'step' : undefined} disabled={i > flow.reached || flow.running} onclick={() => goStep(step)}>
              <span class="sv-step-n" aria-hidden="true">{i < flow.reached && i !== stepIndex ? '✓' : i + 1}</span><span>{label}</span>
            </button>
          </li>
        {/each}
      </ol>
    </nav>

    {#if flow.error}
      <div class="notice" role="alert">
        <p>{failureText(flow.error)}</p>
        {#if fixLabel}<button type="button" class="secondary-button" onclick={applyFix}>{fixLabel}</button>{/if}
      </div>
    {/if}

    <svelte:boundary onerror={(error) => console.error(error)}>
    {#if flow.step === 'connect'}
      <ConnectStep {flow} />
    {:else if flow.step === 'trust'}
      <TrustStep {flow} />
    {:else if flow.step === 'pick'}
      <PickStep {flow} />
    {:else if flow.step === 'reach'}
      <ReachStep {flow} />
    {:else if flow.step === 'has'}
      <HasStep {flow} {inCase} onChangeWindows={changeWindows} />
    {:else if flow.step === 'pull'}
      <PullStep {flow} />
    {:else}
      <LandStep {flow} onOpenBlackbox={openBlackbox} />
    {/if}
    {#snippet failed(_error, reset)}
      <div class="notice" role="alert">
        <p>{T.stepCrashed}</p>
        <button type="button" class="secondary-button" onclick={() => { reset(); if (flow.step === 'pick') void flow.loadMachines(); }}>{T.retry}</button>
      </div>
    {/snippet}
    </svelte:boundary>
  </div>
</div>

<style>
  .servers-page { position:fixed; inset:var(--header-h) 0 0; z-index:11; overflow:auto; background:var(--bg); color:var(--text); }
  .sv-header { display:flex; align-items:center; justify-content:space-between; gap:16px; padding:14px 24px; background:var(--surface); border-bottom:1px solid var(--line); }
  .sv-header h2 { margin:0; font-size:var(--fs-3); }
  .sv-purpose { padding:16px 24px; background:var(--surface); border-bottom:1px solid var(--line); }
  .sv-purpose p { margin:5px 0 0; color:var(--text-2); }

  .sv { display: grid; gap: var(--space-4); min-width: 0; max-width:1100px; margin:auto; padding:24px; }

  .sv-steps { display: grid; grid-template-columns:repeat(7,minmax(0,1fr)); margin: 0; padding: 0; list-style: none; }
  @media (max-width:760px) { .sv-steps { grid-template-columns:repeat(2,minmax(0,1fr)); } }
  .sv-step { display: flex; width:100%; height:100%; align-items: center; gap: 6px; min-height: 48px; padding: 6px 8px; color: var(--text-2); background:var(--surface); border: 1px solid var(--line); border-radius:0; font-size: var(--fs-3); font-weight: 500; text-align:left; overflow-wrap:anywhere; }
  .sv-step:hover:not(:disabled) { color: var(--text); background: var(--surface-2); }
  .sv-step:disabled { opacity: 0.55; cursor: default; }
  .sv-step-on { color: var(--text); border-bottom:3px solid var(--accent); font-weight:700; }
  .sv-step-done { color: var(--text); }
  .sv-step-n { font-size: 12px; font-variant-numeric: tabular-nums; }

  .notice { display: grid; gap: var(--space-2); }
  .notice p { margin: 0; }

  /* shared by the step components */
  .sv :global(.sv-block) { display: grid; gap: var(--space-3); min-width: 0; }
  .sv :global(.sv-block h3) { margin: 0; font-size: var(--fs-5); font-weight: 600; }
  .sv :global(.sv-lead) { margin: 0; color: var(--text); font-size: var(--fs-3); max-width: 62ch; }
  .sv :global(.sv-note) { margin: 0; color: var(--text-2); font-size: var(--fs-2); overflow-wrap: anywhere; }
  .sv :global(.sv-bad) { margin: 0; color: var(--bad); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .sv :global(.sv-actions) { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .sv :global(.sv-table-wrap) { overflow-x:auto; border:1px solid var(--line); background:var(--surface); }
  .sv :global(.sv-table) { width:100%; border-collapse:collapse; text-align:left; font-size:var(--fs-3); }
  .sv :global(.sv-table th), .sv :global(.sv-table td) { padding:10px 12px; border-bottom:1px solid var(--line); }
  .sv :global(.sv-form) { display: grid; gap: var(--space-3); }
  .sv :global(.sv-field) { position: relative; display: grid; gap: 4px; min-width: 0; margin: 0; padding: 0; border: 0; }
  .sv :global(.sv-field > span), .sv :global(.sv-field > legend) { color: var(--text); font-size: var(--fs-3); font-weight: 500; padding: 0; }
  .sv :global(.sv-field small) { color: var(--text-2); font-size: var(--fs-2); }
  .sv :global(.sv-field input[type='text']), .sv :global(.sv-field input[type='password']), .sv :global(.sv-field input[type='file']), .sv :global(.sv-field select) {
    width: 100%; min-width: 0; padding: 6px 8px; font: inherit; font-size: var(--fs-3); color: var(--text); background: var(--surface); border: 1px solid var(--control-line); border-radius: var(--radius-sm); box-sizing: border-box;
  }
  .sv :global(.sv-field.sv-inline select) { width: auto; max-width: 100%; }
  .sv :global(.sv-check) { display: inline-flex; flex-wrap: wrap; align-items: center; gap: 6px 8px; font-size: var(--fs-3); min-width: 0; overflow-wrap: anywhere; }
  .sv :global(.sv-row) { display: grid; gap: var(--space-2); padding: var(--space-3) 0; border-top: 1px solid var(--line); min-width: 0; }
  .sv :global(.sv-row-main) { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--space-2); min-width: 0; overflow-wrap: anywhere; }
  .sv :global(.sv-chip) { display: inline-flex; align-items: center; min-height: 22px; padding: 1px 8px; color: var(--text-2); border: 1px solid var(--control-line); border-radius: var(--radius-sm); font-size: var(--fs-2); font-weight: 500; }
  .sv :global(.sv-tone-ok) { color: var(--tone-ok); border-color: var(--tone-ok); }
  .sv :global(.sv-tone-bad) { color: var(--bad); border-color: var(--bad); }
  .sv :global(.sv-tone-pending) { color: var(--pending); border-color: var(--pending); }
  /* panels, tables and status markers shared by steps 4 to 7 (the prototype's .panel, .tbl, .st, .bar) */
  .sv :global(.sv-panel) { border:1px solid var(--line); background:var(--surface); min-width:0; }
  .sv :global(.sv-panel > header) { display:flex; flex-wrap:wrap; align-items:center; gap:4px 12px; padding:10px 14px; border-bottom:1px solid var(--line); }
  .sv :global(.sv-panel > header h4) { margin:0; font-size:var(--fs-4); font-weight:700; overflow-wrap:anywhere; }
  .sv :global(.sv-sp) { flex:1 1 0; }
  .sv :global(.sv-sub) { color:var(--text-2); font-size:var(--fs-2); }
  .sv :global(.sv-in) { padding:12px 14px; display:grid; gap:var(--space-2); }
  .sv :global(.sv-tbl) { width:100%; border-collapse:collapse; text-align:left; font-size:var(--fs-3); font-family:inherit; }
  .sv :global(.sv-tbl th) { padding:8px 14px; font-family:inherit; text-transform:none; letter-spacing:normal; color:var(--text-2); font-size:var(--fs-2); font-weight:600; border-bottom:1px solid var(--line); }
  .sv :global(.sv-tbl td) { font-family:inherit; padding:9px 14px; border-bottom:1px solid var(--line); vertical-align:top; }
  .sv :global(.sv-tbl tr:last-child td) { border-bottom:0; }
  .sv :global(.sv-tbl .r) { text-align:right; }
  .sv :global(.sv-tbl tr.sv-dim td) { color:var(--text-2); }
  .sv :global(.sv-mono) { font-family:var(--mono); font-size:12px; overflow-wrap:anywhere; }
  .sv :global(.sv-st) { display:inline-flex; align-items:center; gap:6px; white-space:nowrap; }
  .sv :global(.sv-st::before) { content:''; width:8px; height:8px; background:var(--pending); }
  .sv :global(.sv-st-ok::before) { background:var(--tone-ok); }
  .sv :global(.sv-st-bad::before) { background:var(--bad); }
  .sv :global(.sv-meter) { height:6px; background:var(--surface-2); border:1px solid var(--line); overflow:hidden; }
  .sv :global(.sv-meter > i) { display:block; height:100%; background:var(--accent); transition:width .3s ease; }
  .sv :global(.sv-meter-wait > i) { background:var(--pending); }
  .sv :global(.sv-meter-bad > i) { background:var(--bad); }
  .sv :global(.sv-stop) { padding:14px; border:1px solid var(--bad); background:var(--surface); display:grid; gap:var(--space-2); max-width:760px; }
  .sv :global(.sv-stop h4) { margin:0; font-size:var(--fs-4); color:var(--bad); }
  .sv :global(.sv-kv) { display:grid; grid-template-columns:max-content 1fr; gap:6px 16px; margin:0; font-size:var(--fs-3); }
  .sv :global(.sv-kv dt) { color:var(--text-2); }
  .sv :global(.sv-kv dd) { margin:0; display:grid; }
  @media (prefers-reduced-motion: reduce) { .sv :global(.sv-meter > i) { transition:none; } }
  .sv :global(h4) { margin: 0; }
</style>

