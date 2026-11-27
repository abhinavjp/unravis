<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { app, closeSidePanel, openSidePanel, openTracing } from '../lib/appState.svelte';
  import { SV_TEXT as T } from '../lib/copy/serversCopy';
  import { failureFix, failureText } from '../lib/servers';
  import { STEP_ORDER, ServersFlow, type Step } from '../lib/serversFlow.svelte';
  import Drawer from './Drawer.svelte';
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

  onMount(() => { void flow.load(); });
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
</script>

<Drawer class="glossary-drawer servers-drawer" focusOnOpen title={T.title} titleId="servers-title" intro={T.intro} closeLabel={T.close} {onClose}>
  <div class="sv">
    <div class="sv-promise">
      <p class="sv-note">{T.readOnly}</p>
      <ul class="sv-promises">{#each T.promises as p (p)}<li>{p}</li>{/each}</ul>
    </div>

    <nav aria-label={T.stepsLabel}>
      <ol class="sv-steps">
        {#each T.steps as label, i (label)}
          {@const step = STEP_ORDER[i]}
          <li>
            <button type="button" class="sv-step" class:sv-step-on={i === stepIndex} class:sv-step-done={i < flow.reached && i !== stepIndex}
              aria-current={i === stepIndex ? 'step' : undefined} disabled={i > flow.reached || flow.running} onclick={() => goStep(step)}>
              <span class="sv-step-n" aria-hidden="true">{i + 1}</span><span>{label}</span>
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
  </div>
</Drawer>

<style>
  :global(.connections-drawer.servers-drawer) { overflow-x: hidden; gap: var(--space-3); }
  @media (min-width: 721px) { :global(.connections-drawer.servers-drawer) { width: min(720px, calc(100vw - 24px)); } }

  .sv { display: grid; gap: var(--space-4); min-width: 0; }
  .sv-promise { display: grid; gap: var(--space-2); }
  .sv-promises { display: grid; gap: 2px; margin: 0; padding-left: var(--space-5); color: var(--text-2); font-size: var(--fs-3); }

  .sv-steps { display: flex; flex-wrap: wrap; gap: var(--space-1) var(--space-2); margin: 0; padding: 0; list-style: none; }
  .sv-step { display: inline-flex; align-items: center; gap: 6px; min-height: 28px; padding: 3px 8px; color: var(--text-2); background: transparent; border: 1px solid transparent; border-radius: var(--radius-sm); font-size: var(--fs-3); font-weight: 500; }
  .sv-step:hover:not(:disabled) { color: var(--text); background: var(--surface-2); }
  .sv-step:disabled { opacity: 0.55; cursor: default; }
  .sv-step-on { color: var(--text); border-color: var(--accent); background: var(--surface-2); }
  .sv-step-done { color: var(--text); }
  .sv-step-n { display: inline-grid; place-items: center; width: 20px; height: 20px; border: 1px solid var(--control-line); border-radius: 50%; font-size: 12px; font-variant-numeric: tabular-nums; }
  .sv-step-on .sv-step-n { border-color: var(--accent); }

  .notice { display: grid; gap: var(--space-2); }
  .notice p { margin: 0; }

  /* shared by the step components */
  .sv :global(.sv-block) { display: grid; gap: var(--space-3); min-width: 0; }
  .sv :global(.sv-block h3) { margin: 0; font-size: var(--fs-5); font-weight: 600; }
  .sv :global(.sv-lead) { margin: 0; color: var(--text); font-size: var(--fs-3); max-width: 62ch; }
  .sv :global(.sv-note) { margin: 0; color: var(--text-2); font-size: var(--fs-2); overflow-wrap: anywhere; }
  .sv :global(.sv-bad) { margin: 0; color: var(--bad); font-size: var(--fs-3); overflow-wrap: anywhere; }
  .sv :global(.sv-actions) { display: flex; flex-wrap: wrap; align-items: center; gap: var(--space-2); }
  .sv :global(.sv-form) { display: grid; gap: var(--space-3); }
  .sv :global(.sv-field) { display: grid; gap: 4px; min-width: 0; margin: 0; padding: 0; border: 0; }
  .sv :global(.sv-field > span), .sv :global(.sv-field > legend) { color: var(--text); font-size: var(--fs-3); font-weight: 500; padding: 0; }
  .sv :global(.sv-field small) { color: var(--text-2); font-size: var(--fs-2); }
  .sv :global(.sv-field input[type='text']), .sv :global(.sv-field input[type='password']), .sv :global(.sv-field input[type='file']), .sv :global(.sv-field select) {
    width: 100%; min-width: 0; padding: 6px 8px; font: inherit; font-size: var(--fs-3); color: var(--text); background: var(--surface); border: 1px solid var(--control-line); border-radius: var(--radius-sm); box-sizing: border-box;
  }
  .sv :global(.sv-field.sv-inline select) { width: auto; max-width: 100%; }
  .sv :global(.sv-radios) { display: flex; flex-wrap: wrap; gap: var(--space-2) var(--space-4); }
  .sv :global(.sv-radios legend) { flex: 0 0 100%; }
  .sv :global(.sv-radios label) { display: inline-flex; align-items: center; gap: 6px; font-size: var(--fs-3); overflow-wrap: anywhere; }
  .sv :global(.sv-check) { display: inline-flex; flex-wrap: wrap; align-items: center; gap: 6px 8px; font-size: var(--fs-3); min-width: 0; overflow-wrap: anywhere; }
  .sv :global(.sv-row) { display: grid; gap: var(--space-2); padding: var(--space-3) 0; border-top: 1px solid var(--line); min-width: 0; }
  .sv :global(.sv-row-main) { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--space-2); min-width: 0; overflow-wrap: anywhere; }
  .sv :global(.sv-chip) { display: inline-flex; align-items: center; min-height: 22px; padding: 1px 8px; color: var(--text-2); border: 1px solid var(--control-line); border-radius: var(--radius-sm); font-size: var(--fs-2); font-weight: 500; }
  .sv :global(.sv-tone-ok) { color: var(--tone-ok); border-color: var(--tone-ok); }
  .sv :global(.sv-tone-bad) { color: var(--bad); border-color: var(--bad); }
  .sv :global(.sv-tone-pending) { color: var(--pending); border-color: var(--pending); }
  .sv :global(h4) { margin: 0; }
</style>
