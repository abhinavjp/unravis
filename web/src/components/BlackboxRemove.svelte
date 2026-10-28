<script lang="ts">
  import { BlackboxError, blackboxApi, refusalText, removeConfirmed, type BlackboxItem, type RemoveAnswer, type ThisPc } from '../lib/blackbox';
  import { BB_REFUSAL, BB_TEXT as T } from '../lib/copy/blackboxCopy';

  // Remove a Blackbox. On this PC: stop and uninstall the service, keep or delete the recordings (typing the name
  // turns that button on). On another server Unravis does nothing there: it shows the two commands and can forget the place.
  let { item, thisPc, onChanged, onCancel }: {
    item: BlackboxItem;
    thisPc: ThisPc;
    onChanged: () => void;
    onCancel: () => void;
  } = $props();

  type Phase = { kind: 'idle' } | { kind: 'working' } | { kind: 'done'; text: string } | { kind: 'failed'; text: string; missing?: boolean };
  const gone = $derived(item.thisPc && item.state.state === 'not-placed');
  const here = $derived(item.thisPc && !gone);
  const typedName = $derived(thisPc.name || item.name);
  let deleteRecordings = $state(false);
  let typed = $state('');
  let phase = $state<Phase>({ kind: 'idle' });
  const busy = $derived(phase.kind === 'working');
  const ready = $derived(removeConfirmed(deleteRecordings, typed, typedName));

  async function removeHere() {
    if (busy || !ready) return;
    phase = { kind: 'working' };
    try {
      const answer: RemoveAnswer = await blackboxApi.remove(deleteRecordings, typed);
      phase = { kind: 'done', text: answer.recordingsDeleted ? T.removedDeleted(answer.filesDeleted) : T.removedKept };
    } catch (error) {
      phase = { kind: 'failed', text: refusalText(error, BB_REFUSAL) };
    }
  }
  async function forget() {
    if (busy) return;
    phase = { kind: 'working' };
    try {
      await blackboxApi.forget(item.id);
      phase = { kind: 'done', text: T.forgotten };
    } catch (error) {
      // 404: another window already took it out of the list. There is nothing left to forget, so the way on is the list.
      phase = { kind: 'failed', text: refusalText(error, BB_REFUSAL), missing: error instanceof BlackboxError && error.refusal.status === 404 };
    }
  }
</script>

<div class="bb-remove">
  <h3 tabindex="-1">{T.removeTitle(item.name)}</h3>

  {#if phase.kind === 'done'}
    <section class="bb-block" aria-label={T.removedTitle}>
      <h4>{T.removedTitle}</h4>
      <p role="status">{phase.text}</p>
    </section>
    <div class="bb-actions"><button type="button" class="primary-button" onclick={onChanged}>{T.back}</button></div>
  {:else if here}
    <p class="bb-note">{T.removeThisPc}</p>
    <fieldset class="bb-block bb-choice">
      <legend class="bb-sr">{T.removeTitle(item.name)}</legend>
      <label class="bb-radio"><input type="radio" name="bb-remove" checked={!deleteRecordings} onchange={() => { deleteRecordings = false; }} disabled={busy} />
        <span><strong>{T.removeKeep}</strong><span class="bb-note">{T.removeKeepText}</span></span></label>
      <label class="bb-radio"><input type="radio" name="bb-remove" checked={deleteRecordings} onchange={() => { deleteRecordings = true; }} disabled={busy} />
        <span><strong>{T.removeDelete}</strong><span class="bb-note">{T.removeDeleteText}</span></span></label>
    </fieldset>
    {#if deleteRecordings}
      <label class="bb-field"><span>{T.removeTypeName(typedName)}</span>
        <input type="text" bind:value={typed} autocomplete="off" spellcheck="false" aria-label={T.removeTypeLabel} />
      </label>
    {/if}
    <p class="bb-note">{T.removeCasesNote}</p>
    <p class="bb-note">{T.adminNote}</p>
    <div aria-live="polite">
      {#if busy}<p class="bb-note" role="status">{T.removing}</p>{/if}
      {#if phase.kind === 'failed'}<div class="inline-error" role="alert">{phase.text}</div>{/if}
    </div>
    <div class="bb-actions">
      <button type="button" class="secondary-button" disabled={busy || !ready} onclick={removeHere}>{T.removeButton}</button>
      <button type="button" class="primary-button" disabled={busy} onclick={onCancel}>{T.cancel}</button>
    </div>
  {:else if gone}
    <p class="bb-note">{T.removeNotPlaced}</p>
    <div aria-live="polite">
      {#if phase.kind === 'failed'}<div class="inline-error" role="alert">{phase.text}</div>{/if}
    </div>
    <div class="bb-actions">
      {#if phase.kind === 'failed' && phase.missing}
        <button type="button" class="primary-button" onclick={onChanged}>{T.back}</button>
      {:else}
        <button type="button" class="secondary-button" disabled={busy} onclick={forget}>{T.removeForget}</button>
        <button type="button" class="primary-button" disabled={busy} onclick={onCancel}>{T.cancel}</button>
      {/if}
    </div>
  {:else}
    <section class="bb-block" aria-label={T.removeOther}>
      <h4>{T.removeOther}</h4>
      <p class="bb-note">{T.removeOtherSteps}</p>
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <pre class="bb-code" tabindex="0" aria-label={T.removeOtherSteps}>{T.removeOtherCommands}</pre>
      <p class="bb-note">{T.removeOtherAfter}</p>
      <p class="bb-note">{T.forgetText}</p>
    </section>
    <div aria-live="polite">
      {#if phase.kind === 'failed'}<div class="inline-error" role="alert">{phase.text}</div>{/if}
    </div>
    <div class="bb-actions">
      {#if phase.kind === 'failed' && phase.missing}
        <button type="button" class="primary-button" onclick={onChanged}>{T.back}</button>
      {:else}
        <button type="button" class="secondary-button" disabled={busy} onclick={forget}>{T.removeForget}</button>
        <button type="button" class="primary-button" disabled={busy} onclick={onCancel}>{T.cancel}</button>
      {/if}
    </div>
  {/if}
</div>

<style>
  .bb-remove { display: grid; gap: var(--space-4); min-width: 0; }
  .bb-remove h3 { margin: 0; font-size: var(--fs-5); font-weight: 600; overflow-wrap: anywhere; }
  .bb-block { display: grid; gap: var(--space-2); margin: 0; padding: 0; border: 0; min-width: 0; }
  .bb-block h4 { margin: 0; font-size: 14px; font-weight: 600; }
  .bb-note { margin: 0; color: var(--text-2); font-size: 12px; line-height: 1.5; overflow-wrap: anywhere; }
  .bb-sr { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); white-space: nowrap; }
  .bb-radio { display: flex; align-items: flex-start; gap: 10px; padding: 10px 0; border-top: 1px solid var(--line); font-size: 14px; }
  .bb-radio input { flex: 0 0 auto; margin: 3px 0 0; }
  .bb-radio > span { display: grid; gap: 2px; min-width: 0; }
  .bb-field { display: grid; gap: 4px; font-size: 13px; font-weight: 500; }
  .bb-field input { width: 100%; max-width: 320px; padding: 6px 8px; font: inherit; font-weight: 400; color: var(--text); background: var(--surface); border: 1px solid var(--line); border-radius: var(--radius-sm); box-sizing: border-box; }
  .bb-code { margin: 0; padding: 8px 10px; font-family: var(--mono); font-size: 12px; background: var(--surface-2); border: 1px solid var(--line); border-radius: var(--radius-sm); overflow-x: auto; user-select: all; }
  .bb-actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }
</style>
