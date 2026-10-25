<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { finishReset } from '../lib/appState.svelte';
  import { canReset, resetFailureOf } from '../lib/masterKey';
  import type { KeyStatus } from '../lib/masterKey';
  import { resetDamagedKey } from '../lib/masterKeyApi';
  import { DAMAGED_TEXT, resetErrorText } from '../lib/copy/masterKeyCopy';

  // The master key file on this PC cannot be read by any key. This screen says so, says nothing was changed, and offers one way on:
  // set the old file aside and make a new key. That button stays off until the box is ticked, and no request is sent without the tick.
  // Nothing is deleted: the server renames the file, and only its name (never a path) is shown here.
  let ack = $state(false);
  let busy = $state(false);
  let problem = $state('');
  let phase = $state<'offer' | 'done'>('offer');
  let keptAs = $state('');
  // The answer is kept here until Continue: the app's key state stays "damaged" until then, so this screen is not swapped out mid-read.
  let result: KeyStatus | null = null;
  let title = $state<HTMLHeadingElement>();
  let box = $state<HTMLInputElement>();

  onMount(() => { void tick().then(() => title?.focus()); });

  const ready = $derived(canReset(ack) && !busy);

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !canReset(ack)) return;
    busy = true;
    problem = '';
    try {
      const answer = await resetDamagedKey(true);
      result = answer.status;
      keptAs = answer.keptAs ?? '';
      phase = 'done';
      busy = false;
      await tick();
      title?.focus();
    } catch (error) {
      problem = resetErrorText(resetFailureOf(error).kind);
      // The box is disabled while busy, and a disabled control cannot take focus: clear busy and let the page update first.
      busy = false;
      await tick();
      box?.focus();
    } finally {
      busy = false;
    }
  }

  function carryOn() {
    if (result) finishReset(result);
  }
</script>

<section class="damaged" aria-labelledby="damaged-title">
  {#if phase === 'offer'}
    <h2 id="damaged-title" tabindex="-1" bind:this={title}>{DAMAGED_TEXT.title}</h2>
    <p class="intro">{DAMAGED_TEXT.what}</p>
    <p class="intro">{DAMAGED_TEXT.unchanged}</p>
    <p class="intro">{DAMAGED_TEXT.dpapi}</p>
    <p class="intro">{DAMAGED_TEXT.option}</p>
    <p class="intro">{DAMAGED_TEXT.keptAside}</p>
    <p class="warning">{DAMAGED_TEXT.warning}</p>
    <form onsubmit={submit}>
      <label class="ack"><input type="checkbox" bind:this={box} bind:checked={ack} disabled={busy} aria-describedby={problem ? 'damaged-problem' : undefined} /> <span>{DAMAGED_TEXT.ackLabel}</span></label>
      <p id="damaged-problem" class="inline-error" role="alert" hidden={!problem}>{problem}</p>
      <div class="actions"><button type="submit" class="primary-button" disabled={!ready}>{busy ? DAMAGED_TEXT.resetting : DAMAGED_TEXT.reset}</button></div>
    </form>
  {:else}
    <h2 id="damaged-title" tabindex="-1" bind:this={title}>{DAMAGED_TEXT.doneTitle}</h2>
    {#if keptAs}<p class="intro">{DAMAGED_TEXT.doneKept} <code>{keptAs}</code>.</p>{/if}
    <p class="intro">{DAMAGED_TEXT.doneNext}</p>
    <div class="actions"><button type="button" class="primary-button" onclick={carryOn}>{DAMAGED_TEXT.continue}</button></div>
  {/if}
</section>

<style>
  .damaged { display: grid; gap: var(--space-3); max-width: 560px; padding: var(--space-4); background: var(--surface); border: var(--hairline-width) solid var(--line); border-radius: var(--radius-sm); font-family: var(--mono); }
  .damaged h2 { margin: 0; font-size: 20px; font-weight: 600; }
  .damaged h2:focus-visible { outline: 2px solid var(--focus); outline-offset: 2px; }
  .intro, .warning { margin: 0; font-size: var(--fs-3); }
  .intro { color: var(--text-2); }
  .warning { font-weight: 600; }
  code { overflow-wrap: anywhere; }
  form { display: grid; gap: var(--space-2); justify-items: start; }
  .ack { display: flex; align-items: flex-start; gap: var(--space-2); font-size: var(--fs-3); }
  .ack input { margin-top: 3px; }
  .ack input:focus-visible { outline: 2px solid var(--focus); outline-offset: 1px; }
  .inline-error { margin: 0; }
  .inline-error[hidden] { display: none; }
  .actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
</style>
