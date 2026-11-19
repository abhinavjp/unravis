<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { wizard, resetWizard, wizardIsDirty } from '../../lib/wizard.svelte';
  import { REVIEW_WINDOWS, blockers, chooseWindow, createNow, createView, setAck } from '../../lib/wizardCreate.svelte';
  import { importPlan } from '../../lib/wizardSubmit';
  import { listSourceTags } from '../../lib/caseTagsApi';
  import { sourcesView } from '../../lib/wizardSources.svelte';
  import { sourceKey } from '../../lib/wizardSources';
  import { groupOf } from '../../lib/discover';
  import { railGroups, canFinish } from '../../lib/sourceWizard';
  import { DISCOVER_TEXT, windowChoiceLabel } from '../../lib/copy/discoverCopy';
  import { kindLabel } from '../../lib/copy/wizardSourcesCopy';
  import { ACK_LABEL, GATING_BANNER } from '../../lib/copy/importCopy';
  import { WIZ, lastButton } from '../../lib/copy/sourceWizardCopy';
  import CreateProgress from '../wizard/CreateProgress.svelte';
  import SourceStep from './SourceStep.svelte';

  // Add sources to a case that is already open: the same Add sources step as the create wizard, then a short Review & add, then the run.
  // The case already exists, so the run starts with its id set and never makes a case.
  let { caseId, readOnly = false, onDone }: { caseId: string; readOnly?: boolean; onDone: () => void } = $props();

  type Screen = 'add' | 'review';
  let screen = $state<Screen>('add');
  let existing = $state<Array<{ kind: string; label: string }>>([]);
  let head = $state<HTMLHeadingElement>();
  let flowEl = $state<HTMLDivElement>();
  const NL = String.fromCharCode(10);
  let caseEnvs: string[] = [];
  const plan = $derived(importPlan(wizard.sources));
  const needsNotice = $derived(plan.scan + plan.files > 0);
  const pending = $derived(blockers());
  const started = $derived(createView.submit.caseId !== '');
  const titleOf = (s: { kind: string; ref: string }): string => {
    if (s.kind !== 'discover') return kindLabel(s.kind);
    const f = sourcesView.findings.find((x) => x.id === s.ref);
    return f ? DISCOVER_TEXT.groups[groupOf(f)] : kindLabel('discover');
  };
  const groups = $derived(railGroups(wizard.sources, titleOf));
  const closed = $derived(readOnly);
  // An unfinished new-case draft is not thrown away without a word: this screen asks first.
  const resume = createView.appendOnly && createView.submit.caseId === caseId;
  let askDiscard = $state(!resume && wizardIsDirty());
  const ready = $derived(canFinish(true, wizard.sources.length, createView.running, closed) && pending.length === 0);

  onMount(() => {
    // A draft from the create wizard is not carried into this case: start from an empty list.
    if (!resume && !askDiscard) resetWizard();
    const mine = new AbortController();
    void listSourceTags(caseId, mine.signal).then((tags) => {
      existing = tags.map((t) => ({ kind: t.kind, label: t.label }));
      // The environments the case already uses, so a new source is checked against them. They are never written back (the run skips that step).
      caseEnvs = [...new Set(tags.map((t) => (t.env ?? '').trim()).filter((e) => e !== ''))];
      if (!askDiscard) wizard.envText = caseEnvs.join(NL);
    }).catch(() => { /* the list above stays empty */ });
    return () => { mine.abort(); if (!createView.running && !askDiscard) resetWizard(); };
  });

  async function go(to: Screen) { screen = to; await tick(); (to === 'review' ? head : flowEl)?.focus(); }

  async function run() {
    // The blockers are read fresh (a scan can expire while this screen is open); the run skips them once the id is set.
    if (!ready || blockers().length > 0) return;
    createView.appendOnly = true;
    createView.submit.caseId = caseId;
    createView.submit.caseTitle = '';
    await createNow();
  }
</script>

<div class="flow" tabindex="-1" bind:this={flowEl}>
  {#if askDiscard}
    <section role="alert">
      <p>{WIZ.draftWarning}</p>
      <button type="button" class="primary-button" onclick={() => { resetWizard(); askDiscard = false; wizard.envText = caseEnvs.join(NL); }}>{WIZ.draftDiscard}</button>
    </section>
  {:else if started}
    <CreateProgress adding onOpen={() => { resetWizard(); onDone(); }} onAddMore={() => { resetWizard(); screen = 'add'; }} />
  {:else if screen === 'add'}
    <SourceStep adding {caseId} {closed} {existing} />
    <div class="actions">
      <button type="button" class="primary-button" disabled={wizard.sources.length === 0 || closed} onclick={() => void go('review')}>Review</button>
    </div>
  {:else}
    <section class="review" aria-labelledby="add-review-h">
      <h3 id="add-review-h" tabindex="-1" bind:this={head}>Review &amp; add</h3>
      {#each groups as g (g.title)}
        <p class="gtitle">{g.title}</p>
        <ul>{#each g.items as s (sourceKey(s))}<li>{s.label || s.ref}</li>{/each}</ul>
      {/each}
      {#if plan.scan > 0}
        <label class="field">How far back to read the logs of this computer
          <select value={createView.window} onchange={(e) => chooseWindow((e.currentTarget as HTMLSelectElement).value as typeof createView.window)}>
            {#each REVIEW_WINDOWS as w (w)}<option value={w}>{windowChoiceLabel(w)}</option>{/each}
          </select>
        </label>
      {/if}
      {#if needsNotice}
        <p class="hint">{GATING_BANNER}</p>
        <label class="ack"><input type="checkbox" checked={createView.ack} onchange={(e) => setAck((e.currentTarget as HTMLInputElement).checked)} /> {ACK_LABEL}</label>
      {/if}
      {#if pending.includes('scan-busy') || pending.includes('scan-stale')}<p class="inline-error" role="alert">The list of things found on this PC is out of date. Go back and use Look again.</p>{/if}
      {#if pending.includes('files')}<p class="inline-error" role="alert">A file is missing. Go back and pick it again.</p>{/if}
      {#if pending.includes('zone')}<p class="inline-error" role="alert">Name the computer each file came from. Go back to the Files card.</p>{/if}
      <p class="hint">{WIZ.reviewStays} {WIZ.reviewStale}</p>
      <p class="hint">{WIZ.footerAdd}</p>
      <div class="actions">
        <button type="button" class="quiet-button" onclick={() => void go('add')}>← Back</button>
        <button type="button" class="primary-button" disabled={!ready} onclick={() => void run()}>{lastButton(true, wizard.sources.length, createView.running)}</button>
      </div>
    </section>
  {/if}
</div>

<style>
  .flow:focus { outline: none; }
  .flow { display: grid; gap: var(--space-4); min-width: 0; }
  .review { display: grid; gap: var(--space-3); max-width: 70ch; }
  .review h3 { margin: 0; font-size: var(--fs-5); } .review h3:focus { outline: none; }
  .review ul { margin: 0; padding-left: var(--space-5); }
  .gtitle { margin: 0; font-weight: 600; }
  .hint { margin: 0; color: var(--text-2); font-size: var(--fs-3); }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-3); justify-content: space-between; }
  .ack { display: flex; gap: var(--space-2); align-items: flex-start; }
  .field { display: grid; gap: var(--space-1); }
</style>
