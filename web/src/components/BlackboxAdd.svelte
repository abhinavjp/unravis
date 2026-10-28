<script lang="ts">
  import { onMount } from 'svelte';
  import { BlackboxError, blackboxApi, needsKey, refusalText, shownTime, type BlackboxItem, type StateInfo, type TestAnswer } from '../lib/blackbox';
  import { BB_REFUSAL, BB_TEXT as T } from '../lib/copy/blackboxCopy';
  import Sticker from './BlackboxSticker.svelte';

  // Add a Blackbox that already runs: by folder, network share, or address and port. No network scan.
  // The read key goes up once in the add request. It is sealed on this PC and never comes back.
  let { initialLocation = '', initialName = '', onAdded, onOpen, onCancel }: {
    initialLocation?: string;
    initialName?: string;
    onAdded: (item: BlackboxItem) => void;
    onOpen: (item: BlackboxItem) => void;
    onCancel: () => void;
  } = $props();

  type Outcome =
    | { kind: 'none' }
    | { kind: 'working'; what: 'add' | 'check' }
    | { kind: 'checked'; answer: TestAnswer }
    | { kind: 'failed'; text: string; state?: StateInfo }
    | { kind: 'duplicate'; existing: BlackboxItem };

  // The fields start from the values the page passes in, then belong to the form.
  // svelte-ignore state_referenced_locally
  let location = $state(initialLocation);
  // svelte-ignore state_referenced_locally
  let name = $state(initialName);
  let key = $state('');
  let showKey = $state(false);
  let outcome = $state<Outcome>({ kind: 'none' });
  let problem = $state('');
  let first = $state<HTMLInputElement>();

  const wantsKey = $derived(needsKey(location));
  const busy = $derived(outcome.kind === 'working');

  onMount(() => first?.focus());

  function body() { return { name: name.trim(), location: location.trim(), key: wantsKey ? key : '' }; }
  function checkInput(): boolean {
    problem = '';
    if (!location.trim()) { problem = T.locationEmpty; return false; }
    if (wantsKey && !key) { problem = T.keyNeeded; return false; }
    return true;
  }
  function failed(error: unknown) {
    if (error instanceof BlackboxError && error.refusal.reason === 'duplicate' && error.refusal.existing) {
      outcome = { kind: 'duplicate', existing: error.refusal.existing };
      return;
    }
    const state = error instanceof BlackboxError ? error.refusal.state : undefined;
    outcome = { kind: 'failed', text: refusalText(error, BB_REFUSAL), state };
  }
  async function add() {
    if (busy || !checkInput()) return;
    outcome = { kind: 'working', what: 'add' };
    try {
      const item = await blackboxApi.add(body());
      key = '';
      onAdded(item);
    } catch (error) { failed(error); }
  }
  async function check() {
    if (busy || !checkInput()) return;
    outcome = { kind: 'working', what: 'check' };
    try {
      outcome = { kind: 'checked', answer: await blackboxApi.test(body()) };
    } catch (error) { failed(error); }
  }
</script>

<div class="bb-add">
  <h3 tabindex="-1">{T.addTitle}</h3>
  <section class="bb-block" aria-label={T.addWhere}>
    <h4>{T.addWhere}</h4>
    <label class="bb-field"><span>{T.addPlace}</span>
      <input bind:this={first} type="text" bind:value={location} autocomplete="off" spellcheck="false" placeholder={'D:\\UnravisBlackbox'} aria-describedby="bb-add-help" onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); add(); } }} />
    </label>
    <p class="bb-note" id="bb-add-help">{T.addPlaceHelp}</p>
    <label class="bb-field"><span>{T.addName}</span><input type="text" bind:value={name} autocomplete="off" spellcheck="false" aria-describedby="bb-add-name-help" /></label>
    <p class="bb-note" id="bb-add-name-help">{T.addNameHelp}</p>
    {#if wantsKey}
      <label class="bb-field"><span>{T.addKey}</span>
        <input type={showKey ? 'text' : 'password'} bind:value={key} autocomplete="off" spellcheck="false" aria-describedby="bb-add-key-help" />
      </label>
      <label class="bb-check"><input type="checkbox" bind:checked={showKey} /><span>{T.addKeyShow}</span></label>
      <p class="bb-note" id="bb-add-key-help">{T.addKeyHelp}</p>
      <p class="bb-note">{T.addHttpNote}</p>
    {/if}
  </section>

  <div aria-live="polite">
    {#if problem}<div class="inline-error" role="alert">{problem}</div>{/if}
    {#if outcome.kind === 'working'}<p class="bb-note" role="status">{outcome.what === 'add' ? T.adding : T.checking}</p>{/if}
    {#if outcome.kind === 'checked'}
      <div class="bb-result" role="status">
        <p><Sticker info={outcome.answer.state} /> <strong>{outcome.answer.name}</strong></p>
        <p class="bb-note">{outcome.answer.state.detail}</p>
        {#if outcome.answer.facts}<p class="bb-note">{T.lastSample}: {shownTime(outcome.answer.facts.lastSample)}. {T.samplesEvery} {outcome.answer.facts.intervalSeconds} {T.seconds}.</p>{/if}
      </div>
    {/if}
    {#if outcome.kind === 'failed'}
      <div class="inline-error" role="alert">
        <strong>{T.addFailTitle}</strong>
        <p>{outcome.text}</p>
        <p>{T.addNothingChanged}</p>
      </div>
      <details class="bb-reasons">
        <summary>{T.addFailLikely}</summary>
        <ul>{#each T.addFailReasons as reason, i (i)}<li>{reason}</li>{/each}</ul>
      </details>
    {/if}
    {#if outcome.kind === 'duplicate'}
      <div class="notice" role="alert">
        <strong>{T.duplicateTitle}</strong>
        <p>{T.duplicateText(outcome.existing.name)}</p>
        <button type="button" class="secondary-button" onclick={() => { if (outcome.kind === 'duplicate') onOpen(outcome.existing); }}>{T.duplicateOpen}</button>
      </div>
    {/if}
  </div>

  <div class="bb-actions">
    <button type="button" class="primary-button" disabled={busy} onclick={add}>{T.addButton}</button>
    <button type="button" class="secondary-button" disabled={busy} onclick={check}>{outcome.kind === 'checked' || outcome.kind === 'failed' ? T.addLookAgain : T.check}</button>
    <button type="button" class="quiet-button" onclick={onCancel}>{T.cancel}</button>
  </div>
</div>

<style>
  .bb-add { display: grid; gap: var(--space-4); min-width: 0; }
  .bb-add h3 { margin: 0; font-size: var(--fs-5); font-weight: 600; }
  .bb-block { display: grid; gap: var(--space-2); margin: 0; padding: 0; border: 0; min-width: 0; }
  .bb-block h4 { margin: 0; font-size: 14px; font-weight: 600; }
  .bb-note { margin: 0; color: var(--text-2); font-size: 12px; line-height: 1.5; overflow-wrap: anywhere; }
  .bb-field { display: grid; gap: 4px; font-size: 13px; font-weight: 500; }
  .bb-field input { width: 100%; max-width: 460px; padding: 6px 8px; font: inherit; font-weight: 400; color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); box-sizing: border-box; }
  .bb-check { display: flex; align-items: center; gap: 8px; font-size: 13px; }
  .bb-result p { margin: 0 0 4px; font-size: 13px; }
  .bb-reasons { margin-top: var(--space-2); font-size: 13px; }
  .bb-reasons ul { margin: var(--space-2) 0 0; padding-left: 18px; display: grid; gap: 4px; color: var(--text-2); }
  .bb-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
  .inline-error p, .notice p { margin: 4px 0 0; }
</style>
